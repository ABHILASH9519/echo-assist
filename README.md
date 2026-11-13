# Echo Assist — Go backend

Echo Assist is a productivity command center with an HTML/CSS/JavaScript frontend and a Go backend. The Go service provides task management, agenda and statistics APIs, optional PostgreSQL persistence, optional Amazon Bedrock responses, and stateless MCP task tools.

## Features

- Create, search, filter, paginate, update, complete, and delete tasks.
- Validate task titles, priorities, and due dates.
- Task priority (low, medium, high) and due dates.
- Daily agenda endpoint separates due-today and overdue tasks, sorted by priority.
- Statistics endpoint returns open/completed counts, overdue and due-today counts, and priority breakdown.
- Natural-language task commands for adding, listing, completing, and reviewing agenda tasks.
- MCP HTTP endpoint at POST /mcp with add_task, list_tasks, and complete_task tools.
- PostgreSQL persistence when DATABASE_URL is configured; JSON-file fallback for local development.
- Optional Amazon Bedrock Converse API for open-ended assistant responses.
- Request size limits, security headers, graceful shutdown, and automated API/MCP tests.

## Run locally

Requires Go 1.24 or later.

    go mod download
    go run .

Open http://localhost:3000. Keep the public directory alongside the executable.

## Configuration

- PORT defaults to 3000.
- AWS_REGION defaults to ap-south-1.
- APP_TIMEZONE defaults to Asia/Kolkata. Use an IANA time-zone name.
- BEDROCK_MODEL_ID must be set to a model ID available to your AWS account and region.
- DATABASE_URL enables PostgreSQL storage. For Render, set it in the service's Environment settings using the database's **internal connection string**. Never paste credentials into source control or chat.
- Configure AWS credentials through a secret manager or environment variables in the hosting dashboard. Do not commit AWS keys.

Health reports the runtime language, storage mode, database connectivity, AI credential readiness, configured time zone, task count, and uptime. A configured model ID alone is not enough: AWS credentials must also be available.

## API

- GET /api/health
- GET /api/tasks?status=open&priority=high&q=demo&limit=50&offset=0
- POST /api/tasks with a JSON title, optional details, priority, and dueDate
- PATCH /api/tasks/:id
- PATCH /api/tasks/:id/complete
- DELETE /api/tasks/:id
- GET /api/stats
- GET /api/agenda?date=YYYY-MM-DD
- POST /api/assistant with a JSON message
- POST /mcp for MCP JSON-RPC initialization, tool discovery, and tool calls

Task-list query parameters are optional. status accepts open, completed, or all; priority accepts low, medium, or high; limit is 1–200.

## Deploy to Render

The Dockerfile and render.yaml define a separate Go service. The existing Node deployment is intentionally kept as a fallback until the Go service is fully verified.

1. Configure DATABASE_URL with the internal connection string from the Render PostgreSQL dashboard.
2. Set BEDROCK_MODEL_ID and AWS credentials in Render's Environment settings if open-ended AI responses are required.
3. Set APP_TIMEZONE to the intended user time zone.
4. Verify /api/health reports storage=postgres, databaseHealthy=true, and aiConfigured=true when those integrations are configured.
5. Test task create/update/complete/delete, agenda, stats, and MCP tools before switching the primary URL.

The current Render free PostgreSQL database has an expiry date shown in its dashboard. File storage on ephemeral hosting may be lost after restarts; PostgreSQL is recommended for durable task data.

## Security and limitations

This is a hackathon MVP without authentication or per-user task isolation. Anyone who can reach the public endpoint may be able to read or change shared tasks. Do not store sensitive personal data. Before production, add authentication, user-scoped access, rate limiting, and a dedicated MCP client compatibility test. Verify a real Bedrock response before claiming the AI integration is live.
