# Shelley CLI Reference

You have access to the Shelley CLI at `~/shelley-cli/bin/shelley` (aliased as `shelley`).

## Commands Overview

| Command | Description |
|---------|-------------|
| `shelley chat` | Interactive CLI chat mode |
| `shelley serve` | Start the web server |
| `shelley dashboard` | Start dashboard with coordinator management UI |
| `shelley coord` | Start coordinator server (headless) |
| `shelley status` | Show status of all Shelley services |
| `shelley watch` | Live CLI dashboard for coordinator |
| `shelley coord-cli <cmd>` | Manage coordinator from CLI |
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

---

## `shelley chat` - Interactive CLI

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
# Interactive chat
shelley chat

# Resume a specific conversation
shelley chat -conversation my-project

# Non-interactive mode for scripting
shelley chat -prompt "Explain this code" < main.go

# Auto-approve all tool operations
shelley chat -yes
```

---

## `shelley serve` - Web Server

```bash
shelley serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 9000 | Port to listen on |
| `-require-header` | | Require this header on API requests |

---

## `shelley dashboard` - Coordinator Dashboard

Starts the web dashboard for managing distributed tasks across multiple VMs.

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
| `-git-token` | (env) | GitHub token (env: GITHUB_TOKEN) |
| `-git-user` | | Git username for HTTPS auth |
| `-shelley-db` | ~/.config/shelley/shelley.db | Main shelley DB for conversation sync |
| `-auto-start` | false | Start coordinator automatically |
| `-install-script` | | Worker install method (see below) |
| `-host` | (auto) | Coordinator hostname for workers to connect back |

### Install Script Options

The `-install-script` flag controls how shelley is installed on worker VMs:

| Value | Description | Speed |
|-------|-------------|-------|
| `https` **(default)** | Workers download binary from coordinator's `/api/shelley-bin` | ~30 sec |
| `scp` | Copies coordinator's binary to workers via SSH | ~30 sec |
| URL | Runs custom install script (e.g., GitHub raw URL) | varies |
| (empty) | Falls back to building from source | ~2 min |

**Note:** The `https` method is the default and recommended approach. It reliably transfers the binary without SSH/SCP issues.

### Examples

```bash
# Start dashboard (coordinator started via UI)
shelley dashboard

# Start with coordinator auto-started (recommended)
shelley dashboard -auto-start

# Start with git integration for worker commits
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN -auto-start

# Use custom install script (workers get complete repo with docs)
shelley dashboard \
  -install-script 'https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh' \
  -auto-start
```

Access at `https://your-vm.exe.xyz:8080/`

---

## `shelley coord` - Coordinator Server (Headless)

Runs the coordinator without the dashboard UI. Useful for automation.

```bash
shelley coord [flags]
```

Same flags as dashboard except no `-coord-port` or `-auto-start`.

---

## `shelley igor` - File Transfer Server

Starts Igor for uploading files from local machine to VM.

```bash
shelley igor [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | 8099 | Port to listen on |
| `-dir` | ~/uploads | Upload directory |

### Usage

1. Run `shelley igor`
2. Open `https://your-vm.exe.xyz:8099/` in browser
3. Drag and drop files to upload
4. In Shelley CLI, use `/pick` to list and analyze uploads

---

## `shelley unpack-template` - Project Templates

```bash
shelley unpack-template <template-name> <directory>
```

Available templates: `go`

```bash
mkdir -p /home/exedev/myproject
shelley unpack-template go /home/exedev/myproject
cd /home/exedev/myproject
git init && git add . && git commit -m "Initial commit"
```

---

# Coordinator System - Distributed Task Execution

The coordinator enables running Shelley tasks in parallel across multiple exe.dev VMs.

**For a complete step-by-step setup guide, see [COORDINATOR_SETUP_GUIDE.md](COORDINATOR_SETUP_GUIDE.md)**

## Prerequisites: SSH Key Setup

To spawn worker VMs, your coordinator VM must be able to authenticate with exe.dev via SSH.
This requires an SSH key registered with your exe.dev account.

### Check if your VM is set up

```bash
# Test if you can access exe.dev
ssh exe.dev whoami
```

If this shows your email and SSH keys, you're ready to use the coordinator.

### Setting up a new VM

When you create a VM via the exe.dev website, it automatically:
1. Generates an SSH key pair at `~/.ssh/id_ed25519`
2. Registers that public key with your exe.dev account

If you're on a VM without this setup (e.g., manually installed Shelley), you need to:

1. **Generate an SSH key** (if not present):
   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
   ```

2. **Register the key with exe.dev**:
   - Run `ssh exe.dev browser` to get a magic login link
   - Open the link in your browser to log into exe.dev
   - Go to Account Settings and add your public key:
     ```bash
     cat ~/.ssh/id_ed25519.pub
     ```

3. **Verify it works**:
   ```bash
   ssh exe.dev whoami
   ssh exe.dev ls
   ```

Once set up, the coordinator can create/destroy worker VMs on your behalf.

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

```bash
# With git integration for worker commits
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN -auto-start
```

Access at `https://your-vm.exe.xyz:8080/`

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

## Task Groups (Batch Parallel Execution)

Group related tasks to run in parallel across workers:

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

Each prompt becomes a separate task running on a different worker VM.

## Scaling Workers

### Via Dashboard

Use the Workers section to:
- View current workers and their status
- Click "+" to scale up, "-" to scale down
- Click "Drain" to gracefully shut down all workers

### Via API

```bash
# Scale to 5 workers
curl -X POST "https://your-vm.exe.xyz:8080/api/scale?workers=5" \
  -H "X-Coordinator-Token: <token>"

# Drain all workers
curl -X POST https://your-vm.exe.xyz:8080/api/drain \
  -H "X-Coordinator-Token: <token>"
```

## Worker Behavior

1. **Spawning**: Workers are exe.dev VMs created with `ssh exe.dev create <prefix>-<n>`
2. **Execution**: Worker clones repo (if specified), runs Shelley with the prompt
3. **Git Integration**: Creates branch `task-{id}`, commits changes, pushes
4. **Completion**: Reports results, syncs conversation to main DB
5. **Auto-shutdown**: Idle workers shut down after 30 minutes

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

All API requests require: `X-Coordinator-Token: <token>` (or `?token=<token>`)

The token is displayed when the coordinator starts.

---

# Common Workflows

## Parallelize Tasks Across VMs

```bash
# 1. Start dashboard with git integration and install script
export GITHUB_TOKEN=ghp_xxx
shelley dashboard \
  -install-script 'https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh' \
  -git-token $GITHUB_TOKEN \
  -auto-start

# 2. Open https://your-vm.exe.xyz:8080/
# 3. Create a task group with multiple prompts
# 4. Workers spawn automatically and process tasks in parallel
# 5. Each task creates a branch with its changes
```

## File Upload and Analysis

```bash
# Terminal 1: Start Igor
shelley igor

# Browser: Open https://your-vm.exe.xyz:8099/ and upload files

# Terminal 2: Start chat and use /pick
shelley chat
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

---

## `shelley status` - Service Status

Shows the status of all Shelley services, including version info and coordinator details.

```bash
shelley status [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | false | Output as JSON |

### Example Output

```
🐡 Shelley Status
================

Commit:   e1e511b8958281d0fd07c70769868589f7d6184f
Built:    2026-01-16T02:28:35Z
Modified: false
Hostname: panther-gecko

📦 Services:
  ✅ coordinator (port 8080) [PID 8532]
  ✅ igor (port 8099) [PID 4625]

🎛️  Coordinator:
  Workers: 2
  Tasks:   0 queued, 0 running, 5 completed, 0 failed
  Token:   b133061fa13494039c1efae33ddd39de

🔗 URLs:
  Dashboard:    https://panther-gecko.exe.xyz:8080/
  Igor:         https://panther-gecko.exe.xyz:8099/
```

---

## `shelley watch` - Live CLI Dashboard

A terminal-based dashboard that auto-refreshes, showing real-time coordinator status.

```bash
shelley watch [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-token` | (auto) | Coordinator API token (auto-detected from journalctl) |
| `-port` | 8080 | Coordinator port |
| `-interval` | 2s | Refresh interval |

### Usage

```bash
# Start watching (token auto-detected)
shelley watch

# Custom refresh interval
shelley watch -interval 1s

# Explicit token
shelley watch -token abc123
```

Press `Ctrl+C` to exit.

---

## `shelley coord-cli` - Coordinator Management

Manage the coordinator from the command line without using the web UI.

```bash
shelley coord-cli [flags] <command>
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-token` | (auto) | Coordinator API token (auto-detected from journalctl) |
| `-port` | 8080 | Coordinator port |

### Commands

| Command | Description |
|---------|-------------|
| `add-task <prompt>` | Add a task to the queue |
| `tasks` | List all tasks |
| `task <id>` | Show task details |
| `workers` | List all workers |
| `scale <n>` | Scale to n workers |
| `drain` | Drain all workers |
| `kill-worker <id>` | Force remove a worker and delete its VM |
| `reset-task <id>` | Reset a stuck/orphaned task to queued status |
| `stuck` | Show all stuck/orphaned tasks |
| `reset-stuck` | Reset all stuck tasks to queued |
| `stats` | Show coordinator statistics |
| `clear-tasks` | Clear all tasks from queue |
| `clear-workers` | Remove all workers |
| `clear-all` | Reset coordinator (stop, delete DB, restart) |
| `api-help` | Show all API endpoints |

### Examples

```bash
# Add a task
shelley coord-cli add-task "Create a landing page for a coffee shop"

# Show task details
shelley coord-cli task abc12345

# List workers
shelley coord-cli workers

# Scale to 5 workers
shelley coord-cli scale 5

# Kill a hung worker
shelley coord-cli kill-worker wk-abc-123

# Reset a stuck task (e.g., assigned to dead worker)
shelley coord-cli reset-task abc12345

# Show stats
shelley coord-cli stats

# Show all API endpoints
shelley coord-cli api-help

# Drain workers gracefully
shelley coord-cli drain
```

### Example Output

```bash
$ shelley coord-cli stats
📊 Coordinator Stats
   Workers: 3 total (1 busy, 2 idle)
   Tasks:   2 queued, 1 running, 5 completed, 0 failed

$ shelley coord-cli workers
👷 Workers (3):
   ✅ wk-abc123 (idle)
   🔨 wk-def456 (busy)
   ✅ wk-ghi789 (idle)

$ shelley coord-cli tasks
📋 Tasks (8):
   🔨 a1b2c3d4: Create a landing page for DOOM...
   📥 e5f6g7h8: Create a landing page for Sonic...
   ✅ i9j0k1l2: Create a landing page for Mario...
```

---

# exe.dev SSH Quirks

When using the exe.dev SSH proxy, be aware of these behaviors:

## Flag Parsing

The exe.dev SSH proxy intercepts and parses flags from your commands. This can break common patterns:

```bash
# These FAIL - flags get intercepted:
ssh exe.dev ssh worker "curl -s http://example.com"     # -s intercepted
ssh exe.dev ssh worker "base64 -d"                       # -d intercepted
ssh exe.dev ssh worker "mkdir -p /path/to/dir"          # -p intercepted

# These WORK - entire command quoted:
ssh exe.dev "ssh worker 'curl -s http://example.com'"
ssh exe.dev "ssh worker 'base64 -d'"
ssh exe.dev "ssh worker 'mkdir -p /path/to/dir'"
```

**Rule:** Always wrap the entire remote command in quotes so exe.dev treats it as a single argument.

## File Transfer Between VMs

VMs cannot directly SSH or SCP to each other. All traffic goes through the exe.dev proxy.

### Method 1: Base64 Encoding (Small Files)

```bash
# Encode file and transfer
b64=$(cat /path/to/file.html | base64 -w0)
ssh exe.dev "ssh targetvm 'echo $b64 | base64 -d > /path/to/dest.html'"
```

### Method 2: HTTP Server (Large Files)

```bash
# On source VM - start temp HTTP server
cd /path/to/files && python3 -m http.server 8888 &

# On target VM - download file
ssh exe.dev "ssh targetvm 'curl -fsSL http://sourcevm.exe.xyz:8888/file.html -o /path/to/dest.html'"
```

### Method 3: Igor Upload Server

```bash
# On target VM
shelley igor -port 8099

# From browser - upload files to https://targetvm.exe.xyz:8099/
```

## Retrieving Files from Workers

After a coordinator task completes, files exist on the **worker VM**, not the coordinator:

```bash
# Get file from worker
ssh exe.dev "ssh wk-abc-123 'cat /home/exedev/output.html'" > local-output.html

# Or copy to coordinator first
ssh exe.dev "ssh wk-abc-123 'cat /home/exedev/output.html'" | ssh exe.dev "ssh coordinator 'cat > /home/exedev/output.html'"
```

---

# Parallel Task Patterns

## Creating Multiple Landing Pages

```bash
# Start coordinator
shelley dashboard -auto-start

# Add tasks
for game in "DOOM" "Quake" "Duke-Nukem" "Warcraft"; do
  shelley coord-cli add-task "Create a 90s-style landing page for $game. Save to /home/exedev/$game/index.html"
done

# Scale workers to match task count
shelley coord-cli scale 4

# Monitor progress
shelley watch
```

## Aggregating Results from Workers

After tasks complete, retrieve files from each worker:

```bash
# Get list of completed tasks with worker assignments
shelley coord-cli tasks

# For each completed task, copy file from worker
for worker in wk-abc-1 wk-abc-2 wk-abc-3; do
  ssh exe.dev "ssh $worker 'cat /home/exedev/output/index.html'" > results/$worker.html
done
```

## Running Tests in Parallel

```bash
# Create task group for test suites
shelley coord-cli add-task "cd /repo && go test ./pkg/auth/..."
shelley coord-cli add-task "cd /repo && go test ./pkg/api/..."
shelley coord-cli add-task "cd /repo && go test ./pkg/db/..."

# Scale and wait
shelley coord-cli scale 3
shelley watch
```

## Starting Background Processes on Remote VMs

**Problem:** This pattern HANGS:
```bash
ssh exe.dev "ssh vm 'nohup python3 -m http.server 8000 &'"
```

**Solutions:**

### Option 1: Use `timeout` to limit wait time
```bash
ssh exe.dev "ssh vm 'timeout 2 sh -c \"python3 -m http.server 8000 &\"'" || true
```

### Option 2: Use systemd (recommended for persistent services)
```bash
ssh exe.dev "ssh vm 'cat > /tmp/myserver.service << SERVICE
[Unit]
Description=My HTTP Server
[Service]
WorkingDirectory=/home/exedev/mysite
ExecStart=/usr/bin/python3 -m http.server 8000
Restart=always
SERVICE
sudo mv /tmp/myserver.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start myserver'"
```

### Option 3: Check if already running first
```bash
# Check if server is running, only start if not
ssh exe.dev "ssh vm 'pgrep -f \"http.server 8000\" || (cd /home/exedev/site && python3 -m http.server 8000 &)'" &
```

### Option 4: Use `at` for fire-and-forget
```bash
ssh exe.dev "ssh vm 'echo \"cd /home/exedev/site && python3 -m http.server 8000\" | at now'"
```
