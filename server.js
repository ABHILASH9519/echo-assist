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
try { tasks = JSON.parse(fs.readFileSync(dataFile, 'utf8')); } catch { tasks = []; }
function saveTasks() {
  fs.mkdirSync(dataDir, { recursive: true });
  fs.writeFileSync(dataFile, JSON.stringify(tasks, null, 2));
}
function cleanText(value, max = 180) {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}
function createTask(title, details = '') {
  const cleanTitle = cleanText(title);
  if (!cleanTitle) throw new Error('Please enter a task title.');
  const task = { id: randomUUID(), title: cleanTitle, details: cleanText(details, 500), completed: false, createdAt: new Date().toISOString() };
  tasks.unshift(task);
  saveTasks();
  return task;
}
function completeTask(id) {
  const task = tasks.find(t => t.id === id);
  if (!task) throw new Error('Task not found.');
  task.completed = true;
  task.completedAt = new Date().toISOString();
  saveTasks();
  return task;
}
app.get('/api/health', (_req, res) => res.json({ ok: true, app: 'Echo Assist', aiConfigured: Boolean(process.env.BEDROCK_MODEL_ID), taskCount: tasks.length }));
app.get('/api/tasks', (_req, res) => res.json({ tasks }));
app.post('/api/tasks', (req, res) => {
  try { res.status(201).json({ task: createTask(req.body?.title, req.body?.details) }); }
  catch (error) { res.status(400).json({ error: error.message }); }
});
app.patch('/api/tasks/:id/complete', (req, res) => {
  try { res.json({ task: completeTask(req.params.id) }); }
  catch (error) { res.status(404).json({ error: error.message }); }
});
app.delete('/api/tasks/:id', (req, res) => {
  const before = tasks.length;
  tasks = tasks.filter(t => t.id !== req.params.id);
  if (tasks.length === before) return res.status(404).json({ error: 'Task not found.' });
  saveTasks();
  res.status(204).end();
});

const bedrock = new BedrockRuntimeClient({ region: process.env.AWS_REGION || 'ap-south-1' });
app.post('/api/assistant', async (req, res) => {
  const message = cleanText(req.body?.message, 1000);
  if (!message) return res.status(400).json({ error: 'Enter a message first.' });
  const lower = message.toLowerCase();
  try {
    const addMatch = message.match(/^(?:add|create|remember|remind me to)\s+(.+)$/i);
    if (addMatch) {
      const task = createTask(addMatch[1]);
      return res.json({ reply: `Added “${task.title}” to your tasks.`, action: 'task_created', task, mode: 'local' });
    }
    if (/^(?:list|show|what are|view).*(?:tasks|to-dos|todo|things to do)/i.test(message) || /show my tasks|list my tasks/i.test(message)) {
      const open = tasks.filter(t => !t.completed);
      const summary = open.length ? open.map((t, i) => `${i + 1}. ${t.title}`).join('\n') : 'You have no open tasks. Enjoy the breathing room!';
      return res.json({ reply: summary, action: 'task_listed', tasks: open, mode: 'local' });
    }
    if (/^(?:complete|finish|done|mark done)\s+/i.test(message)) {
      const wanted = message.replace(/^(?:complete|finish|done|mark done)\s+/i, '').trim().toLowerCase();
      const task = tasks.find(t => !t.completed && (t.title.toLowerCase() === wanted || t.title.toLowerCase().includes(wanted)));
      if (!task) return res.json({ reply: 'I could not find an open task matching that. Try “list my tasks” to see what is open.', mode: 'local' });
      completeTask(task.id);
      return res.json({ reply: `Completed “${task.title}”. Nice work!`, action: 'task_completed', task, mode: 'local' });
    }
    if (process.env.BEDROCK_MODEL_ID) {
      const result = await bedrock.send(new ConverseCommand({
        modelId: process.env.BEDROCK_MODEL_ID,
        system: [{ text: 'You are Echo Assist, a concise and encouraging productivity assistant. Help the user plan tasks. Do not claim to have changed tasks unless the application confirms it.' }],
        messages: [{ role: 'user', content: [{ text: message }] }],
        inferenceConfig: { maxTokens: 350, temperature: 0.4 }
      }));
      const reply = result.output?.message?.content?.map(c => c.text || '').join('').trim() || 'I could not generate a response just now.';
      return res.json({ reply, mode: 'bedrock' });
    }
    return res.json({ reply: 'I can manage tasks with commands like “add prepare demo”, “list my tasks”, or “complete prepare demo”. For open-ended AI conversations, configure BEDROCK_MODEL_ID and AWS credentials.', mode: 'local' });
  } catch (error) {
    console.error('Assistant request failed:', error?.name || error);
    return res.status(502).json({ error: 'The assistant could not complete that request. Check the server configuration and try again.' });
  }
});

// Stateless MCP endpoint: each request creates a server/transport pair. Task operations share the app's task store.
app.post('/mcp', async (req, res) => {
  const mcp = new McpServer({ name: 'echo-assist', version: '1.0.0' });
  mcp.tool('add_task', 'Create a task in Echo Assist', { title: z.string().min(1).max(180), details: z.string().max(500).optional() }, async ({ title, details }) => {
    try { return { content: [{ type: 'text', text: JSON.stringify(createTask(title, details || '')) }] }; }
    catch (error) { return { content: [{ type: 'text', text: error.message }], isError: true }; }
  });
  mcp.tool('list_tasks', 'List tasks in Echo Assist', { include_completed: z.boolean().optional() }, async ({ include_completed }) => {
    const result = include_completed ? tasks : tasks.filter(t => !t.completed);
    return { content: [{ type: 'text', text: JSON.stringify(result) }] };
  });
  mcp.tool('complete_task', 'Mark a task complete by its exact task ID', { task_id: z.string().min(1) }, async ({ task_id }) => {
    try { return { content: [{ type: 'text', text: JSON.stringify(completeTask(task_id)) }] }; }
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
