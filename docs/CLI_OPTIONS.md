# Shelley CLI Command Line Options

Complete reference for all Shelley CLI commands and flags.

## Usage

```
shelley [global-flags] <command> [command-flags]
```

---

## Global Flags

These flags apply to all commands and must come **before** the command:

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | | Path to shelley.json configuration file |
| `-db` | `shelley.db` | Path to SQLite database file |
| `-debug` | `false` | Enable debug logging |
| `-model` | from config or `claude-opus-4.5` | LLM model to use |
| `-default-model` | `claude-opus-4.5` | Default model for web UI |
| `-predictable-only` | `false` | Use only the predictable service, ignoring all other models |

**Example:**
```bash
shelley -config ~/.config/shelley/shelley.json -db ~/.config/shelley/shelley.db chat
```

---

## Commands

### `chat` - Interactive CLI Chat

Start interactive CLI chat mode.

```bash
shelley chat [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-conversation` | | Resume specific conversation by ID or slug |
| `-no-browser` | `false` | Disable browser tools (screenshots, navigation, etc.) |
| `-no-sync` | `false` | Disable database sync (ephemeral conversation) |
| `-prompt` | | Initial prompt to send (for non-interactive mode) |
| `-sync` | `true` | Sync conversations with database (enables /conversations, /switch) |
| `-theme` | `auto` | Color theme: `dark`, `light`, or `auto` |
| `-verbose` | `false` | Show tool execution details |
| `-yes` | `false` | Auto-accept all tool operations (no confirmations) |

**Examples:**
```bash
# Interactive chat
shelley chat

# Resume a specific conversation
shelley chat -conversation my-project

# Non-interactive mode for scripting
cat main.go | shelley chat -prompt "Review this code for bugs"

# Auto-approve all tool operations
shelley chat -yes
```

---

### `serve` - Web Server

Start the web server.

```bash
shelley serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `9999` | Port to listen on |
| `-require-header` | | Require this header on all API requests (e.g., `X-Exedev-Userid`) |
| `-systemd-activation` | `false` | Use systemd socket activation (listen on fd from systemd) |

**Example:**
```bash
shelley serve -port 8080
```

---

### `dashboard` - Coordinator Dashboard

Start the dashboard server with coordinator management UI.

```bash
shelley dashboard [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-artifacts-dir` | `$TMPDIR/shelley-artifacts` | Directory to store quick task artifacts |
| `-auto-start` | `false` | Automatically start coordinator on dashboard startup |
| `-coord-port` | `8081` | Coordinator internal port |
| `-db` | `~/.config/shelley/coordinator.db` | SQLite database path |
| `-git-token` | env: `GITHUB_TOKEN` | GitHub/GitLab token for worker git push |
| `-git-user` | token owner | Git username for HTTPS auth |
| `-host` | auto-detected | Coordinator hostname |
| `-install-script` | `https` | Worker install method: `https` or URL to custom script |
| `-max-workers` | `10` | Maximum workers allowed |
| `-min-workers` | `0` | Minimum idle workers to maintain (pre-warm pool) |
| `-port` | `8080` | Dashboard HTTP server port |
| `-prefix` | `wk` | Worker VM name prefix |
| `-shelley-bin` | current binary | Path to shelley binary |
| `-shelley-db` | `~/.config/shelley/shelley.db` | Path to main shelley DB for syncing conversations |
| `-tailscale-authkey` | env: `TAILSCALE_AUTHKEY` | Tailscale auth key for workers to join private network |
| `-token` | auto-generated | API token |

**Examples:**
```bash
# Start dashboard (coordinator started via UI)
shelley dashboard

# Start with coordinator auto-started
shelley dashboard -auto-start

# With git integration
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN -auto-start
```

---

### `coord` - Coordinator Server (Headless)

Start the coordinator server for distributed task execution (without dashboard UI).

```bash
shelley coord [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-db` | `~/.config/shelley/coordinator.db` | SQLite database path |
| `-git-log` | `true` | Enable git logging of completed tasks |
| `-git-token` | env: `GITHUB_TOKEN` | GitHub/GitLab token for worker git push |
| `-git-user` | token owner | Git username for HTTPS auth |
| `-host` | auto-detected | Coordinator hostname |
| `-install-script` | `https` | Worker install method: `https` or URL to custom script |
| `-max-workers` | `10` | Maximum workers allowed |
| `-min-workers` | `0` | Minimum idle workers to maintain (pre-warm pool) |
| `-port` | `8080` | HTTP server port |
| `-prefix` | `wk` | Worker VM name prefix |
| `-shelley-bin` | current binary | Path to shelley binary |
| `-shelley-db` | | Path to main shelley DB for syncing conversations |
| `-tailscale-authkey` | env: `TAILSCALE_AUTHKEY` | Tailscale auth key for workers to join private network |
| `-token` | auto-generated | API token |

**Example:**
```bash
shelley coord -port 8081 -host my-coordinator.exe.xyz
```

---

### `coord-cli` - Coordinator CLI Management

Manage coordinator from command line.

```bash
shelley coord-cli [flags] <command>
```

**Flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8081` | Coordinator port (8081 for coord, 8080 for dashboard) |
| `-token` | auto-detected | Coordinator API token |

**Commands:**

| Command | Description |
|---------|-------------|
| `add-task <prompt>` | Add a task to the queue |
| `add-group <name> <prompts...>` | Create a task group (prompts separated by `\|`) |
| `tasks` | List tasks |
| `task <id>` | Show task details |
| `groups` | List task groups |
| `group <id>` | Show group details |
| `workers` | List workers |
| `scale <n>` | Scale to n workers |
| `drain` | Drain all workers |
| `kill-worker <id>` | Force remove a worker and delete its VM |
| `reset-task <id>` | Reset a stuck task to queued status |
| `stuck` | Show stuck/orphaned tasks |
| `reset-stuck` | Reset all stuck tasks to queued |
| `artifacts <task>` | List artifacts for a task |
| `stats` | Show coordinator stats |
| `clear-tasks` | Clear all tasks from the queue |
| `clear-workers` | Remove all workers |
| `clear-failed` | Clear failed worker records |
| `clear-all` | Clear tasks and workers, reset DB |
| `token` | Show the API token |
| `api-help` | Show all API endpoints |

**Examples:**
```bash
# Scale to 5 workers
shelley coord-cli scale 5

# Add a task
shelley coord-cli add-task "Create a landing page for Docker"

# Create a task group with multiple parallel tasks
shelley coord-cli add-group "Landing Pages" \
  "Create docker.html" \| "Create k8s.html" \| "Create terraform.html"

# Drain all workers
shelley coord-cli drain
```

---

### `watch` - Live Coordinator Dashboard

Live CLI dashboard showing coordinator status, workers, and tasks.

```bash
shelley watch [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-interval` | `2s` | Refresh interval |
| `-port` | `8080` | Coordinator port |
| `-token` | auto-detected | Coordinator API token |

**Example:**
```bash
shelley watch -interval 1s
```

---

### `igor` - File Transfer Server

Start Igor file transfer server for uploading files from local machine to VM.

```bash
shelley igor [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-dir` | `~/uploads` | Upload directory |
| `-port` | `8099` | Port to listen on |

**Usage:**
1. Run `shelley igor`
2. Open `https://your-vm.exe.xyz:8099/` in browser
3. Drag and drop files to upload
4. In Shelley CLI, use `/pick` to list and analyze uploads

---

### `status` - Service Status

Show status of Shelley services (coordinator, igor, etc.).

```bash
shelley status
```

No flags. Shows:
- Build information (commit, time, modified)
- Running services with PIDs and ports
- Coordinator stats if running
- Service URLs

---

### `unpack-template` - Project Templates

Unpack a project template to a directory.

```bash
shelley unpack-template <template-name> <directory>
```

**Available templates:** `go`

**Example:**
```bash
mkdir -p /home/exedev/myproject
shelley unpack-template go /home/exedev/myproject
cd /home/exedev/myproject
git init && git add . && git commit -m "Initial commit"
```

---

### `version` - Version Information

Print version information as JSON.

```bash
shelley version
```

**Output:**
```json
{
  "commit": "d8f2e476d92013804dda233b37d44e3a85c44b9c",
  "commit_time": "2026-01-18T05:52:19Z",
  "modified": true
}
```

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `GITHUB_TOKEN` | GitHub token for git operations (used by `-git-token`) |
| `TAILSCALE_AUTHKEY` | Tailscale auth key for worker networking |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `ANTHROPIC_BASE_URL` | Anthropic API base URL |
| `OPENAI_API_KEY` | OpenAI API key |
| `OPENAI_BASE_URL` | OpenAI API base URL |
| `FIREWORKS_API_KEY` | Fireworks API key |
| `FIREWORKS_BASE_URL` | Fireworks API base URL |

---

## See Also

- [CLI Reference](CLI_REFERENCE.md) - Complete documentation with workflows and examples
- [Coordinator Setup Guide](COORDINATOR_SETUP_GUIDE.md) - Distributed task execution setup
- [README](../README.md) - Project overview
