# Shelley Coordinator: How Parallel Execution Works

This document explains the complete flow of using Shelley to execute tasks in parallel across multiple exe.dev VMs.

---

## Step 1: Create a VM on exe.dev

Go to the exe.dev website and create a new VM. Let's call it `coordinator`.

This VM automatically gets:
- An SSH key pair at `~/.ssh/id_ed25519`
- That public key registered with your exe.dev account

---

## Step 2: SSH into the VM

**Important:** You cannot SSH directly from your local machine to a new VM. You must first connect to the exe.dev shell, then SSH from there to your VM:

```bash
# From your local machine:
ssh exe.dev

# From the exe.dev shell:
ssh coordinator
```

Alternatively, you can do this in one command:
```bash
ssh exe.dev ssh coordinator
```

> **Why?** Your local SSH key is registered with exe.dev, but not with the VM itself. The exe.dev shell acts as a jump host that can access all your VMs. If you want to SSH directly to `coordinator.exe.xyz` from your local machine, you would need to manually copy your local public key to the VM's `~/.ssh/authorized_keys`.

---

## Step 3: Install shelley-cli

```bash
curl -fsSL https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh | bash
source ~/.bashrc
```

This installs shelley-cli to `~/shelley-cli/` and adds it to your PATH.

---

## Step 4: Register the VM's SSH key with exe.dev

The new VM has its own SSH key pair, but it's not yet registered with your exe.dev account. To register it:

```bash
ssh exe.dev
```

The first time you run this, exe.dev will see an unrecognized key and send you an email with a magic link. Click the link to register the VM's SSH key with your exe.dev account.

After clicking the link, verify it works:

```bash
ssh exe.dev whoami
```

Should show your email and SSH keys. This confirms the coordinator VM can spawn other VMs.

---

## Step 5: Start Shelley

```bash
shelley chat
```

---

## Step 6: Give Shelley a parallel task

You type something like:

> "Create 5 creative landing pages for 90s video games in parallel on 5 worker VMs"

---

## Step 7: Shelley (the agent) recognizes this needs parallel execution

Shelley reads AGENTS.md, sees the coordinator documentation, and decides to:

1. **Start the coordinator/dashboard:**
   ```bash
   shelley dashboard -auto-start &
   ```

2. **Scale to 5 workers via API:**
   ```bash
   curl -X POST "http://localhost:8080/api/scale?workers=5" -H "X-Coordinator-Token: <token>"
   ```

---

## Step 8: Coordinator spawns 5 worker VMs

For each worker, the coordinator:

1. **Creates VM on exe.dev:**
   ```bash
   ssh exe.dev new --name=wk-1 --no-email --json
   ```

2. **Waits for VM to be ready** (SSH accessible)

3. **Installs shelley-cli via HTTP download (default):**
   ```bash
   # Worker downloads binary from coordinator
   ssh exe.dev ssh wk-1 "curl -fsSL https://<coordinator>.exe.xyz:8080/api/shelley-bin -o ~/.local/bin/shelley && chmod +x ~/.local/bin/shelley"
   ```
   
   > **Note:** The coordinator SSH's through exe.dev to reach the worker VM, since the coordinator's SSH key is registered with exe.dev (not directly with the worker).
   
   The install method is determined by the `-install-script` flag:
   - `http` (default): Workers download binary from coordinator's `/api/shelley-bin` endpoint
   - `scp`: Copy binary via SSH
   - URL: Run a custom install script (e.g., `https://raw.githubusercontent.com/user/repo/main/install.sh`)

4. **Configures the worker** (shelley.json, git credentials if set)

5. **Marks worker as ready** in the database

---

## Step 9: Coordinator dispatches tasks

Once workers are ready, the coordinator:

1. Creates 5 tasks (one per landing page)
2. Assigns each task to a worker
3. Each worker runs `shelley chat -prompt "<task prompt>" -yes`

---

## Step 10: Workers execute tasks

Each worker VM:
1. Runs Shelley with the assigned prompt
2. Creates the landing page
3. (If git configured) Commits to a branch and pushes
4. Reports completion back to coordinator

---

## Step 11: Monitor progress

You can view progress at `https://coordinator.exe.xyz:8080/`

The dashboard shows:
- Worker status (spawning, ready, busy, idle)
- Task status (pending, running, completed, failed)
- Results/output from each task

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                     COORDINATOR VM                          │
│  ┌─────────────┐    ┌─────────────────────────────────┐    │
│  │ shelley     │───▶│ coordinator/dashboard           │    │
│  │ chat        │    │ (port 8080)                     │    │
│  └─────────────┘    └──────────────┬──────────────────┘    │
│                                    │                        │
│                          ssh exe.dev new                    │
│                          ssh exe.dev ssh <worker> "..."     │
└────────────────────────────────────┼────────────────────────┘
                                     │
          ┌──────────────────────────┼──────────────────────────┐
          │                          │                          │
          ▼                          ▼                          ▼
   ┌─────────────┐           ┌─────────────┐           ┌─────────────┐
   │   wk-1      │           │   wk-2      │           │   wk-3      │
   │  (worker)   │           │  (worker)   │           │  (worker)   │
   │             │           │             │           │             │
   │ Task:       │           │ Task:       │           │ Task:       │
   │ "Sonic      │           │ "Mario      │           │ "Zelda      │
   │  landing    │           │  landing    │           │  landing    │
   │  page"      │           │  page"      │           │  page"      │
   └─────────────┘           └─────────────┘           └─────────────┘
```

---

## Configuration Options

### Install Method

The coordinator uses HTTP download by default to install shelley-cli on worker VMs. You can customize this:

```bash
# Default: HTTP download from coordinator (recommended)
shelley dashboard -auto-start

# Use SCP instead (copies binary directly via SSH)
shelley dashboard -install-script scp -auto-start

# Use custom install script (downloads and builds from source)
shelley dashboard -install-script "https://example.com/my-install.sh" -auto-start
```

### Git Integration

To have workers commit their changes to git:

```bash
export GITHUB_TOKEN=ghp_xxx
shelley dashboard -git-token $GITHUB_TOKEN -auto-start
```

Workers will:
- Create a branch named `task-{id}`
- Commit changes with a descriptive message
- Push to the repository

---

## Troubleshooting

### "Permission denied" when spawning workers

Your coordinator VM's SSH key isn't registered with exe.dev. Run:
```bash
ssh exe.dev whoami
```

If this fails, see the SSH Key Setup section in CLI_REFERENCE.md.

### Workers stuck in "spawning" state

Check coordinator logs for errors. Common issues:
- exe.dev rate limiting (too many VMs created quickly)
- Network timeouts
- Install script failures

### Tasks not being assigned

Ensure workers reach "ready" state. Check:
- Worker VM is accessible via SSH
- shelley-cli installed successfully
- shelley.json configured properly

---

## CLI Tools for Monitoring

Instead of using the web dashboard, you can monitor and manage from the command line:

### Quick Status Check

```bash
shelley status
```

Shows:
- Service status (coordinator, igor)
- Coordinator stats (workers, tasks)
- Current API token
- URLs for web access

### Live Dashboard

```bash
shelley watch
```

A terminal-based dashboard that auto-refreshes every 2 seconds showing:
- Worker status (idle/busy/spawning)
- Task queue (queued/running/completed/failed)
- Real-time updates

### Coordinator Management

```bash
# View stats
shelley coord-cli stats

# Scale workers
shelley coord-cli scale 5

# List workers/tasks
shelley coord-cli workers
shelley coord-cli tasks

# Reset everything
shelley coord-cli clear-all
```
