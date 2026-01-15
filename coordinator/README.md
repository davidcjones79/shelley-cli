# Shelley Coordinator

A task queue and worker pool for distributed Shelley execution on exe.dev VMs.

## Quick Start

### Dashboard Mode (Recommended)

```bash
shelley dashboard
```

Access at `https://your-vm.exe.xyz:8080/`

The dashboard:
- Serves the web UI
- Lets you start/stop the coordinator from the browser
- Proxies API requests to the coordinator
- Shows coordinator logs in real-time

### Direct Coordinator Mode

```bash
shelley coord
```

Runs the coordinator directly without the dashboard wrapper.

## Architecture

```
┌─────────────────┐     ┌─────────────────┐
│   Dashboard     │     │   Worker VMs    │
│  (browser UI)   │     │  (exe.dev VMs)  │
└────────┬────────┘     └────────┬────────┘
         │                       │
         │ exe.dev auth          │ API token
         ▼                       ▼
┌─────────────────────────────────────────┐
│           Coordinator Server            │
│                                         │
│  • Task queue (SQLite)                  │
│  • Worker management                    │
│  • Auto-scaling                         │
└─────────────────────────────────────────┘
```

## Authentication

The coordinator has two authentication mechanisms:

### Dashboard (Browser Access)

Requires exe.dev authentication. When accessing via the exe.dev proxy:
- Non-public ports automatically require exe.dev login
- The proxy adds `X-Exedev-Userid` header for authenticated users
- Unauthenticated requests redirect to `/__exe.dev/login`

**Important**: Run on a non-public port (not the port set via `ssh exe.dev share port`).

### API (Worker Access)

Workers authenticate using the API token:
- Token is auto-generated on startup (displayed in logs)
- Pass via `X-Coordinator-Token` header or `?token=` query param
- Workers receive the token when spawned by the coordinator

## Flags

| Flag | Default | Description |
|------|---------|-------------|
### `shelley dashboard` Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8080 | Dashboard HTTP server port |
| `-coord-port` | 8081 | Coordinator internal port |
| `-db` | coordinator.db | SQLite database path |
| `-prefix` | wk | Worker VM name prefix |
| `-max-workers` | 10 | Maximum concurrent workers |
| `-shelley-bin` | (self) | Path to shelley binary |
| `-host` | (auto) | Coordinator hostname |
| `-token` | (auto) | API token (auto-generated if empty) |
| `-auto-start` | false | Start coordinator automatically |

### `shelley coord` Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8080 | HTTP server port |
| `-db` | coordinator.db | SQLite database path |
| `-prefix` | wk | Worker VM name prefix |
| `-max-workers` | 10 | Maximum concurrent workers |
| `-shelley-bin` | (self) | Path to shelley binary for workers |
| `-host` | (auto) | Coordinator hostname |
| `-token` | (auto) | API token (auto-generated if empty) |

## API Endpoints

### Dashboard
- `GET /` - Web UI (requires exe.dev auth)

### Tasks
- `POST /api/enqueue` - Add task to queue
- `GET /api/tasks` - List all tasks
- `GET /api/task?id=<id>` - Get task details

### Groups
- `POST /api/group/create` - Create a task group
- `GET /api/groups` - List all groups
- `GET /api/group?id=<id>` - Get group details
- `GET /api/group/tasks?id=<id>` - Get tasks in a group

### Workers
- `GET /api/workers` - List workers
- `POST /api/scale` - Scale worker count
- `GET /api/stats` - Queue and worker statistics

### Worker Internal
- `GET /api/next-task` - Claim next task (worker use)
- `POST /api/complete` - Mark task complete (worker use)
- `POST /api/worker-shutdown` - Worker shutdown notification
- `GET /shelley-bin` - Download shelley binary

## Example Usage

### Start Coordinator

```bash
# Run on non-public port for security
shelley coord -port 8001 -max-workers 5
```

### Enqueue a Task

```bash
curl -X POST https://your-vm.exe.xyz:8001/api/enqueue \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Fix the bug in main.go", "repo_url": "https://github.com/user/repo.git"}'
```

### Create a Task Group

```bash
curl -X POST https://your-vm.exe.xyz:8001/api/group/create \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Auth System Updates",
    "repo_url": "https://github.com/user/repo.git",
    "prompts": [
      "Add input validation to login form",
      "Add rate limiting to login endpoint",
      "Fix failing auth tests"
    ]
  }'
```

### Check Status

```bash
curl -H "X-Coordinator-Token: <token>" \
  https://your-vm.exe.xyz:8001/api/stats
```

## Security Notes

1. **Use a non-public port** - Ensures exe.dev proxy requires authentication
2. **Keep the API token secret** - Only share with authorized workers/clients
3. **Workers are isolated** - Each runs in its own exe.dev VM
