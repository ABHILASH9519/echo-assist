import express from 'express';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomUUID } from 'node:crypto';
import { BedrockRuntimeClient, ConverseCommand } from '@aws-sdk/client-bedrock-runtime';
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import { z } from 'zod';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const app = express();
app.disable('x-powered-by');
app.use(express.json({ limit: '32kb' }));
app.use(express.static(path.join(__dirname, 'public')));

const dataDir = process.env.DATA_DIR || path.join(__dirname, '.data');
const dataFile = path.join(dataDir, 'tasks.json');
fs.mkdirSync(dataDir, { recursive: true });
let tasks = [];
let pool = null;
let storageMode = 'file';
if (process.env.DATABASE_URL) {
  try {
    const { Pool } = await import('pg');
    pool = new Pool({
      connectionString: process.env.DATABASE_URL,
      ssl: process.env.PGSSL === 'disable' ? false : { rejectUnauthorized: false },
      max: 5,
      idleTimeoutMillis: 30000,
      connectionTimeoutMillis: 8000
    });
    await pool.query(`CREATE TABLE IF NOT EXISTS echo_tasks (
      id TEXT PRIMARY KEY, title TEXT NOT NULL, details TEXT NOT NULL DEFAULT '',
      completed BOOLEAN NOT NULL DEFAULT FALSE, priority TEXT NOT NULL DEFAULT 'medium',
      due_date DATE NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      completed_at TIMESTAMPTZ NULL
    )`);
    const loaded = await pool.query('SELECT * FROM echo_tasks ORDER BY created_at DESC');
    tasks = loaded.rows.map(fromDb);
    storageMode = 'postgres';
    console.log('Echo Assist connected to PostgreSQL');
  } catch (error) {
    console.error('PostgreSQL setup failed; using file storage:', error?.message || error);
    if (pool) await pool.end().catch(() => {});
    pool = null;
    try { tasks = JSON.parse(fs.readFileSync(dataFile, 'utf8')); } catch { tasks = []; }
  }
} else {
  try { tasks = JSON.parse(fs.readFileSync(dataFile, 'utf8')); } catch { tasks = []; }
}
function fromDb(row) {
  return { id: row.id, title: row.title, details: row.details || '', completed: row.completed,
    priority: row.priority || 'medium', dueDate: row.due_date ? String(row.due_date).slice(0, 10) : null,
    createdAt: new Date(row.created_at).toISOString(), ...(row.completed_at ? { completedAt: new Date(row.completed_at).toISOString() } : {}) };
}
async function saveTasks() {
  if (pool) {
    const client = await pool.connect();
    try {
      await client.query('BEGIN');
      await client.query('DELETE FROM echo_tasks');
      for (const t of tasks) await client.query(
        'INSERT INTO echo_tasks (id,title,details,completed,priority,due_date,created_at,completed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)',
        [t.id,t.title,t.details || '',t.completed,t.priority || 'medium',t.dueDate || null,t.createdAt,t.completedAt || null]
      );
      await client.query('COMMIT');
    } catch (error) {
      await client.query('ROLLBACK').catch(() => {});
      throw error;
    } finally {
      client.release();
    }
    return;
  }
  fs.mkdirSync(dataDir, { recursive: true });
  fs.writeFileSync(dataFile, JSON.stringify(tasks, null, 2));
}
function cleanText(value, max = 180) {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}
function cleanPriority(value) {
  return ['low', 'medium', 'high'].includes(String(value || '').toLowerCase()) ? String(value).toLowerCase() : 'medium';
}
function cleanDueDate(value) {
  if (!value) return null;
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) throw new Error('Choose a valid due date.');
  const d = new Date(value + 'T00:00:00Z');
  if (Number.isNaN(d.getTime()) || d.toISOString().slice(0, 10) !== value) throw new Error('Choose a valid due date.');
  return value;
}
async function createTask(title, details = '', priority = 'medium', dueDate = null) {
  const cleanTitle = cleanText(title);
  if (!cleanTitle) throw new Error('Please enter a task title.');
  const task = { id: randomUUID(), title: cleanTitle, details: cleanText(details, 500), completed: false,
    priority: cleanPriority(priority), dueDate: cleanDueDate(dueDate), createdAt: new Date().toISOString() };
  tasks.unshift(task);
  try { await saveTasks(); } catch (error) { tasks = tasks.filter(t => t.id !== task.id); throw error; }
  return task;
}
async function completeTask(id) {
  const task = tasks.find(t => t.id === id);
  if (!task) throw new Error('Task not found.');
  const previous = { ...task };
  task.completed = true;
  task.completedAt = new Date().toISOString();
  try { await saveTasks(); } catch (error) { Object.assign(task, previous); throw error; }
  return task;
}
function agendaFor(date) {
  const rank = { high: 0, medium: 1, low: 2 };
  return tasks.filter(t => !t.completed && t.dueDate === date)
    .sort((a, b) => rank[a.priority || 'medium'] - rank[b.priority || 'medium']);
}
function localDateString(date = new Date()) {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
}
app.get('/api/health', (_req, res) => res.json({ ok: true, app: 'Echo Assist', aiConfigured: Boolean(process.env.BEDROCK_MODEL_ID), storage: storageMode, taskCount: tasks.length }));
app.get('/api/tasks', (_req, res) => res.json({ tasks }));
app.get('/api/agenda', (req, res) => {
  const date = req.query.date || localDateString();
  try { cleanDueDate(date); res.json({ date, tasks: agendaFor(date), overdue: tasks.filter(t => !t.completed && t.dueDate && t.dueDate < date) }); }
  catch { res.status(400).json({ error: 'Use a date in YYYY-MM-DD format.' }); }
});
app.post('/api/tasks', async (req, res) => {
  try { res.status(201).json({ task: await createTask(req.body?.title, req.body?.details, req.body?.priority, req.body?.dueDate) }); }
  catch (error) { res.status(400).json({ error: error.message }); }
});
app.patch('/api/tasks/:id', async (req, res) => {
  const task = tasks.find(t => t.id === req.params.id);
  if (!task) return res.status(404).json({ error: 'Task not found.' });
  const previous = { ...task };
  try {
    if (Object.hasOwn(req.body || {}, 'title')) {
      const title = cleanText(req.body.title);
      if (!title) throw new Error('Please enter a task title.');
      task.title = title;
    }
    if (Object.hasOwn(req.body || {}, 'details')) task.details = cleanText(req.body.details, 500);
    if (Object.hasOwn(req.body || {}, 'priority')) task.priority = cleanPriority(req.body.priority);
    if (Object.hasOwn(req.body || {}, 'dueDate')) task.dueDate = cleanDueDate(req.body.dueDate);
    await saveTasks();
    res.json({ task });
  } catch (error) { Object.assign(task, previous); res.status(400).json({ error: error.message }); }
});
app.patch('/api/tasks/:id/complete', async (req, res) => {
  try { res.json({ task: await completeTask(req.params.id) }); }
  catch (error) { res.status(404).json({ error: error.message }); }
});
app.delete('/api/tasks/:id', async (req, res) => {
  const before = tasks;
  tasks = tasks.filter(t => t.id !== req.params.id);
  if (tasks.length === before.length) return res.status(404).json({ error: 'Task not found.' });
  try { await saveTasks(); res.status(204).end(); }
  catch (error) { tasks = before; res.status(500).json({ error: 'Could not save task changes.' }); }
});

const bedrock = new BedrockRuntimeClient({ region: process.env.AWS_REGION || 'ap-south-1' });
app.post('/api/assistant', async (req, res) => {
  const message = cleanText(req.body?.message, 1000);
  if (!message) return res.status(400).json({ error: 'Enter a message first.' });
  try {
    const addMatch = message.match(/^(?:add|create|remember|remind me to)\s+(.+)$/i);
    if (addMatch) {
      let title = addMatch[1].trim();
      const priorityMatch = title.match(/\s+priority\s+(low|medium|high)\s*$/i);
      const priority = priorityMatch ? priorityMatch[1].toLowerCase() : 'medium';
      if (priorityMatch) title = title.slice(0, priorityMatch.index).trim();
      const dueMatch = title.match(/\s+(?:due|by)\s+(\d{4}-\d{2}-\d{2})\s*$/i);
      const dueDate = dueMatch ? dueMatch[1] : null;
      if (dueMatch) title = title.slice(0, dueMatch.index).trim();
      const task = await createTask(title, '', priority, dueDate);
      return res.json({ reply: `Added “${task.title}” with ${task.priority} priority${task.dueDate ? ` and a due date of ${task.dueDate}` : ''}.`, action: 'task_created', task, mode: 'local' });
    }
    if (/^(?:list|show|what are|view).*(?:tasks|to-dos|todo|things to do)/i.test(message) || /show my tasks|list my tasks/i.test(message)) {
      const open = tasks.filter(t => !t.completed);
      const summary = open.length ? open.map((t, i) => `${i + 1}. [${t.priority}]${t.dueDate ? ` (due ${t.dueDate})` : ''} ${t.title}`).join('\n') : 'You have no open tasks. Enjoy the breathing room!';
      return res.json({ reply: summary, action: 'task_listed', tasks: open, mode: 'local' });
    }
    if (/agenda|due today|today's tasks|todays tasks/i.test(message)) {
      const today = localDateString();
      const due = agendaFor(today);
      return res.json({ reply: due.length ? `Due today ( ${today} ):\n` + due.map((t,i)=>`${i+1}. [${t.priority}] ${t.title}`).join('\n') : `No open tasks are due today (${today}).`, tasks: due, action: 'agenda_listed', mode: 'local' });
    }
    if (/^(?:complete|finish|done|mark done)\s+/i.test(message)) {
      const wanted = message.replace(/^(?:complete|finish|done|mark done)\s+/i, '').trim().toLowerCase();
      const task = tasks.find(t => !t.completed && (t.title.toLowerCase() === wanted || t.title.toLowerCase().includes(wanted)));
      if (!task) return res.json({ reply: 'I could not find an open task matching that. Try “list my tasks” to see what is open.', mode: 'local' });
      await completeTask(task.id);
      return res.json({ reply: `Completed “${task.title}”. Nice work!`, action: 'task_completed', task, mode: 'local' });
    }
    if (process.env.BEDROCK_MODEL_ID) {
      const result = await bedrock.send(new ConverseCommand({
        modelId: process.env.BEDROCK_MODEL_ID,
        system: [{ text: 'You are Echo Assist, a concise and encouraging productivity assistant. Help the user plan tasks. Do not claim to have changed tasks unless the application confirms it. Task operations are handled separately by the application.' }],
        messages: [{ role: 'user', content: [{ text: message }] }],
        inferenceConfig: { maxTokens: 350, temperature: 0.4 }
      }));
      const reply = result.output?.message?.content?.map(c => c.text || '').join('').trim() || 'I could not generate a response just now.';
      return res.json({ reply, mode: 'bedrock' });
    }
    return res.json({ reply: 'I can manage tasks locally with “add prepare demo”, “list my tasks”, “complete prepare demo”, or “agenda today”. Add “priority high” or “due 2026-10-30” when creating a task. Open-ended AI responses are not enabled yet: configure BEDROCK_MODEL_ID and AWS credentials to connect Amazon Bedrock.', mode: 'local' });
  } catch (error) {
    console.error('Assistant request failed:', error?.name || error);
    return res.status(502).json({ error: 'The assistant could not complete that request. Check the server configuration and try again.' });
  }
});

// Stateless MCP endpoint: task operations share the same store and validation as the REST API.
app.post('/mcp', async (req, res) => {
  const mcp = new McpServer({ name: 'echo-assist', version: '1.1.0' });
  mcp.tool('add_task', 'Create a task in Echo Assist', {
    title: z.string().min(1).max(180), details: z.string().max(500).optional(),
    priority: z.enum(['low','medium','high']).optional(), due_date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).optional()
  }, async ({ title, details, priority, due_date }) => {
    try { return { content: [{ type: 'text', text: JSON.stringify(await createTask(title, details || '', priority, due_date || null)) }] }; }
    catch (error) { return { content: [{ type: 'text', text: error.message }], isError: true }; }
  });
  mcp.tool('list_tasks', 'List tasks in Echo Assist', { include_completed: z.boolean().optional() }, async ({ include_completed }) => {
    const result = include_completed ? tasks : tasks.filter(t => !t.completed);
    return { content: [{ type: 'text', text: JSON.stringify(result) }] };
  });
  mcp.tool('complete_task', 'Mark a task complete by its exact task ID', { task_id: z.string().min(1) }, async ({ task_id }) => {
    try { return { content: [{ type: 'text', text: JSON.stringify(await completeTask(task_id)) }] }; }
    catch (error) { return { content: [{ type: 'text', text: error.message }], isError: true }; }
  });
  const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
  res.on('close', () => { void transport.close(); void mcp.close(); });
  try {
    await mcp.connect(transport);
    await transport.handleRequest(req, res, req.body);
  } catch (error) {
    console.error('MCP request failed:', error?.message || error);
    if (!res.headersSent) res.status(500).json({ error: 'MCP request failed.' });
  }
});
app.get('/mcp', (_req, res) => res.status(405).set('Allow', 'POST').json({ error: 'Use POST for stateless MCP requests.' }));
app.delete('/mcp', (_req, res) => res.status(405).set('Allow', 'POST').json({ error: 'Stateless MCP sessions are not supported.' }));

const port = Number(process.env.PORT || 3000);
app.listen(port, '0.0.0.0', () => console.log(`Echo Assist listening on port ${port}`));
