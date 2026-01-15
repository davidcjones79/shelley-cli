# Shelley Coordinator - Project Status

**Last Updated:** January 15, 2026

## What's Built

### 1. Dashboard (`shelley dashboard`)
Web UI that manages coordinator as subprocess.

- **Port 8080** - Dashboard web UI
- **Port 8081** - Coordinator internal port
- Start/stop coordinator from browser
- exe.dev authentication required
- Themed to match exe.dev UI
- Help modal with documentation

### 2. Coordinator (`shelley coord`)
Task queue + worker pool for distributed Shelley execution.

- Spawns exe.dev worker VMs on demand
- Workers poll for tasks, run Shelley, report results
- SQLite database for tasks, workers, events
- Auto-scaling workers (configurable max)
- Idle timeout for worker shutdown

### 3. Git Integration
Workers can clone repos, make changes, and push.

- Clone repo, create task branch (`task-{id}`)
- Run Shelley with prompt in repo directory
- Commit changes and push to origin
- `-git-token` flag for GitHub/GitLab HTTPS auth
- Dashboard shows repo/branch/commit info
- "View Diff / Create PR" links in task details

## Key Files

```
coordinator/
├── coordinator.go      # Task queue, worker management, VM spawning
├── dashboard.go        # Dashboard server, subprocess management
├── dashboard.html      # Web UI (exe.dev themed)
├── handlers.go         # HTTP API handlers
├── schema.sql          # SQLite schema
├── webui.go           # Index page handler
├── README.md          # Documentation
└── PROJECT_STATUS.md  # This file
```

## Database Schema

- **tasks** - id, prompt, status, priority, worker_id, result, error, repo_url, base_branch, branch_name, commit_sha, pr_url, timestamps
- **workers** - id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed
- **events** - audit log of all events

## API Endpoints

### Dashboard
- `GET /` - Web UI
- `POST /dashboard/start` - Start coordinator
- `POST /dashboard/stop` - Stop coordinator
- `GET /dashboard/status` - Coordinator status + logs

### Tasks
- `POST /api/enqueue` - Add task (prompt, repo_url, base_branch)
- `GET /api/tasks` - List tasks
- `GET /api/task?id=` - Get task details
- `GET /api/stats` - Queue statistics

### Workers
- `GET /api/workers` - List workers
- `POST /api/scale?workers=N` - Scale worker count
- `GET /api/next-task?worker=` - Claim next task (worker use)
- `POST /api/complete` - Report task completion (worker use)

## Configuration

### Dashboard Flags
```
-port 8080              Dashboard HTTP port
-coord-port 8081        Coordinator internal port
-db coordinator.db      SQLite database path
-max-workers 10         Maximum worker VMs
-prefix wk              Worker VM name prefix
-git-token              GitHub token for workers (env: GITHUB_TOKEN)
-git-user               Git username for HTTPS auth
-auto-start             Start coordinator on dashboard startup
```

### Coordinator Flags
```
-port 8080              HTTP server port
-db coordinator.db      SQLite database path
-max-workers 10         Maximum workers
-prefix wk              Worker VM name prefix
-git-token              GitHub token (env: GITHUB_TOKEN)
-git-user               Git username
-host                   Coordinator hostname (auto-detected)
-token                  API token (auto-generated)
```

## To Run

```bash
cd /home/exedev/shelley-cli

# Build
go build -o bin/shelley ./cmd/shelley

# Run dashboard with git support
export GITHUB_TOKEN=ghp_xxx
./bin/shelley dashboard -git-token $GITHUB_TOKEN

# Access at: https://desert-daemon.exe.xyz:8080/
```

## What's Next (Planned)

1. **Task Groups / Batches**
   - Group multiple prompts into one batch
   - Track overall group progress
   - Common repo/branch for related tasks

2. **Progress Streaming**
   - Real-time output from workers
   - WebSocket or SSE for live updates
   - Show Shelley's progress in dashboard

3. **Auto-merge**
   - Combine completed task branches
   - Sequential merge strategy
   - Conflict detection/reporting

4. **Task Dependencies**
   - Task B waits for Task A
   - Chain tasks sequentially
   - Fan-out / fan-in patterns

## Recent Changes

### January 15, 2026
- Added git integration for workers
- Dashboard repo/branch input fields
- Task list shows git info (repo badge, branch, commit checkmark)
- Task detail modal with GitHub links
- Help modal with documentation
- exe.dev themed UI with SVG icons
- "The Laboratory" tagline
