# Shelley Coordinator Project Status

**Last Updated:** 2026-01-18

## Overview

The Shelley coordinator system distributes tasks across multiple exe.dev worker VMs for parallel execution. Workers run shelley chat autonomously and report results back to the coordinator.

## Current Status: ✅ FULLY WORKING

Successfully tested with:
- Parallel tasks across multiple workers
- Full end-to-end task execution with file creation
- Task completes in ~6 seconds for simple tasks

## Quick Start

```bash
# On coordinator VM (e.g., panther-gecko)
cd ~/shelley-cli

# Start coordinator
nohup ./bin/shelley coord \
  -port 8081 \
  -db coordinator.db \
  -max-workers 10 \
  -prefix wk \
  -host $(hostname).exe.xyz \
  > /tmp/coord.log 2>&1 &

# Get token from log
TOKEN=$(grep -A1 "API TOKEN" /tmp/coord.log | tail -1)
echo "Token: $TOKEN"

# Scale workers
curl -sk -X POST -H "X-Coordinator-Token: $TOKEN" "https://$(hostname).exe.xyz:8081/api/scale?workers=3"

# Submit a task
curl -sk -X POST "https://$(hostname).exe.xyz:8081/api/enqueue" \
  -H "Content-Type: application/json" \
  -H "X-Coordinator-Token: $TOKEN" \
  -d '{"prompt": "Create a hello world HTML file"}'

# Monitor tasks
curl -sk -H "X-Coordinator-Token: $TOKEN" "https://$(hostname).exe.xyz:8081/api/tasks" | jq .
```

## Key Fixes Applied

### 1. Worker Idle Timeout
**Problem:** Workers self-shutdown after 2 minutes idle
**Fix:** Changed `MAX_IDLE=360` (30 minutes)

### 2. Shell Quoting Through SSH Proxy (CRITICAL)
**Problem:** Commands with `bash -c` or heredocs failed through exe.dev SSH proxy
**Cause:** `sshToWorker()` helper had issues with nested quoting
**Fix:** Replace ALL `sshToWorker()` calls with direct `exec.Command()` calls

```go
// BAD - sshToWorker has quoting issues
sshToWorker(workerID, "bash", "-c", "some command")

// GOOD - direct exec.Command
exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'some command'", workerID))
```

### 3. Worker Loop Script Quoting (CRITICAL)
**Problem:** jq expressions lost their quotes when written via heredoc
**Example:** `jq -r '.id // empty'` became `jq -r .id // empty` causing jq errors
**Fix:** Use base64 encoding to transfer script content verbatim

```go
// Encode script as base64 to preserve all characters
scriptB64 := base64.StdEncoding.EncodeToString([]byte(pollScript))
cmd := exec.Command("ssh", "exe.dev",
    fmt.Sprintf("ssh %s 'echo %s | base64 -d > /tmp/worker-loop.sh'", workerID, scriptB64))
```

### 4. Config File Not Written
**Problem:** Workers had empty config, causing shelley chat to fail silently
**Fix:** Write config in both install script and HTTP download paths using heredoc

### 5. Multi-Coordinator Conflicts
**Problem:** Multiple coordinators with same prefix interfere with each other's workers
**Fix:** Add random 3-char hex suffix to prefix at startup (e.g., `wk` → `wk-4f2`)

### 6. Task Group Creation Drops Prompts (SQLITE_BUSY)
**Problem:** When creating task groups, some tasks silently failed due to SQLite lock contention
**Fix:** 
- Added `PRAGMA busy_timeout = 5000` (5 second wait before failing)
- Added retry logic with exponential backoff (3 attempts)
- Group creation now fails entirely if any task can't be created (no silent drops)

### 7. Dashboard 404 Without Auth Headers
**Problem:** Accessing dashboard on localhost returned 404 instead of showing the UI
**Fix:** Allow local access (localhost/127.0.0.1) without exe.dev auth headers

### 8. Worker Hostname Not Stored
**Problem:** Worker API responses had null `tailscale_ip` field, making programmatic access harder
**Fix:** 
- Repurposed column to store exe.dev hostname
- Renamed JSON field from `tailscale_ip` to `hostname`
- Workers now return hostname like `wk-xxx.exe.xyz`

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│              Coordinator VM (e.g., panther-gecko)           │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  shelley coord -port 8081                           │   │
│  │  - Task queue (SQLite)                              │   │
│  │  - Worker lifecycle management                       │   │
│  │  - Cleanup every 5 min                              │   │
│  └─────────────────────────────────────────────────────┘   │
│                            │                                │
│                SSH via exe.dev proxy                        │
│           ssh exe.dev "ssh <worker> '<cmd>'"               │
└────────────────────────────┼────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                    Worker VMs (wk-xxx-*)                    │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  /tmp/worker-loop.sh (polls coordinator)            │   │
│  │  shelley serve -port 8000 (web UI for monitoring)   │   │
│  │  shelley chat -yes -prompt "..." (task execution)   │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Config: ~/.config/shelley/shelley.json                    │
│  {"llm_gateway": "http://169.254.169.254/gateway/llm",     │
│   "default_model": "claude-sonnet-4.5"}                     │
└─────────────────────────────────────────────────────────────┘
```

## exe.dev SSH Proxy Pattern

**Key Insight:** exe.dev VMs cannot SSH directly to each other. All SSH goes through the exe.dev proxy.

```bash
# This doesn't work for VM-to-VM:
ssh worker.exe.xyz command

# This works:
ssh exe.dev "ssh worker 'command'"
```

## Worker Lifecycle

1. **Spawn:** `ssh exe.dev new --name=<prefix>-<random> --no-email --json`
2. **Wait for SSH:** Poll `ssh exe.dev "ssh <worker> 'echo ready'"`
3. **Install:** Run install script (builds from source, ~2 min)
4. **Write Config:** Create `~/.config/shelley/shelley.json` with LLM gateway
5. **Start Serve:** `nohup shelley serve -port 8000`
6. **Start Loop:** `/tmp/worker-loop.sh` polls for tasks every 5 seconds
7. **Execute:** `shelley chat -yes -prompt "$PROMPT"` for each task
8. **Idle Timeout:** Self-shutdown after 30 minutes without tasks

## API Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/enqueue` | POST | Add task `{"prompt": "..."}` |
| `/api/tasks` | GET | List all tasks |
| `/api/task?id=X` | GET | Get task details |
| `/api/workers` | GET | List workers |
| `/api/scale?workers=N` | POST | Scale to N workers |
| `/api/drain` | POST | Shutdown all workers |
| `/api/stats` | GET | Queue statistics |

All endpoints require `X-Coordinator-Token` header or `?token=` param.

## Common Issues & Debugging

### "Tasks stuck in queued state"
- Check if workers are running: `/api/workers`
- Check worker loop: `ssh exe.dev "ssh <worker> 'ps aux | grep worker-loop'"`
- Check worker log: `ssh exe.dev "ssh <worker> 'cat /tmp/worker.log'"`

### "jq errors in worker log"
- Likely the script was written with quoting issues
- Fix: Use base64 encoding for script transfer (already fixed in current code)

### "Workers disappearing quickly"  
- Check `MAX_IDLE` value in worker-loop.sh (should be 360 for 30 min)

### "Install taking forever"
- Default install script builds from source (~2 min)
- Consider using `-install-script scp` to copy pre-built binary

## Git Log (Recent Fixes)
```
cfeeb39 fix: use base64 encoding for worker loop script
ba9d12b fix: replace all sshToWorker calls with direct exec.Command
611d64c fix: write config in install script path too
a1ab108 fix: increase worker idle timeout from 2 minutes to 30 minutes
```

## Testing Checklist

- [x] Coordinator starts and shows token
- [x] Workers spawn successfully
- [x] Workers install shelley binary
- [x] Workers have valid config file
- [x] Workers start HTTP file server (port 8000)
- [x] Workers start polling loop
- [x] Worker loop script has correct jq syntax
- [x] Tasks picked up and executed
- [x] Files created by tasks persist
- [x] Task status updates correctly
- [x] Workers stay alive for 30 minutes idle
- [x] Git integration (branches, commits)
- [x] Conversation sync to main DB
- [x] Multiple tasks run in parallel
- [x] Task groups work correctly
- [x] CLI commands (coord-cli, watch) work

## Recent Improvements (2026-01-16)

See [COORDINATOR_CHANGES_2026-01-16.md](COORDINATOR_CHANGES_2026-01-16.md) for detailed changelog.

### Key Improvements:

1. **Persistent Worker Prefix** - Coordinator remembers its worker prefix across restarts
2. **Startup Cleanup** - Cleans up orphaned workers/tasks immediately on start
3. **Reduced Failed Worker Retention** - 10 minutes instead of 1 hour
4. **Filter Failed Workers** - Dashboard hides failed workers by default
5. **Clear Failed Button** - One-click removal of failed worker records
6. **Worker Error Display** - Shows why workers failed in dashboard
7. **Conversation Sync Fix** - Fixed "argument list too long" error
8. **New CLI Commands** - `add-group`, `groups`, `group`, `clear-failed`
9. **HTTP File Server on Workers** - Workers serve files at port 8000 for easy transfer

### CLI Commands:

```bash
# Add task group
shelley coord-cli add-group "Landing Pages" "Create docker.html" \| "Create k8s.html"

# Monitor
shelley coord-cli stats
shelley coord-cli groups
shelley coord-cli workers
shelley watch  # Live auto-refreshing dashboard

# Cleanup
shelley coord-cli clear-failed
shelley coord-cli drain
```
