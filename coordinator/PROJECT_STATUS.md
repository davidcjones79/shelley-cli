# Shelley Coordinator - Project Status

**Last Updated:** January 16, 2026

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

- **task_groups** - id, name, description, repo_url, base_branch, status, tasks_total, tasks_completed, tasks_failed, timestamps
- **tasks** - id, prompt, status, priority, worker_id, result, error, repo_url, base_branch, branch_name, commit_sha, pr_url, group_id, timestamps
- **workers** - id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed
- **events** - audit log of all events

## API Endpoints

### Dashboard
- `GET /` - Web UI
- `POST /dashboard/start` - Start coordinator
- `POST /dashboard/stop` - Stop coordinator
- `GET /dashboard/status` - Coordinator status + logs

### Tasks
- `POST /api/enqueue` - Add task (prompt, repo_url, base_branch, group_id)
- `GET /api/tasks` - List tasks
- `GET /api/task?id=` - Get task details
- `GET /api/stats` - Queue statistics

### Groups
- `POST /api/group/create` - Create group (name, description, repo_url, base_branch, prompts[])
- `GET /api/groups` - List groups
- `GET /api/group?id=` - Get group details
- `GET /api/group/tasks?id=` - Get tasks in a group

### Workers
- `GET /api/workers` - List workers
- `POST /api/scale?workers=N` - Scale worker count
- `POST /api/drain` - Gracefully shut down all workers
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

## CLI Commands (`shelley coord-cli`)

```bash
# Task management
shelley coord-cli add-task "Create a landing page"
shelley coord-cli add-group "Pages" "docker.html" \| "k8s.html" \| "terraform.html"
shelley coord-cli tasks
shelley coord-cli task <id>
shelley coord-cli groups
shelley coord-cli group <id>

# Worker management
shelley coord-cli workers
shelley coord-cli scale 5
shelley coord-cli drain
shelley coord-cli clear-failed
shelley coord-cli kill-worker <id>

# Monitoring
shelley coord-cli stats
shelley watch  # Live auto-refreshing dashboard

# Troubleshooting
shelley coord-cli stuck
shelley coord-cli reset-stuck
shelley coord-cli reset-task <id>
```

## What's Next (Planned)

1. **Artifact Management**
   - Automatic artifact collection from workers
   - HTTP-based file transfer (workers serve files on port 8000)
   - `shelley artifacts collect` command

2. **Multi-Step Workflows**
   - Task dependencies (B waits for A)
   - Fan-out / fan-in patterns
   - Consolidation steps

3. **Auto-merge**
   - Combine completed task branches
   - Sequential merge strategy
   - Conflict detection/reporting

## Recent Changes

### January 16, 2026 (Latest)

- **Reliability Improvements**
  - Persistent worker prefix (survives coordinator restarts)
  - Startup cleanup (cleans orphans immediately on start)
  - Reduced failed worker retention (10 min instead of 1 hour)
  - Worker error display (shows why workers failed)
  - Fixed conversation sync "argument list too long" error

- **New CLI Commands**
  - `add-group` - Create task groups with pipe-separated prompts
  - `groups` - List all task groups
  - `group <id>` - Show group details and tasks
  - `clear-failed` - Clear failed worker records
  - `shelley watch` - Live auto-refreshing CLI dashboard

- **Dashboard UI Improvements**
  - "Show failed" checkbox to toggle failed worker visibility
  - "Clear Failed" button to remove failed worker records
  - Failed worker count badge in Workers section
  - Error messages displayed for failed workers

- **Task Groups / Batches**
  - New `task_groups` table to group related tasks
  - Tasks inherit repo_url and base_branch from their group
  - Group progress tracking (tasks_total, tasks_completed, tasks_failed)
  - API endpoints: `/api/groups`, `/api/group`, `/api/group/create`, `/api/group/tasks`

- **Worker Features**
  - HTTP file server on port 8000 (for artifact transfer)
  - Auto-scaling workers when tasks enqueued
  - Drain feature for graceful shutdown
  - Health monitoring with heartbeat staleness detection

- **API Additions**
  - `GET /api/workers?show_failed=true` - Include failed workers
  - `POST /api/workers/clear-failed` - Clear failed worker records

### January 15, 2026
- Added git integration for workers
- Dashboard repo/branch input fields
- Task list shows git info (repo badge, branch, commit checkmark)
- Task detail modal with GitHub links
- Help modal with documentation
- exe.dev themed UI with SVG icons
- "The Laboratory" tagline
