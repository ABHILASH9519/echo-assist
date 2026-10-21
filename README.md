# Echo Assist

Echo Assist is a conversational productivity prototype with a task dashboard, REST API, MCP task tools, and optional Amazon Bedrock responses.

## Features

- Create, list, complete, and delete tasks.
- Natural-language task commands: `add prepare demo`, `list my tasks`, `complete prepare demo`.
- MCP Streamable HTTP endpoint at `POST /mcp` with `add_task`, `list_tasks`, and `complete_task` tools.
- Optional Amazon Bedrock Converse API for open-ended assistant responses.
- JSON-file task persistence for a simple prototype.

## Run locally

Requires Node.js 20 or later.

```bash
npm install
cp .env.example .env
npm start
```

On Windows PowerShell, use `Copy-Item .env.example .env` instead of `cp`. Open http://localhost:3000.

## Amazon Bedrock (optional)

1. Ensure your AWS account has access to a supported Bedrock model in your chosen region.
2. Set `AWS_REGION` and `BEDROCK_MODEL_ID` in the runtime environment.
3. Configure AWS credentials using an IAM role or your host's secret manager. Never commit credentials.
4. Restart and verify `GET /api/health` reports `aiConfigured: true`, then send an open-ended assistant request and verify the response.

Without Bedrock configuration, task commands still work locally; open-ended assistant replies explain the available commands.

## MCP

The server exposes a stateless Streamable HTTP MCP endpoint at `/mcp`. Use an MCP client that supports Streamable HTTP and point it to your deployed base URL plus `/mcp`. Verify using an MCP inspector/client before describing this integration as tested.

## API

- `GET /api/health`
- `GET /api/tasks`
- `POST /api/tasks` with `{"title":"Prepare demo"}`
- `PATCH /api/tasks/:id/complete`
- `DELETE /api/tasks/:id`
- `POST /api/assistant` with `{"message":"Add prepare demo"}`

## Deployment notes

The app listens on `0.0.0.0` and uses `PORT`. Set `DATA_DIR` to a writable persistent volume if your hosting platform supports it. Some serverless or ephemeral filesystems reset data on restart/redeploy; use a database for durable production storage.

## Security and limitations

This is a hackathon MVP, not a multi-user production service. It has no authentication or per-user task isolation. Do not store sensitive personal data. Configure Bedrock access through IAM/host secrets. Before production, add authentication, a managed database, request rate limiting, structured logging, and end-to-end tests.

## Hackathon submission checklist

- [ ] Test task CRUD in the deployed app.
- [ ] Verify the MCP endpoint with a real MCP client.
- [ ] Configure and verify a real Bedrock response before claiming the integration is active.
- [ ] Record a public English demo video under the event's time limit.
- [ ] Keep the Devpost description aligned with verified features.
