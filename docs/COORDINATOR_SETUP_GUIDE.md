# Shelley Coordinator Setup Guide

This guide explains how to set up a Shelley coordinator to run tasks in parallel across multiple exe.dev worker VMs.

## Overview

The Shelley coordinator system allows you to:
- Run multiple Shelley instances in parallel across worker VMs
- Submit tasks via API or through Shelley chat
- Monitor progress via web dashboard
- Automatically scale workers up and down

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Your Browser / Shelley Chat              │
│                              │                              │
│                    Submit tasks, monitor progress           │
└──────────────────────────────┼──────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────┐
│              Coordinator VM (e.g., my-coordinator)          │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │  shelley dashboard (port 8080) - Web UI             │  │
│   │       └── shelley coord (port 8081) - Task queue    │  │
│   └─────────────────────────────────────────────────────┘  │
│                              │                              │
│                    Spawns & manages workers                 │
│                    via exe.dev SSH proxy                    │
└──────────────────────────────┼──────────────────────────────┘
                               │
          ┌────────────────────┼────────────────────┐
          │                    │                    │
          ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   Worker VM 1   │  │   Worker VM 2   │  │   Worker VM 3   │
│                 │  │                 │  │                 │
│ worker-loop.sh  │  │ worker-loop.sh  │  │ worker-loop.sh  │
│ (polls tasks)   │  │ (polls tasks)   │  │ (polls tasks)   │
│                 │  │                 │  │                 │
│ shelley chat    │  │ shelley chat    │  │ shelley chat    │
│ (runs tasks)    │  │ (runs tasks)    │  │ (runs tasks)    │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

## Prerequisites

### 1. exe.dev Account

You need an exe.dev account. Sign up at https://exe.dev if you don't have one.

### 2. SSH Key Setup

exe.dev VMs use SSH keys for authentication. When you create a VM via the exe.dev web dashboard, it automatically:
1. Generates an SSH key pair (`~/.ssh/id_ed25519`) on the VM
2. Registers that public key with your exe.dev account

This allows the coordinator VM to create and manage worker VMs on your behalf.

**Verify SSH access works:**
```bash
ssh exe.dev whoami    # Should show your email
ssh exe.dev ls        # Should list your VMs
```

### 3. Shelley CLI Installed

The coordinator VM needs Shelley CLI installed. This happens automatically on exe.dev VMs, but you can verify with:
```bash
shelley version
```

## Step 1: Create the Coordinator VM

**Important:** Create the coordinator VM through the exe.dev web dashboard (https://exe.dev), NOT via `ssh exe.dev new`. This ensures proper SSH key setup.

1. Go to https://exe.dev and log in
2. Click "New VM"
3. Give it a memorable name (e.g., `my-coordinator`)
4. Wait for the VM to be created

Once created, SSH into it:
```bash
ssh my-coordinator.exe.xyz
```

## Step 2: Start the Coordinator

You have two options:

### Option A: Web Dashboard (Recommended)

The dashboard provides a web UI for managing the coordinator, viewing tasks, and scaling workers.

```bash
cd ~/shelley-cli

# Start the dashboard with auto-start coordinator
./bin/shelley dashboard \
  -port 8080 \
  -db coordinator.db \
  -max-workers 10 \
  -prefix wk \
  -auto-start \
  -install-script scp
```

**Flags explained:**
- `-port 8080`: Dashboard web UI port
- `-db coordinator.db`: SQLite database for task queue
- `-max-workers 10`: Maximum concurrent workers
- `-prefix wk`: Worker VM name prefix (e.g., `wk-abc-x123456`)
- `-auto-start`: Start coordinator automatically
- `-install-script scp`: Copy binary to workers (faster than building from source)

Access the dashboard at: `https://my-coordinator.exe.xyz:8080/`

### Option B: CLI Only

If you prefer command-line only:

```bash
cd ~/shelley-cli

./bin/shelley coord \
  -port 8081 \
  -db coordinator.db \
  -max-workers 10 \
  -prefix wk \
  -host my-coordinator.exe.xyz \
  -install-script scp
```

The coordinator will print an API token on startup - save this for API access.

## Step 3: Scale Up Workers

### Via Dashboard

1. Open `https://my-coordinator.exe.xyz:8080/`
2. Use the "Scale Workers" section
3. Enter the desired number of workers and click the + button

### Via API

```bash
TOKEN="your-api-token"
curl -X POST "https://my-coordinator.exe.xyz:8081/api/scale?workers=3" \
  -H "X-Coordinator-Token: $TOKEN"
```

### Via Shelley Chat

You can ask Shelley to manage the coordinator for you:

```
You: Scale the coordinator at my-coordinator.exe.xyz to 5 workers using token abc123

Shelley: [Uses curl to call the scale API]
```

**What happens when workers scale up:**
1. Coordinator creates new VMs via `ssh exe.dev new --name=wk-xxx-...`
2. Waits for VM to be ready (~3-5 seconds)
3. Installs Shelley binary on worker (~30 seconds with scp, ~2 min from source)
4. Starts worker loop script that polls for tasks
5. Worker status changes to "idle" and appears in dashboard

## Step 4: Submit Tasks

### Via Dashboard

1. Go to the "New Task" section in the dashboard
2. Enter a prompt describing what you want Shelley to do
3. Click "Enqueue"

### Via API

```bash
TOKEN="your-api-token"

# Submit a single task
curl -X POST "https://my-coordinator.exe.xyz:8081/api/enqueue" \
  -H "Content-Type: application/json" \
  -H "X-Coordinator-Token: $TOKEN" \
  -d '{"prompt": "Create a Python script that calculates fibonacci numbers"}'

# Submit multiple tasks for parallel execution
for prompt in \
  "Create a landing page for a coffee shop" \
  "Create a landing page for a bookstore" \
  "Create a landing page for a gym"; do
  curl -X POST "https://my-coordinator.exe.xyz:8081/api/enqueue" \
    -H "Content-Type: application/json" \
    -H "X-Coordinator-Token: $TOKEN" \
    -d "{\"prompt\": \"$prompt\"}"
done
```

### Via Shelley Chat

From any Shelley session, you can submit tasks:

```
You: Submit a task to the coordinator at my-coordinator.exe.xyz:8081 with token abc123:
     "Create a React component for a user profile card"

Shelley: [Uses curl to submit the task to the coordinator API]
```

## Step 5: Monitor Progress

### Dashboard

The dashboard at `https://my-coordinator.exe.xyz:8080/` shows:
- **Stats**: Queued, running, completed, failed tasks
- **Workers**: List of workers with status (starting, idle, busy)
- **Tasks**: Recent tasks with status and assigned worker
- **Logs**: Real-time coordinator logs

### API

```bash
# Get stats
curl -H "X-Coordinator-Token: $TOKEN" \
  "https://my-coordinator.exe.xyz:8081/api/stats"

# List tasks  
curl -H "X-Coordinator-Token: $TOKEN" \
  "https://my-coordinator.exe.xyz:8081/api/tasks"

# List workers
curl -H "X-Coordinator-Token: $TOKEN" \
  "https://my-coordinator.exe.xyz:8081/api/workers"
```

## Step 6: Drain and Cleanup

When you're done, drain the workers to clean up:

### Via Dashboard
Click the "Drain" button in the Workers section.

### Via API
```bash
curl -X POST "https://my-coordinator.exe.xyz:8081/api/drain" \
  -H "X-Coordinator-Token: $TOKEN"
```

This will:
- Delete idle workers immediately
- Let busy workers finish their current task, then delete them
- Stop assigning new tasks to workers

## Running as a Persistent Service

To keep the coordinator running after you disconnect:

### Option 1: nohup (Simple)
```bash
cd ~/shelley-cli
nohup ./bin/shelley dashboard -port 8080 -db coordinator.db \
  -max-workers 10 -prefix wk -auto-start -install-script scp \
  > /tmp/dashboard.log 2>&1 &
```

### Option 2: systemd (Recommended for Production)

Create a service file:
```bash
sudo tee /etc/systemd/system/shelley-coordinator.service << 'SERVICEEOF'
[Unit]
Description=Shelley Coordinator Dashboard
After=network.target

[Service]
Type=simple
User=exedev
WorkingDirectory=/home/exedev/shelley-cli
ExecStart=/home/exedev/shelley-cli/bin/shelley dashboard -port 8080 -db coordinator.db -max-workers 10 -prefix wk -auto-start -install-script scp
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICEEOF

sudo systemctl daemon-reload
sudo systemctl enable shelley-coordinator
sudo systemctl start shelley-coordinator
```

View logs with:
```bash
journalctl -u shelley-coordinator -f
```

## API Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/stats` | GET | Queue and worker statistics |
| `/api/tasks` | GET | List all tasks |
| `/api/task?id=X` | GET | Get specific task details |
| `/api/enqueue` | POST | Add task `{"prompt": "..."}` |
| `/api/workers` | GET | List all workers |
| `/api/scale?workers=N` | POST | Scale to N workers |
| `/api/drain` | POST | Gracefully shutdown all workers |

All API endpoints require the `X-Coordinator-Token` header or `?token=` query parameter.

## Troubleshooting

### Workers not appearing in dashboard
- Check coordinator logs: `tail -f /tmp/dashboard.log`
- Verify SSH access works: `ssh exe.dev ls`
- Check if VM limit reached: exe.dev accounts have a concurrent VM limit

### Tasks stuck in "queued" status
- Ensure workers exist and are in "idle" status
- Check worker logs: `ssh exe.dev "ssh wk-xxx 'cat /tmp/worker.log'"`
- Verify the worker loop is running: `ssh exe.dev "ssh wk-xxx 'ps aux | grep worker-loop'"`

### Workers disappearing from dashboard
- Ensure you're running the latest version (this bug has been fixed)
- Update: `cd ~/shelley-cli && git pull && go build -o bin/shelley ./cmd/shelley`

### Old coordinator process still running after restart
- Fixed in latest version - dashboard now properly kills coordinator on shutdown
- Manual cleanup: `pkill -f "shelley coord"`

### Worker install takes too long
- Use `-install-script scp` flag to copy pre-built binary (~30 seconds)
- Default install builds from source (~2 minutes)

## Example: Parallel Web Page Generation

Here's a complete example that generates 5 landing pages in parallel:

```bash
# 1. SSH to your coordinator VM
ssh my-coordinator.exe.xyz

# 2. Start coordinator (if not already running)
cd ~/shelley-cli
nohup ./bin/shelley dashboard -port 8080 -db coordinator.db \
  -max-workers 5 -prefix wk -auto-start -install-script scp \
  > /tmp/dashboard.log 2>&1 &

# 3. Get token
sleep 3
TOKEN=$(grep "API Token" /tmp/dashboard.log | awk '{print $NF}')
echo "Token: $TOKEN"

# 4. Scale to 5 workers
curl -X POST "http://localhost:8081/api/scale?workers=5" \
  -H "X-Coordinator-Token: $TOKEN"

# 5. Wait for workers to be ready (~1-2 minutes)
echo "Waiting for workers to install..."
while true; do
  IDLE=$(curl -s -H "X-Coordinator-Token: $TOKEN" http://localhost:8081/api/stats | jq '.workers.idle')
  echo "Idle workers: $IDLE"
  [ "$IDLE" -ge 5 ] && break
  sleep 10
done

# 6. Submit 5 tasks
for game in "DOOM" "Quake" "Duke-Nukem-3D" "Command-and-Conquer" "Warcraft-2"; do
  curl -X POST "http://localhost:8081/api/enqueue" \
    -H "Content-Type: application/json" \
    -H "X-Coordinator-Token: $TOKEN" \
    -d "{\"prompt\": \"Create a nostalgic 90s-style landing page for $game with retro styling, visitor counter, and guestbook link. Save to /home/exedev/$game/index.html\"}"
  echo ""
done

# 7. Monitor progress
watch -n 5 "curl -s -H 'X-Coordinator-Token: $TOKEN' http://localhost:8081/api/stats | jq"
```

## Tips

1. **Use `-install-script scp`** - Much faster than building from source on each worker
2. **Start with 2-3 workers** - Scale up once you verify things work
3. **Monitor the dashboard** - It shows real-time logs and task status
4. **Use meaningful task prompts** - Be specific about what you want created and where to save files
5. **Create coordinator via web dashboard** - Ensures SSH keys are properly set up
