# Shelley Coordinator Project Status

**Last Updated:** 2026-01-16 03:50 UTC

## Overview

The Shelley coordinator system allows distributing tasks across multiple exe.dev worker VMs. This document tracks the current state and known issues.

## Current Status: ✅ FULLY WORKING

The coordinator can now:
- Create worker VMs on exe.dev
- Install shelley on workers via install script
- Start shelley serve on workers for web UI access
- Run worker polling loop that picks up and executes tasks
- Track worker status accurately
- Clean up missing/stale workers from the database
- Workers stay alive for 30 minutes of idle time (was 2 minutes - fixed!)

## Quick Start on panther-gecko

```bash
# SSH to panther-gecko
ssh panther-gecko.exe.xyz

# Start coordinator
cd ~/shelley-cli
nohup ./bin/shelley coord \
  -port 8081 \
  -db /tmp/coord.db \
  -max-workers 10 \
  -prefix wk \
  -host panther-gecko.exe.xyz \
  -install-script scp \
  > /tmp/coord.log 2>&1 &

# Get token from log
cat /tmp/coord.log

# Make port public (required for workers to download binary)
ssh exe.dev share port panther-gecko 8081
ssh exe.dev share set-public panther-gecko

# Scale workers
curl -X POST "http://localhost:8081/api/scale?workers=2&token=<TOKEN>"

# Submit a task
curl -X POST http://localhost:8081/api/enqueue \
  -H "Content-Type: application/json" \
  -H "X-Coordinator-Token: <TOKEN>" \
  -d '{"prompt": "Say hello"}'

# Check workers
curl -H "X-Coordinator-Token: <TOKEN>" http://localhost:8081/api/workers

# Check tasks
curl -H "X-Coordinator-Token: <TOKEN>" http://localhost:8081/api/tasks
```

## Recent Bug Fixes

### 1. Worker VMs Disappearing After 2 Minutes (FIXED)

**Problem:** Workers were self-shutting down after only 2 minutes of idle time.

**Root Cause:** The worker polling script had `MAX_IDLE=24` with 5-second sleep = 120 seconds.

**Fix:** Changed `MAX_IDLE` to 360 (30 minutes).

**Commit:** `a1ab108` - "fix: increase worker idle timeout from 2 minutes to 30 minutes"

### 2. Shelley Serve Not Starting on Workers (FIXED)

**Problem:** The `nohup` command was failing with "missing operand".

**Root Cause:** The command was being passed through `sshToWorker` with multiple args like `"bash", "-c", "nohup..."` which got incorrectly joined.

**Fix:** Pass the entire command as a single string: `sshToWorker(workerID, "nohup /usr/local/bin/shelley serve...")`

**Commits:** 
- `96ec4b9` - "fix: simplify shelley serve command to avoid quoting issues"
- `f9e5a73` - "fix: simplify worker-loop startup to avoid quoting issues"

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    panther-gecko.exe.xyz                     │
│  ┌─────────────────────────────────────────────────────┐    │
│  │  Coordinator (port 8081)                            │    │
│  │  - Manages worker lifecycle                         │    │
│  │  - Serves /api/shelley-bin for worker downloads     │    │
│  │  - Cleanup runs every 5 min (30s initial delay)     │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ ssh exe.dev ssh <worker>
                              │ https download of binary
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Worker VMs (wk-xxx)                      │
│  - Created via: ssh exe.dev new --name=wk-xxx               │
│  - Shelley installed via install script or HTTP download    │
│  - Runs shelley serve on port 8000 (web UI)                 │
│  - Runs worker-loop.sh that polls for tasks                 │
│  - Idle timeout: 30 minutes                                 │
└─────────────────────────────────────────────────────────────┘
```

## Key Technical Details

### exe.dev VM-to-VM SSH

exe.dev VMs cannot SSH directly to each other. Use the proxy pattern:
```bash
ssh exe.dev "ssh <vmname> '<command>'"
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

**Important:** When using shell operators (>, &, |), pass the entire command as a single string argument, not multiple args.

### Worker Lifecycle

1. **Spawn:** `ssh exe.dev new --name=<worker-id> --no-email --json`
2. **Wait for SSH:** Poll until `ssh exe.dev ssh <worker> echo ready` succeeds
3. **Install shelley:** Run install script or download from coordinator
4. **Start shelley serve:** Background on port 8000 for web UI
5. **Start worker loop:** Polls coordinator for tasks every 5 seconds
6. **Execute tasks:** Run `shelley chat -yes -prompt "<prompt>"`
7. **Idle timeout:** After 30 minutes without tasks, worker self-shuts down

## Cleanup Behavior

The coordinator runs cleanup every 5 minutes:

1. **Failed/deleted workers:** Removed from DB after 1 hour
2. **Stuck starting workers:** Deleted after 10 minutes
3. **Idle workers:** Deleted after 30 minutes (via coordinator cleanup)
4. **Worker self-shutdown:** After 30 minutes idle (via worker script)
5. **Orphaned VMs:** VMs on exe.dev not in coordinator DB are deleted
6. **Missing VMs:** Workers in DB whose VMs no longer exist are removed

## Git Commits (Recent)

```
f9e5a73 fix: simplify worker-loop startup to avoid quoting issues
96ec4b9 fix: simplify shelley serve command to avoid quoting issues
c275df7 fix: use 'which shelley' to find binary in worker startup
a1ab108 fix: increase worker idle timeout from 2 minutes to 30 minutes
bf71131 fix: cleanup removes workers from DB when VM no longer exists
```

## Testing Checklist

- [x] Coordinator starts and logs token
- [x] Port made public for worker downloads
- [x] `scale N` creates N workers
- [x] Workers install shelley successfully
- [x] Workers start shelley serve on port 8000
- [x] Workers start polling loop
- [x] Task submission creates queued task
- [x] Worker picks up and executes task
- [x] Task status updates to completed
- [x] Workers stay alive for 30 minutes idle
- [ ] Git integration for task branches
- [ ] Dashboard UI on port 8080
- [ ] Multiple workers processing tasks in parallel
