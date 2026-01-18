# Shelley Coordinator

A task queue and worker pool for distributed Shelley execution on exe.dev VMs.

## Prerequisites

### Tailscale Account (Required)

The coordinator requires Tailscale for direct network connectivity and shared filesystem between coordinator and workers.

1. **Create a Tailscale account** at https://tailscale.com (free tier works)
2. **Generate an auth key**:
   - Go to Settings > Keys in the Tailscale admin console
   - Click "Generate auth key"
   - Enable **Reusable** (for multiple workers) and **Ephemeral** (auto-cleanup)
   - Copy the key (starts with `tskey-auth-`)
3. **Install Tailscale on your coordinator VM**:
   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up
   ```

## Quick Start

### Dashboard Mode (Recommended)

```bash
shelley dashboard -auto-start \
  -tailscale-authkey "tskey-auth-YOUR-KEY-HERE"
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

### CLI Mode (Programmatic Control)

```bash
# Add a single task
shelley coord-cli add-task "Create a landing page for Docker"

# Create a task group with multiple parallel tasks (prompts separated by |)
shelley coord-cli add-group "Landing Pages" \
  "Create docker.html" \| "Create k8s.html" \| "Create terraform.html"

# Scale workers
shelley coord-cli scale 3

# Monitor progress
shelley coord-cli stats
shelley coord-cli workers
shelley coord-cli groups
shelley coord-cli tasks

# Live auto-refreshing dashboard
shelley watch

# Clear failed worker records
shelley coord-cli clear-failed
```

See `shelley coord-cli --help` for all commands.

## Architecture

```
┌─────────────────┐     ┌─────────────────┐
│   Dashboard     │     │   Worker VMs    │
│  (browser UI)   │     │  (exe.dev VMs)  │
└────────┬────────┘     └────────┬────────┘
         │                       │
         │ exe.dev auth          │ Tailscale + API token
         ▼                       ▼
┌─────────────────────────────────────────┐
│           Coordinator Server            │
│                                         │
│  • Task queue (SQLite)                  │
│  • Worker management                    │
│  • Shared filesystem (~/shared)         │
│  • Auto-scaling                         │
│  • Git integration                      │
└─────────────────────────────────────────┘
```

### Shared Filesystem

With Tailscale enabled, workers mount `~/shared` from the coordinator via SSHFS:

```
Coordinator ~/shared/          Worker ~/shared/ (SSHFS mount)
├── source/      ────────────► ├── source/
│   └── <task-id>/              │   └── <task-id>/  (staged input files)
├── tasks/       ◄──────────── ├── tasks/
└── results/     ◄──────────── └── results/
```

- Put input files in `~/shared/source/` on coordinator
- Workers read from `~/shared/source/`, write to `~/shared/tasks/`
- Results immediately visible on coordinator

#### Task-Aware File Staging

Input files can be staged automatically when creating a task:

```bash
# Stage files when enqueueing a task
curl -X POST http://localhost:8081/api/enqueue \
  -H "X-Coordinator-Token: $API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Process the input data file and generate a report",
    "input_files": [
      {"path": "data.json", "content": "{\"test\": \"data\"}"},
      {"path": "config.yaml", "source": "common/config.yaml"}
    ]
  }'
```

Input file options:
- `path`: Destination path relative to `~/shared/source/<task-id>/`
- `content`: File content (string or base64 encoded for binary)
- `source`: Copy from existing file in `~/shared/source/` (alternative to content)

The worker's prompt is automatically augmented with the input directory location.

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

### Worker Context Template

Workers receive detailed context about their task, including:

- **Task Identity**: Task ID, worker ID, group name (if applicable)
- **File Ownership Rules**: What files the worker owns, what's forbidden, shared read-only paths
- **Output Format**: Structured DONE.md specification (see below)
- **Exit Protocol**: How to signal success, partial completion, or failure
- **Constraints**: Task timeout, idempotency expectations, no long-running servers
- **Git Workflow**: Branch naming, commit conventions, push instructions

The context is automatically injected into each task's prompt by the coordinator.

### Structured Output (DONE.md)

Workers write a `DONE.md` file to `~/shared/results/{task-id}/` with YAML frontmatter:

```markdown
---
status: success  # or: partial, failed
files_changed:
  - path: auth/login.go
    action: created
    lines_added: 45
    lines_removed: 0
  - path: auth/login_test.go
    action: modified
    lines_added: 80
    lines_removed: 5
tests:
  passed: 5
  failed: 0
  skipped: 1
merge_ready: true
blockers: []  # list any issues preventing completion
---

## Summary
Brief description of what was accomplished.

## Changes Made
Detailed explanation of changes.

## Testing
How the changes were tested.
```

**Status values:**
- `success` - Task completed fully
- `partial` - Task partially completed, blockers listed
- `failed` - Task could not be completed

The coordinator parses this file and stores structured JSON in the database. The dashboard displays:
- Status badge (green/yellow/red)
- Files changed with action icons (+/~/−) and line counts
- Test results (passed/failed/skipped)
- Blockers highlighted in red

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
| `-tailscale-authkey` | | Tailscale auth key for workers (required for shared filesystem) |
| `-install-script` | https | Worker install method: `scp` (recommended with Tailscale), `https`, or URL |

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
| `-tailscale-authkey` | | Tailscale auth key (required for shared filesystem) |

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

## File Ownership / Conflict Detection

To prevent parallel tasks from modifying the same files, you can specify file ownership:

### Per-Task File Ownership

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/enqueue \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Add input validation to auth/login.go",
    "repo_url": "https://github.com/user/repo.git",
    "owns_files": ["auth/login.go", "auth/login_test.go"],
    "forbidden_files": ["auth/session.go", "config/*"]
  }'
```

- **owns_files**: Glob patterns for files this task may modify
- **forbidden_files**: Glob patterns for files this task must NOT touch

### Group Tasks with File Ownership

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/group/create \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Code Review Batch",
    "repo_url": "https://github.com/user/repo.git",
    "tasks": [
      {"prompt": "Review auth/login.go", "owns_files": ["auth/login.go"]},
      {"prompt": "Review auth/session.go", "owns_files": ["auth/session.go"]},
      {"prompt": "Review auth/oauth.go", "owns_files": ["auth/oauth.go"]}
    ]
  }'
```

### Conflict Detection

When a worker requests a task, the coordinator checks for conflicts:
1. Tasks with overlapping `owns_files` patterns cannot run simultaneously
2. A task's `owns_files` cannot overlap with another running task's `forbidden_files`
3. Conflicting tasks stay queued until the blocking task completes

### Check Conflicts Before Enqueueing

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/check-conflicts \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "owns_files": ["auth/*.go"],
    "forbidden_files": ["config/*"]
  }'

# Response:
# {"has_conflicts": true, "conflicts": [{...}]}
```

### File-Based Templates (Dashboard)

When using file-based templates in the dashboard (e.g., "Code Review" for `*.go` files), each task automatically:
- Sets `owns_files` to the specific file it's working on
- Sets `forbidden_files` to all other files in the batch

This ensures parallel tasks never step on each other.

## Database Schema

- **task_groups** - id, name, description, repo_url, base_branch, status, tasks_total, tasks_completed, tasks_failed, timestamps
- **tasks** - id, prompt, status, priority, worker_id, result, error, repo_url, base_branch, branch_name, commit_sha, pr_url, group_id, conversation_id, owns_files, forbidden_files, timestamps
- **workers** - id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed
- **events** - audit log of all events


---

## File Transfer Between VMs

### HTTP Pull Pattern (Recommended)

Workers now automatically start an HTTP file server on port 8000 at startup. This serves the worker's home directory and makes files accessible via HTTPS:

```
https://worker-id.exe.xyz:8000/
```

**To transfer files from a worker to another VM:**

```bash
# On the destination VM, pull files from the worker:
curl -o /path/to/dest/file.html https://wk-abc-123.exe.xyz:8000/workspaces/task-id/output.html
```

**Benefits:**
- No SSH flag parsing issues
- Works reliably with exe.dev proxy
- Parallel downloads possible
- Easy to verify with HEAD requests

### SSH Flag Parsing Workaround

The exe.dev SSH wrapper parses flags before passing them to the remote command. To pass flags to commands on the remote VM, use `--` to stop flag parsing:

```bash
# This fails (flag consumed by exe.dev ssh):
ssh exe.dev ssh myvm 'base64 -d file.b64'

# This works:
ssh exe.dev ssh myvm -- 'base64 -d file.b64'
```

## Worker Health Monitoring

Workers send heartbeats every 30 seconds during task execution. The coordinator monitors these heartbeats to detect unhealthy workers:

| Health Status | Heartbeat Age | Action |
|---------------|---------------|--------|
| Healthy       | < 60 seconds  | Normal operation |
| Warning       | 60-120 seconds | Yellow indicator in dashboard |
| Unhealthy     | 120-300 seconds | Orange indicator, alert banner |
| Dead          | > 300 seconds | Auto-replaced, task reset to queued |

The coordinator automatically:
1. Detects dead workers (no heartbeat for >5 minutes)
2. Resets any in-progress task to "queued" for retry
3. Deletes the dead worker VM
4. Spawns a replacement worker

## Git Worktrees (Shared Repository)

When Tailscale is enabled, the coordinator uses **git worktrees** instead of having each worker clone the repo separately. This provides:

- **Shared git objects**: Workers share the same `.git` directory, making operations fast
- **Parallel branches**: Each task gets its own worktree (working directory) with its own branch
- **Real-time visibility**: All workers can see all branches immediately
- **Fast merges**: Merging branches is local (no network fetch needed)

### How It Works

```
~/shared/repos/
├── owner-repo/                    # Main clone (shared by all tasks)
│   ├── .git/                      # Shared git database
│   └── (main branch files)
├── owner-repo-task-abc123/        # Worktree for task abc123
│   └── (task-abc123 branch files)
├── owner-repo-task-def456/        # Worktree for task def456
│   └── (task-def456 branch files)
└── owner-repo-task-ghi789/        # Worktree for task ghi789
    └── (task-ghi789 branch files)
```

Workers operate in their assigned worktree via SSHFS. Changes are immediately visible to other workers who can:
- See the branch in `git branch -a`
- Cherry-pick or merge changes
- Coordinate on related files

### API Endpoints

```bash
# List shared repositories
curl -H "X-Coordinator-Token: $TOKEN" http://localhost:8081/api/repos

# Clone a repository (creates shared repo)
curl -X POST http://localhost:8081/api/repo/create \
  -H "X-Coordinator-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"url": "https://github.com/owner/repo.git", "base_branch": "main"}'

# Fetch latest changes
curl -X POST "http://localhost:8081/api/repo/fetch?id=owner-repo" \
  -H "X-Coordinator-Token: $TOKEN"

# List worktrees for a repo
curl "http://localhost:8081/api/repo/worktrees?repo=owner-repo" \
  -H "X-Coordinator-Token: $TOKEN"
```

### Example: Parallel Feature Development

```bash
# Create a task group with related features
curl -X POST http://localhost:8081/api/group/create \
  -H "X-Coordinator-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Auth System v2",
    "repo_url": "https://github.com/myorg/myapp.git",
    "base_branch": "main",
    "prompts": [
      "Add OAuth2 login support in auth/oauth.go",
      "Add session management in auth/session.go", 
      "Add rate limiting middleware in middleware/ratelimit.go",
      "Update API docs for new auth endpoints"
    ]
  }'

# Scale to 4 workers (one per task)
curl -X POST "http://localhost:8081/api/scale?workers=4" \
  -H "X-Coordinator-Token: $TOKEN"
```

Each worker gets a worktree and works in parallel:
- Worker 1: `~/shared/repos/myorg-myapp-task-xxx/` (branch: task-xxx)
- Worker 2: `~/shared/repos/myorg-myapp-task-yyy/` (branch: task-yyy)
- Worker 3: `~/shared/repos/myorg-myapp-task-zzz/` (branch: task-zzz)
- Worker 4: `~/shared/repos/myorg-myapp-task-www/` (branch: task-www)

After completion, you can:
1. Review each branch's changes
2. Create PRs for each branch
3. Merge branches sequentially or use GitHub's merge queue
