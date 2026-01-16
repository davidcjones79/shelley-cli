# Shelley Coordinator Project Status

**Last Updated:** 2026-01-16 04:40 UTC

## Overview

The Shelley coordinator system distributes tasks across multiple exe.dev worker VMs for parallel execution. Workers run shelley chat autonomously and report results back to the coordinator.

## Current Status: ✅ FULLY WORKING

Successfully tested with:
- 5 parallel tasks (90s video game landing pages)
- 4-5 worker VMs processing simultaneously
- Full end-to-end task execution with file creation

## Quick Start

```bash
# On your coordinator VM (e.g., panther-gecko)
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
grep "API TOKEN" -A1 /tmp/coord.log | tail -1

# Make port public (REQUIRED for workers)
ssh exe.dev share port $(hostname) 8081
ssh exe.dev share set-public $(hostname)

# Scale workers
curl -X POST "http://localhost:8081/api/scale?workers=5&token=<TOKEN>"

# Submit tasks
curl -X POST http://localhost:8081/api/enqueue \
  -H "Content-Type: application/json" \
  -H "X-Coordinator-Token: <TOKEN>" \
  -d '{"prompt": "Create a file called test.txt with Hello World"}'

# Monitor
curl -H "X-Coordinator-Token: <TOKEN>" http://localhost:8081/api/tasks
curl -H "X-Coordinator-Token: <TOKEN>" http://localhost:8081/api/workers
```

## Key Fixes Applied (This Session)

### 1. Worker Idle Timeout (CRITICAL)
**Problem:** Workers self-shutdown after 2 minutes idle
**Cause:** `MAX_IDLE=24` with 5s polling = 120 seconds
**Fix:** Changed to `MAX_IDLE=360` (30 minutes)
**Commit:** `a1ab108`

### 2. Shell Quoting Through SSH Proxy
**Problem:** Commands with `bash -c` failed through exe.dev SSH proxy
**Cause:** `sshToWorker()` joins args and wraps in quotes, breaking nested quoting
**Fix:** Pass shell commands as single strings, not `bash -c` with separate args

```go
// BAD - doesn't work
sshToWorker(workerID, "bash", "-c", "nohup /usr/local/bin/shelley serve...")

// GOOD - works
sshToWorker(workerID, "nohup /usr/local/bin/shelley serve -port 8000 > /tmp/log 2>&1 &")
```
**Commits:** `96ec4b9`, `f9e5a73`

### 3. Config File Not Written (CRITICAL)
**Problem:** Workers had empty config, causing shelley chat to fail silently
**Cause:** Config writing was only in HTTP download path, not install script path
**Fix:** Write config in both paths using heredoc

```go
configCmd := exec.Command("ssh", "exe.dev", 
    fmt.Sprintf("ssh %s 'cat > .config/shelley/shelley.json << EOF\n%s\nEOF'", 
    workerID, configJSON))
```
**Commits:** `a542b10`, `9e7ddf7`, `611d64c`

### 4. Multi-Coordinator Conflicts
**Problem:** Multiple coordinators with same prefix interfere with each other's workers
**Fix:** Add random 3-char hex suffix to prefix at startup (e.g., `wk` → `wk-78b`)
**Commit:** `2223b0d`

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│              Coordinator VM (e.g., panther-gecko)           │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  shelley coord -port 8081                           │   │
│  │  - Task queue (SQLite)                              │   │
│  │  - Worker lifecycle management                       │   │
│  │  - /api/shelley-bin serves binary to workers        │   │
│  │  - Cleanup every 5 min                              │   │
│  └─────────────────────────────────────────────────────┘   │
│                            │                                │
│              Port must be PUBLIC for workers!               │
└────────────────────────────┼────────────────────────────────┘
                             │
          ssh exe.dev "ssh <worker> '<cmd>'"
                             │
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                    Worker VMs (wk-xxx-*)                    │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  /tmp/worker-loop.sh (polls coordinator)            │   │
│  │  shelley serve -port 8000 (web UI)                  │   │
│  │  shelley chat -yes -prompt "..." (task execution)   │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Config: ~/.config/shelley/shelley.json                    │
│  {"llm_gateway": "http://169.254.169.254/gateway/llm",     │
│   "default_model": "claude-sonnet-4.5"}                     │
└─────────────────────────────────────────────────────────────┘
```

## exe.dev SSH Proxy Limitations

**Key Insight:** exe.dev VMs cannot SSH directly to each other. All SSH goes through the exe.dev proxy.

### The Pattern
```bash
# Direct SSH (doesn't work for VM-to-VM)
ssh worker.exe.xyz command

# Through proxy (works!)
ssh exe.dev "ssh worker 'command'"
```

### sshToWorker Helper
```go
func sshToWorker(workerID string, args ...string) *exec.Cmd {
    remoteCmd := strings.Join(args, " ")
    remoteCmd = strings.ReplaceAll(remoteCmd, "'", "'\"'\"'")
    sshCmd := fmt.Sprintf(`ssh %s '%s'`, workerID, remoteCmd)
    return exec.Command("ssh", "exe.dev", sshCmd)
}
```

### Quoting Rules
1. **Single string for shell commands:** Pass `"cmd1 && cmd2 > file"` as one arg
2. **No bash -c wrapper:** The outer quotes provide shell context
3. **Heredocs work:** Use `<< EOF ... EOF` for multi-line content
4. **Redirects work:** `>`, `>>`, `2>&1` work inside the quoted string

## Worker Lifecycle

1. **Spawn:** `ssh exe.dev new --name=<prefix>-x<random> --no-email --json`
2. **Wait for SSH:** Poll `ssh exe.dev ssh <worker> echo ready`
3. **Install:** Run install script OR download binary from coordinator
4. **Write Config:** Create `~/.config/shelley/shelley.json` with LLM gateway
5. **Start Serve:** `nohup /usr/local/bin/shelley serve -port 8000`
6. **Start Loop:** `/tmp/worker-loop.sh` polls for tasks every 5 seconds
7. **Execute:** `shelley chat -yes -prompt "$PROMPT"` for each task
8. **Idle Timeout:** Self-shutdown after 30 minutes without tasks

## Common Issues & Solutions

### "Tasks completing instantly with no output"
**Cause:** Empty or missing `~/.config/shelley/shelley.json`
**Fix:** Ensure config is written in coordinator setup code

### "Workers disappearing after 2 minutes"
**Cause:** Old `MAX_IDLE=24` value (2 minute timeout)
**Fix:** Update to `MAX_IDLE=360` (30 minutes)

### "nohup: missing operand" in worker logs
**Cause:** Command passed with `bash -c` through sshToWorker
**Fix:** Pass command as single string without bash -c wrapper

### "VM limit reached" errors
**Cause:** exe.dev account has concurrent VM limit (often 15)
**Fix:** Delete unused VMs or reduce worker count

### "Port unbound" when accessing worker web UI
**Cause:** Worker's shelley serve not running
**Check:** `ssh exe.dev "ssh <worker> 'ps aux | grep shelley'"`

## API Reference

### Coordinator Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/enqueue` | POST | Add task `{"prompt": "..."}` |
| `/api/tasks` | GET | List all tasks |
| `/api/task?id=X` | GET | Get task details |
| `/api/workers` | GET | List workers |
| `/api/scale?workers=N` | POST | Scale to N workers |
| `/api/drain` | POST | Shutdown all workers |
| `/api/stats` | GET | Queue statistics |
| `/api/shelley-bin` | GET | Download shelley binary |

All endpoints require `X-Coordinator-Token` header or `?token=` param.

## Files Changed

### coordinator/coordinator.go
- `MAX_IDLE=360` (was 24) - 30 minute idle timeout
- Config written in both install script and HTTP download paths
- Shell commands passed as single strings to sshToWorker
- Random suffix added to worker prefix

### cmd/shelley/main.go
- Token auto-detection from `/tmp/coord.log`
- Multi-port checking (8080, 8081)

## Git Log (Recent)
```
611d64c fix: write config in install script path too
9e7ddf7 fix: use heredoc for writing config to avoid shell quoting issues
a542b10 fix: remove bash -c wrapper from config write command
2223b0d feat: add random suffix to worker prefix for multi-coordinator support
f9e5a73 fix: simplify worker-loop startup to avoid quoting issues
96ec4b9 fix: simplify shelley serve command to avoid quoting issues
a1ab108 fix: increase worker idle timeout from 2 minutes to 30 minutes
```

## Testing Checklist

- [x] Coordinator starts and shows token
- [x] Port made public for worker binary downloads
- [x] Workers spawn successfully
- [x] Workers install shelley binary
- [x] Workers have valid config file
- [x] Workers start shelley serve (port 8000)
- [x] Workers start polling loop
- [x] Tasks picked up and executed
- [x] Files created by tasks persist
- [x] Workers stay alive for 30 minutes idle
- [x] Multiple tasks run in parallel
- [x] Task status updates correctly
- [ ] Git integration (branches, commits)
- [ ] Conversation sync to main DB
