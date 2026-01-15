# Shelley Coordinator

A task queue and worker pool for distributed Shelley execution on exe.dev VMs.

## Quick Start

### Dashboard Mode (Recommended)

```bash
shelley dashboard
```

Access at `https://your-vm.exe.xyz:8080/`

The dashboard:
- Serves a web UI for managing tasks and workers
- Lets you start/stop the coordinator from the browser
- Proxies API requests to the coordinator
- Shows coordinator logs in real-time
- Supports task groups for batch operations
- Integrates with Git for repo cloning and branch creation

### Direct Coordinator Mode

```bash
shelley coord
```

Runs the coordinator directly without the dashboard wrapper. Useful for automation or when you don't need the web UI.

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
│  • Git integration                      │
└─────────────────────────────────────────┘
```

## Features

### Task Groups / Batches

Group related tasks together with shared settings:

- Tasks inherit `repo_url` and `base_branch` from their group
- Group progress tracking (tasks_total, tasks_completed, tasks_failed)
- Group status auto-updates when tasks complete
- Dashboard "Groups" section with create modal
- Multi-line prompt input (one task per line)

### Git Integration

Workers can clone repos, make changes, and push:

- Clone repo, create task branch (`task-{id}`)
- Run Shelley with prompt in repo directory
- Commit changes and push to origin
- `-git-token` flag for GitHub/GitLab HTTPS auth
- Dashboard shows repo/branch/commit info
- "View Diff / Create PR" links in task details

### Auto-scaling Workers

- Workers spawn automatically when tasks are enqueued
- Respects `max-workers` limit
- Idle workers automatically shut down after 30 minutes
- Manual scaling via `/api/scale` or dashboard UI

### Worker Drain

Gracefully shut down all workers:

- "Drain" button in Workers section
- Idle workers deleted immediately
- Busy workers complete current task then shut down
- No new workers spawned during drain

### Conversation Sync

View worker conversations in the main Shelley UI:

- Workers sync completed conversations to the coordinator's shelley DB
- "View Chat" button for completed/failed tasks
- "Live Chat" link for running tasks (connects to worker)
- Navigate to `/conversation/{id}` on the dashboard

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
| `-git-token` | (env) | GitHub token for workers (env: GITHUB_TOKEN) |
| `-git-user` | | Git username for HTTPS auth |
| `-shelley-db` | ~/.config/shelley/shelley.db | Main shelley DB for conversation sync |
| `-auto-start` | false | Start coordinator automatically |

### `shelley coord` Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8080 | HTTP server port |
| `-db` | coordinator.db | SQLite database path |
| `-prefix` | wk | Worker VM name prefix |
| `-max-workers` | 10 | Maximum workers |
| `-shelley-bin` | (self) | Path to shelley binary for workers |
| `-host` | (auto) | Coordinator hostname |
| `-token` | (auto) | API token (auto-generated) |
| `-git-token` | (env) | GitHub token (env: GITHUB_TOKEN) |
| `-git-user` | | Git username |
| `-shelley-db` | | Main shelley DB for conversation sync |
| `-git-log` | true | Enable git logging of completed tasks |

## API Endpoints

### Dashboard
- `GET /` - Web UI (requires exe.dev auth)
- `GET /conversation/{id}` - View synced conversation
- `POST /dashboard/start` - Start coordinator
- `POST /dashboard/stop` - Stop coordinator
- `GET /dashboard/status` - Coordinator status + logs

### Tasks
- `POST /api/enqueue` - Add task to queue
- `GET /api/tasks` - List all tasks
- `GET /api/task?id=<id>` - Get task details
- `GET /api/stats` - Queue and worker statistics

### Groups
- `POST /api/group/create` - Create a task group
- `GET /api/groups` - List all groups
- `GET /api/group?id=<id>` - Get group details
- `GET /api/group/tasks?id=<id>` - Get tasks in a group

### Workers
- `GET /api/workers` - List workers
- `POST /api/scale?workers=N` - Scale worker count
- `POST /api/drain` - Gracefully shut down all workers

### Worker Internal (used by workers)
- `GET /api/next-task?worker=<id>` - Claim next task
- `POST /api/complete` - Mark task complete
- `POST /api/worker-shutdown` - Worker shutdown notification
- `GET /shelley-bin` - Download shelley binary
- `POST /api/sync-conversation` - Sync conversation to main DB

## Example Usage

### Start Dashboard with Git Support

```bash
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN
```

Then open `https://your-vm.exe.xyz:8080/` in your browser.

### Enqueue a Task via API

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/enqueue \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Fix the bug in main.go",
    "repo_url": "https://github.com/user/repo.git",
    "base_branch": "main"
  }'
```

### Create a Task Group

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/group/create \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Auth System Updates",
    "repo_url": "https://github.com/user/repo.git",
    "base_branch": "main",
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
  https://your-vm.exe.xyz:8080/api/stats
```

### Scale Workers

```bash
# Scale to 5 workers
curl -X POST "https://your-vm.exe.xyz:8080/api/scale?workers=5" \
  -H "X-Coordinator-Token: <token>"

# Drain all workers
curl -X POST https://your-vm.exe.xyz:8080/api/drain \
  -H "X-Coordinator-Token: <token>"
```

## Security Notes

1. **Use a non-public port** - Ensures exe.dev proxy requires authentication
2. **Keep the API token secret** - Only share with authorized workers/clients
3. **Workers are isolated** - Each runs in its own exe.dev VM
4. **Git tokens are passed securely** - Not stored in database, passed via environment to workers

## Database Schema

- **task_groups** - id, name, description, repo_url, base_branch, status, tasks_total, tasks_completed, tasks_failed, timestamps
- **tasks** - id, prompt, status, priority, worker_id, result, error, repo_url, base_branch, branch_name, commit_sha, pr_url, group_id, conversation_id, timestamps
- **workers** - id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed
- **events** - audit log of all events
