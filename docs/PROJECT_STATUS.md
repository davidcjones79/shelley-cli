# Shelley Coordinator Project Status

**Last Updated:** 2026-01-16 03:30 UTC

## Overview

The Shelley coordinator system allows distributing tasks across multiple exe.dev worker VMs. This document tracks the current state and known issues.

## Current Status: ✅ WORKING

The coordinator can now:
- Create worker VMs on exe.dev
- Install shelley on workers via HTTP download
- Track worker status accurately
- Clean up missing/stale workers from the database

## Key Commands

```bash
# Start coordinator on panther-gecko
nohup ~/shelley-cli/bin/shelley coord \
  -port 8081 \
  -db coordinator.db \
  -max-workers 10 \
  -prefix wk \
  -host panther-gecko.exe.xyz \
  -install-script scp \
  > /tmp/coord.log 2>&1 &

# Check status
shelley status

# Scale workers
shelley coord-cli scale 3

# View workers (verifies against exe.dev)
shelley coord-cli workers

# View stats
shelley coord-cli stats

# Drain all workers
shelley coord-cli drain
```

## Prerequisites for Coordinator

1. **Make coordinator port public** (required for workers to download shelley binary):
   ```bash
   ssh exe.dev share port panther-gecko 8081
   ssh exe.dev share set-public panther-gecko
   ```

2. **Token is auto-generated** and logged to `/tmp/coord.log`

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
│  - Shelley binary downloaded from coordinator               │
│  - Runs shelley serve on port 8000                          │
│  - Polls coordinator for tasks                              │
└─────────────────────────────────────────────────────────────┘
```

## Key Technical Discoveries

### 1. exe.dev VM-to-VM SSH Limitation

**Problem:** exe.dev VMs cannot SSH directly to each other. All SSH goes through the exe.dev proxy.

**Solution:** Use `ssh exe.dev ssh <vmname> '<command>'` pattern:
```go
func sshToWorker(workerID string, args ...string) *exec.Cmd {
    remoteCmd := strings.Join(args, " ")
    remoteCmd = strings.ReplaceAll(remoteCmd, "'", "'\"'\"'")
    sshCmd := fmt.Sprintf(`ssh %s '%s'`, workerID, remoteCmd)
    return exec.Command("ssh", "exe.dev", sshCmd)
}
```

### 2. exe.dev Flag Parsing

**Problem:** The exe.dev SSH proxy parses ALL flags, so `mkdir -p` fails because `-p` is interpreted as an exe.dev flag.

**Solution:** Wrap the entire remote command in single quotes so it's passed as one argument.

### 3. Binary Transfer

**Problem:** 
- SCP doesn't work through exe.dev proxy
- Binary too large for command-line (50MB)

**Solution:** HTTP download from coordinator's `/api/shelley-bin` endpoint.

### 4. HTTP Access for Workers

**Problem:** Workers need to download shelley binary but coordinator is private by default.

**Solution:** Make coordinator public:
```bash
ssh exe.dev share set-public panther-gecko
```

## Cleanup Behavior

The coordinator runs cleanup every 5 minutes (30 second initial delay):

1. **Failed/deleted workers:** Removed from DB after 1 hour
2. **Stuck starting workers:** Deleted after 10 minutes 
3. **Idle workers:** Deleted after 30 minutes of inactivity
4. **Orphaned VMs:** VMs on exe.dev not in coordinator DB are deleted
5. **Missing VMs:** Workers in DB whose VMs no longer exist are removed from DB

## Files Modified

### coordinator/coordinator.go
- Added `sshToWorker()` helper for exe.dev SSH proxy
- Changed binary transfer from SCP to HTTP download
- Added `cleanupMissingVMs()` to remove stale DB entries
- Fixed cleanup to collect workers before deleting (DB iteration issue)

### cmd/shelley/coord_cli.go
- Default port changed from 8080 to 8081
- `workers` command now verifies VMs exist on exe.dev
- Shows 👻 icon for missing workers
- Improved token auto-detection from `/tmp/coord.log`

### cmd/shelley/status.go
- Checks both port 8080 and 8081 for coordinator
- Uses `lsof` to detect non-systemd processes
- Shows correct port for running coordinator
- Added `isHexString()` helper for token parsing

### cmd/shelley/main.go  
- Added `/api/shelley-bin` endpoint registration

### docs/exe-dev-coordinator.md
- Comprehensive documentation of exe.dev limitations and solutions

## Known Issues / TODO

1. **⚠️ CRITICAL: Worker VMs disappearing unexpectedly** - Worker VMs are being deleted even when coordinator hasn't cleaned them up. Possible causes:
   - exe.dev has a VM limit or auto-cleanup
   - Some other process is deleting them
   - Need to investigate exe.dev's VM lifecycle policies

2. **Worker idle timeout (30 min) may be too aggressive** - Workers get deleted even when coordinator is working correctly

3. **No task execution tested yet** - We've verified worker creation but haven't tested actual task distribution

4. **Dashboard (port 8080) not tested** - We've been using coord command directly on 8081

## Git Commits (Recent)

```
bf71131 fix: cleanup removes workers from DB when VM no longer exists
126c287 fix: coord-cli workers verifies VMs exist on exe.dev
262a693 fix: status shows correct port for running coordinator
e59fb09 fix: improved token parsing from coord.log
8ad0d01 fix: coord-cli defaults to port 8081, improved token detection
adbfb01 fix: status command detects coordinator on port 8081
093ad77 fix: simplify download command to avoid quoting issues
ed268e4 fix: download shelley binary via HTTP instead of inline transfer
7ac62a8 fix: pass command to exe.dev as single quoted argument
1d6e67a fix: quote remote commands for exe.dev ssh proxy
92e84a5 fix: use ssh exe.dev ssh <vmname> for VM-to-VM communication
```

## Testing Checklist

- [x] `shelley status` shows coordinator on correct port
- [x] `shelley coord-cli scale N` creates N workers
- [x] `shelley coord-cli workers` shows accurate VM status
- [x] Workers download shelley binary successfully
- [x] Missing VMs cleaned up from database
- [x] Orphaned VMs cleaned up from exe.dev
- [ ] Task submission and execution
- [ ] Worker task polling loop
- [ ] Git integration for task branches
- [ ] Dashboard UI on port 8080

## How to Resume

1. SSH to panther-gecko: `ssh panther-gecko.exe.xyz`
2. Check coordinator status: `shelley status`
3. If not running, start it:
   ```bash
   nohup ~/shelley-cli/bin/shelley coord -port 8081 -db coordinator.db \
     -max-workers 10 -prefix wk -host panther-gecko.exe.xyz \
     -install-script scp > /tmp/coord.log 2>&1 &
   ```
4. Ensure port is public: `ssh exe.dev share set-public panther-gecko`
5. Scale workers: `shelley coord-cli scale 2`
6. Check workers: `shelley coord-cli workers`
