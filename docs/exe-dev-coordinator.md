# Running Shelley Coordinator on exe.dev

This document describes the key learnings and solutions discovered when running
the Shelley coordinator system on exe.dev VMs.

## Key Discovery: VM-to-VM SSH Limitations

**exe.dev VMs cannot SSH directly to each other.** All SSH traffic is routed
through the exe.dev proxy, which has specific behaviors:

1. Direct SSH to `vmname.exe.xyz` goes through the proxy
2. The proxy parses all command-line flags, causing issues with flags like `-p` (mkdir)
3. VM-to-VM network connections on internal IPs (10.42.x.x) are blocked

### Solution: Use exe.dev SSH Proxy Commands

Instead of:
```bash
ssh -o StrictHostKeyChecking=no worker.exe.xyz mkdir -p .local/bin
```

Use:
```bash
ssh exe.dev "ssh worker 'mkdir -p .local/bin'"
```

The key points:
- Use `ssh exe.dev ssh <vmname>` pattern (no `.exe.xyz` suffix)
- Wrap the entire remote command in quotes so exe.dev treats it as one argument
- This prevents exe.dev from parsing flags meant for the remote command

## Binary Transfer

**SCP doesn't work through the exe.dev proxy.** Solutions:

1. **HTTP Download (Recommended)**: Serve the binary from the coordinator via HTTPS
   - Make coordinator's port public: `ssh exe.dev share set-public <coordinator-vm>`
   - Workers download via curl from `https://<coordinator>.exe.xyz:<port>/api/shelley-bin`

2. **Install Script**: Workers can run an install script that clones and builds
   (slower, but doesn't require HTTP setup)

## Coordinator Setup on exe.dev

### Prerequisites

1. Make coordinator's HTTP port accessible:
   ```bash
   ssh exe.dev share port <your-vm> 8081
   ssh exe.dev share set-public <your-vm>
   ```

2. Start coordinator:
   ```bash
   nohup ~/shelley-cli/bin/shelley coord \
     -port 8081 \
     -db coordinator.db \
     -max-workers 10 \
     -prefix wk \
     -host <your-vm>.exe.xyz \
     -install-script scp \
     > /tmp/coord.log 2>&1 &
   ```

3. Scale workers:
   ```bash
   shelley coord-cli scale 3
   ```

### Using coord-cli

```bash
# Scale to N workers
shelley coord-cli scale 5

# Check worker status  
shelley coord-cli workers

# View stats
shelley coord-cli stats

# Drain all workers
shelley coord-cli drain
```

## Implementation Details

### sshToWorker Function

The coordinator uses a helper function to properly route SSH commands:

```go
func sshToWorker(workerID string, args ...string) *exec.Cmd {
    // Join all args and wrap in quotes to prevent exe.dev from parsing flags
    remoteCmd := strings.Join(args, " ")
    remoteCmd = strings.ReplaceAll(remoteCmd, "'", "'\"'\"'")
    sshCmd := fmt.Sprintf(`ssh %s '%s'`, workerID, remoteCmd)
    return exec.Command("ssh", "exe.dev", sshCmd)
}
```

### Worker Lifecycle

1. **Spawn**: `ssh exe.dev new --name=<worker-id> --no-email --json`
2. **Wait for SSH**: Poll until `ssh exe.dev ssh <worker> echo ready` succeeds
3. **Install shelley**: Create directories, download binary, configure
4. **Start shelley serve**: Background process on port 8000
5. **Run worker loop**: Poll coordinator for tasks, execute, report results
6. **Cleanup**: Idle workers are cleaned up after 30 minutes

## Troubleshooting

### "flag parsing error: flag provided but not defined"

The exe.dev SSH proxy is parsing your flags. Solution: wrap the command in quotes.

### Workers created but immediately fail

Check if coordinator's HTTPS endpoint is accessible:
```bash
curl https://<coordinator>.exe.xyz:<port>/api/stats
```

If you get a redirect to login, run:
```bash
ssh exe.dev share set-public <coordinator-vm>
```

### Binary download fails

Verify the shelley-bin endpoint works:
```bash
curl -o /tmp/test https://<coordinator>.exe.xyz:<port>/api/shelley-bin?token=<token>
file /tmp/test  # Should show: ELF 64-bit LSB executable
```

### shelley status shows coordinator as stopped

The status command now checks both ports 8080 and 8081. If running manually
(not via systemd), it uses `lsof` to detect the process.

## Timeline of Fixes

1. **coord-cli scale command**: Fixed URL parameter joining (`?` vs `&` for token)
2. **VM-to-VM SSH**: Discovered proxy limitations, implemented sshToWorker helper
3. **Flag parsing**: Wrapped remote commands in quotes
4. **Binary transfer**: Switched from SCP to HTTPS download
5. **HTTP access**: Made coordinator port public for worker downloads
6. **Command quoting**: Simplified curl command to avoid nested quoting issues
7. **Status command**: Added port 8081 and lsof-based detection
