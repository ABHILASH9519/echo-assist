# Echo Assist — Go backend

Echo Assist is a productivity command center with an HTML/CSS/JavaScript frontend and a Go backend. The Go service provides task management, an agenda API, optional PostgreSQL persistence, optional Amazon Bedrock responses, and stateless MCP task tools.

## Features
- Create, search, list, complete, update, and delete tasks.
- Task priority (low, medium, high) and due dates.
- Daily agenda endpoint includes due-today and overdue tasks.
- Natural-language task commands for adding, listing, completing, and reviewing agenda tasks.
- MCP HTTP endpoint at POST /mcp with add_task, list_tasks, and complete_task tools.
- PostgreSQL persistence when DATABASE_URL is configured; JSON-file fallback for local development.
- Optional Amazon Bedrock Converse API for open-ended assistant responses.

## Run locally
Requires Go 1.24 or later.

    go mod download
    go run .

Open http://localhost:3000. Keep the public directory alongside the executable.

## Configuration
PORT defaults to 3000. AWS_REGION defaults to ap-south-1. Set BEDROCK_MODEL_ID to a model available to your AWS account. Set DATABASE_URL for PostgreSQL persistence. Configure AWS credentials through IAM or your host's secret settings; never commit keys.

## API
- GET /api/health
- GET /api/tasks
- POST /api/tasks with a JSON title, optional details, priority, and dueDate
- PATCH /api/tasks/:id
- PATCH /api/tasks/:id/complete
- DELETE /api/tasks/:id
- GET /api/agenda?date=YYYY-MM-DD
- POST /api/assistant with a JSON message
- POST /mcp for MCP JSON-RPC initialization, tool discovery, and tool calls

## Deploy to Render
The Dockerfile and render.yaml define a separate Go service. Import the Blueprint or create a Docker web service from this repository and configure secret environment variables. This deliberately keeps the existing Node deployment untouched until the Go version is built and smoke-tested. Once verified, switch traffic and retire the old service.

For durable storage, connect PostgreSQL and verify /api/health reports storage=postgres. File storage on ephemeral hosting may be lost on restart.

## Security and limitations
This is a hackathon MVP without authentication or per-user task isolation. Do not store sensitive personal data. Before production, add authentication, user-scoped access, rate limiting, and integration tests. Verify MCP using a real MCP client and verify a real Bedrock response before claiming either integration is live.
