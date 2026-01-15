# Shelley CLI - Agent Guidelines

## Code Style

1. Never add sleeps to tests.
2. Brevity, brevity, brevity! Do not do weird defaults; have only one way of doing things; refactor relentlessly as necessary.
3. If something doesn't work, propagate the error or exit or crash. Do not have "fallbacks".
4. Do not keep old methods around for "compatibility"; this is a new project and there are no compatibility concerns yet.
5. Commit your changes before finishing your turn.

## Testing Guidelines

6. The "predictable" model is a test fixture that lets you specify what a model would say if you said a thing. This is useful for interactive testing with a browser, since you don't rely on a model, and can fabricate some inputs and outputs. To test things, launch shelley with the relevant flag to only expose this model, and use shelley with a browser.
7. Build the UI (`make ui` or `cd ui && pnpm install && pnpm run build`) before running Go tests so `ui/dist` exists for the embed.
8. Run Go unit tests with `go test ./server` (or narrower packages while iterating) once the UI bundle is built.
9. To programmatically type into the React message input (e.g., in browser automation), you must use React's internal setter:
   ```javascript
   const input = document.querySelector('[data-testid="message-input"]');
   const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
   nativeInputValueSetter.call(input, 'your message');
   input.dispatchEvent(new Event('input', { bubbles: true }));
   ```
   Simply setting `input.value = '...'` won't work because React won't detect the change.
10. If you are testing Shelley itself, be aware that you might be running "under" shelley, and indiscriminately running pkill -f shelley may break things.
11. To test the Shelley UI in a separate instance, build with `make build`, then run on a different port with a separate database:
    ```
    ./bin/shelley -config /exe.dev/shelley.json -db /tmp/shelley-test.db serve -port 8002
    ```
    Then use browser tools to navigate to http://localhost:8002/ and interact with the UI.

---

# Shelley CLI Reference

Shelley CLI is a terminal interface and distributed task system for the Shelley coding agent.

## Commands Overview

| Command | Description |
|---------|-------------|
| `shelley chat` | Interactive CLI chat mode |
| `shelley serve` | Start the web server |
| `shelley dashboard` | Start dashboard with coordinator management UI |
| `shelley coord` | Start coordinator server (headless) |
| `shelley igor` | Start Igor file transfer server |
| `shelley unpack-template <name> <dir>` | Unpack a project template |
| `shelley version` | Print version information as JSON |

## Global Flags

These flags apply to all commands and must come BEFORE the command:

```bash
shelley [global-flags] <command> [command-flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | | Path to shelley.json configuration file |
| `-db` | shelley.db | Path to SQLite database file |
| `-debug` | false | Enable debug logging |
| `-model` | (from config) | LLM model to use |
| `-default-model` | claude-opus-4.5 | Default model for web UI |
| `-predictable-only` | false | Use only the predictable test model |

---

## `shelley chat` - Interactive CLI

The primary interface for interactive coding sessions.

```bash
shelley chat [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-conversation ID` | | Resume specific conversation by ID or slug |
| `-no-sync` | false | Disable database sync (ephemeral conversation) |
| `-no-browser` | false | Disable browser tools |
| `-verbose` | false | Show tool execution details |
| `-yes` | false | Auto-accept all tool operations |
| `-prompt TEXT` | | Send initial prompt and exit (non-interactive) |
| `-theme` | auto | Color theme: dark, light, mono |

### Examples

```bash
# Interactive chat (default)
shelley chat

# Resume a specific conversation
shelley chat -conversation my-project

# Non-interactive mode for scripting
shelley chat -prompt "Explain this code" < main.go

# Ephemeral session (not saved to DB)
shelley chat -no-sync

# Auto-approve all tool operations
shelley chat -yes
```

### Slash Commands

In the CLI, type `/help` for full command list. Key commands:

| Command | Description |
|---------|-------------|
| `/models` | List available models |
| `/model <id>` | Switch model |
| `/fast`, `/smart`, `/think` | Quick model switches (Haiku/Sonnet/Opus) |
| `/conversations` | List recent conversations |
| `/switch [id]` | Switch conversation |
| `/new` | Start new conversation |
| `/sync` | Reload from database (picks up external changes) |
| `/pick [n]` | List uploads or pick file n to analyze |
| `/attach <path>` | Attach image to next message |
| `/export [file]` | Export conversation to markdown |
| `/verbose` | Toggle verbose mode |
| `/theme <name>` | Switch theme |
| `/status` | Show session info |

---

## `shelley serve` - Web Server

Starts the Shelley web UI.

```bash
shelley serve [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 9000 | Port to listen on |
| `-require-header` | | Require this header on API requests |
| `-systemd-activation` | false | Use systemd socket activation |

### Examples

```bash
# Start on default port 9000
shelley serve

# Start on custom port
shelley serve -port 8080

# With exe.dev authentication
shelley serve -require-header X-Exedev-Userid
```

---

## `shelley dashboard` - Coordinator Dashboard

Starts the web dashboard for managing the coordinator and distributed tasks.

```bash
shelley dashboard [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8080 | Dashboard HTTP server port |
| `-coord-port` | 8081 | Coordinator internal port |
| `-db` | coordinator.db | SQLite database path |
| `-prefix` | wk | Worker VM name prefix |
| `-max-workers` | 10 | Maximum concurrent workers |
| `-shelley-bin` | (self) | Path to shelley binary |
| `-host` | (auto) | Coordinator hostname |
| `-token` | (auto) | API token (auto-generated) |
| `-git-token` | (env) | GitHub token (env: GITHUB_TOKEN) |
| `-git-user` | | Git username for HTTPS auth |
| `-shelley-db` | ~/.config/shelley/shelley.db | Main shelley DB for conversation sync |
| `-auto-start` | false | Start coordinator automatically |

### Examples

```bash
# Start dashboard (coordinator started via UI)
shelley dashboard

# Start with coordinator auto-started
shelley dashboard -auto-start

# With Git integration for worker commits
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN

# Custom ports
shelley dashboard -port 8080 -coord-port 8081
```

### Dashboard Features

- **Task Queue**: View pending, running, completed, and failed tasks
- **Task Groups**: Create batches of related tasks with shared repo/branch
- **Workers**: View worker status, scale up/down, drain workers
- **Logs**: Real-time coordinator logs
- **Conversation Viewer**: View completed task conversations at `/conversation/{id}`

---

## `shelley coord` - Coordinator Server (Headless)

Runs the coordinator without the dashboard UI. Useful for automation.

```bash
shelley coord [flags]
```

### Flags

Same as `dashboard` except:
- No `-coord-port` (uses `-port` directly)
- No `-auto-start` (always running)

### Examples

```bash
# Start coordinator on port 8080
shelley coord -port 8080

# With git integration
export GITHUB_TOKEN=ghp_xxx
shelley coord -git-token $GITHUB_TOKEN -max-workers 5
```

---

## `shelley igor` - File Transfer Server

Starts Igor, the file transfer assistant for uploading files from your local machine to the VM.

```bash
shelley igor [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8099 | Port to listen on |
| `-dir` | ~/uploads | Upload directory |

### Examples

```bash
# Start Igor on default port
shelley igor

# Custom port and directory
shelley igor -port 9099 -dir /tmp/uploads
```

### Usage

1. Run `shelley igor` (or install as systemd service)
2. Open `https://your-vm.exe.xyz:8099/` in browser
3. Drag and drop files to upload
4. In Shelley CLI, use `/pick` to list and analyze uploads

---

## `shelley unpack-template` - Project Templates

Unpacks a project template to bootstrap new projects.

```bash
shelley unpack-template <template-name> <directory>
```

### Available Templates

- `go` - Go web server with SQLite, migrations, and systemd service

### Examples

```bash
# Create new Go project
mkdir -p /home/exedev/myproject
shelley unpack-template go /home/exedev/myproject
cd /home/exedev/myproject
git init && git add . && git commit -m "Initial commit"
```

---

# Coordinator System

The coordinator enables distributed task execution across multiple exe.dev VMs.

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
│  • Task queue (SQLite)                  │
│  • Worker management                    │
│  • Auto-scaling                         │
│  • Git integration                      │
└─────────────────────────────────────────┘
```

## Starting the Coordinator

### Option 1: Dashboard (Recommended)

```bash
export GITHUB_TOKEN=ghp_xxx  # For git integration
shelley dashboard -git-token $GITHUB_TOKEN
```

Access at `https://your-vm.exe.xyz:8080/`

### Option 2: Headless

```bash
shelley coord -port 8080 -max-workers 5
```

## Creating Tasks

### Via Dashboard UI

1. Open the dashboard in browser
2. Fill in the "New Task" form:
   - **Prompt**: What you want Shelley to do
   - **Repository URL**: Git repo to clone (optional)
   - **Base Branch**: Branch to start from (optional)
3. Click "Enqueue"

### Via API

```bash
# Single task
curl -X POST https://your-vm.exe.xyz:8080/api/enqueue \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Fix the bug in main.go",
    "repo_url": "https://github.com/user/repo.git",
    "base_branch": "main"
  }'
```

## Task Groups (Batches)

Group related tasks together:

```bash
curl -X POST https://your-vm.exe.xyz:8080/api/group/create \
  -H "X-Coordinator-Token: <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Refactor auth module",
    "repo_url": "https://github.com/user/repo.git",
    "base_branch": "main",
    "prompts": [
      "Add input validation to login",
      "Add rate limiting",
      "Fix failing tests"
    ]
  }'
```

Each prompt becomes a separate task that runs in parallel on different workers.

## Scaling Workers

### Via Dashboard

Use the Workers section to:
- View current workers and their status
- Click "+" to scale up
- Click "-" to scale down
- Click "Drain" to gracefully shut down all workers

### Via API

```bash
# Scale to 5 workers
curl -X POST "https://your-vm.exe.xyz:8080/api/scale?workers=5" \
  -H "X-Coordinator-Token: <token>"

# Drain all workers (graceful shutdown)
curl -X POST https://your-vm.exe.xyz:8080/api/drain \
  -H "X-Coordinator-Token: <token>"
```

## Worker Behavior

1. **Spawning**: Workers are exe.dev VMs created with `ssh exe.dev create <prefix>-<n>`
2. **Polling**: Workers poll the coordinator for tasks via `/api/next-task`
3. **Execution**: Worker clones repo (if specified), runs Shelley with the prompt
4. **Git Integration**: If repo specified, worker creates branch `task-{id}`, commits changes, pushes
5. **Completion**: Worker reports results via `/api/complete`
6. **Auto-shutdown**: Idle workers shut down after 30 minutes

## Viewing Results

### Task Status

- **queued**: Waiting for a worker
- **running**: Being processed by a worker
- **completed**: Successfully finished
- **failed**: Error occurred

### Conversation Sync

Completed tasks sync their Shelley conversation to the main database. View at:
- Dashboard: Click "View Chat" on completed tasks
- Direct: `/conversation/{conversation_id}` on the dashboard

### Git Results

For tasks with repos:
- Branch name: `task-{id}`
- Commit SHA shown in task details
- "View Diff" and "Create PR" links available

## API Reference

### Tasks
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/enqueue` | POST | Add task to queue |
| `/api/tasks` | GET | List all tasks |
| `/api/task?id=<id>` | GET | Get task details |
| `/api/stats` | GET | Queue statistics |

### Groups
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/group/create` | POST | Create task group |
| `/api/groups` | GET | List all groups |
| `/api/group?id=<id>` | GET | Get group details |
| `/api/group/tasks?id=<id>` | GET | Get tasks in group |

### Workers
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/workers` | GET | List workers |
| `/api/scale?workers=N` | POST | Scale worker count |
| `/api/drain` | POST | Gracefully shut down all |

### Authentication

All API requests require the coordinator token:
```
X-Coordinator-Token: <token>
```
Or as query param: `?token=<token>`

The token is displayed when the coordinator starts.

---

# Configuration

## Config File

Create `~/.config/shelley/shelley.json`:

```json
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
```

## Environment Variables

```bash
# LLM Gateway (exe.dev)
export LLM_GATEWAY="http://169.254.169.254/gateway/llm"

# Or direct API keys
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export FIREWORKS_API_KEY="..."

# Git token for coordinator workers
export GITHUB_TOKEN="ghp_..."
```

## Recommended Alias

```bash
alias shelley='~/shelley-cli/bin/shelley -config ~/.config/shelley/shelley.json -db ~/.config/shelley/shelley.db'
```

---

# Common Workflows

## Interactive Coding Session

```bash
shelley chat
```

## Parallelize Tasks Across VMs

```bash
# 1. Start dashboard with git integration
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN -auto-start

# 2. Open https://your-vm.exe.xyz:8080/
# 3. Create a task group with multiple prompts
# 4. Workers will spawn automatically and process tasks in parallel
# 5. View results in dashboard, each task creates a branch
```

## File Upload and Analysis

```bash
# 1. Start Igor
shelley igor

# 2. Open https://your-vm.exe.xyz:8099/ and upload files

# 3. In another terminal, start chat
shelley chat

# 4. Use /pick to analyze uploads
/pick      # List uploads
/pick 1    # Analyze most recent file
```

## Non-Interactive Scripting

```bash
# Analyze a file
cat main.go | shelley chat -prompt "Review this code for bugs"

# Generate code
shelley chat -prompt "Write a Python function to parse JSON" -yes
```
