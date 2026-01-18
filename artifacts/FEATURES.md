# Shelley CLI - Feature Overview

Shelley is a multi-modal, multi-model coding agent with both CLI and web interfaces, designed for [exe.dev](https://exe.dev/) VMs.

## Core Interfaces

### 1. Interactive CLI (`shelley chat`)
- **Streaming responses** - Token-by-token text generation
- **Tool execution** - Run bash commands, edit files, take screenshots
- **Tab completion** - Complete file paths and slash commands
- **Prompt history** - Up/Down arrows cycle through previous inputs
- **Multi-line input** - `Ctrl+J` for newlines
- **Mouse scrolling** - Toggle with `/mouse`
- **Themes** - Dark, light, and monochrome (`/theme`)

### 2. Web UI (`shelley serve`)
- React/TypeScript frontend with mobile-first design
- Real-time updates via SSE (Server-Sent Events)
- Visual tool output rendering (diffs, bash output, screenshots)
- Conversation management and search

## LLM & Model Support

### Multi-Provider Support
| Provider | Models |
|----------|--------|
| Anthropic | claude-opus-4.5, claude-sonnet-4.5, claude-haiku-4.5 |
| OpenAI | gpt-5, gpt-5-nano, gpt-5.1-codex |
| Fireworks | qwen3-coder-fireworks, glm-4p6-fireworks |

### Quick Model Switching
- `/models` - List available models
- `/model <name>` - Switch to specific model
- `/fast` - Switch to Haiku (cheap & fast)
- `/smart` - Switch to Sonnet (balanced)
- `/think` - Switch to Opus (complex reasoning)

### exe.dev LLM Gateway
- Built-in gateway integration - no API keys needed on exe.dev VMs
- Configure via `~/.config/shelley/shelley.json`

## Tool System

### Available Tools
| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `patch` | Precise file editing (replace, append, prepend, overwrite) |
| `keyword_search` | Semantic code search |
| `change_dir` | Persistent directory navigation |
| `think` | Internal reasoning/planning |
| `browser_navigate` | Navigate to URLs |
| `browser_screenshot` | Capture screenshots |
| `browser_eval` | Execute JavaScript |
| `browser_resize` | Resize browser viewport |
| `read_image` | Analyze images with vision models |

### Tool Confirmation
- Interactive approval: `[y]es / [n]o / [a]lways`
- `-yes` flag for auto-approval (unattended execution)

## Conversation Management

### SQLite Persistence
- All conversations synced to SQLite database
- Seamless switching between CLI and Web UI
- `/sync` to pull in external changes

### Commands
| Command | Description |
|---------|-------------|
| `/conversations` | List recent conversations |
| `/switch <id>` | Switch to conversation |
| `/new` | Start new conversation |
| `/search <query>` | Search by content |
| `/rename <slug>` | Rename current conversation |
| `/archive` / `/unarchive` | Archive management |
| `/export [file]` | Export to markdown |

## Igor - File Transfer Assistant

### Web-Based File Upload
- Start with `shelley igor` (default port 8099)
- Drag & drop file uploads
- Image thumbnails with lightbox preview
- File browser and management
- Copy paths for CLI use

### CLI Integration
| Command | Description |
|---------|-------------|
| `/uploads` | List files in ~/uploads |
| `/pick` | List recent uploads (numbered) |
| `/pick <n>` | Analyze file n with Shelley |

### Supported File Types
- **Images**: PNG, JPG, JPEG, GIF, WEBP
- **Data**: CSV, JSON
- **Documents**: Markdown, plain text
- **Code**: Go, Python, JavaScript, TypeScript, Rust, C, C++, Java, etc.

## Coordinator - Distributed Task Execution

### Architecture
```
┌─────────────────┐     ┌─────────────────┐
│   Dashboard     │     │   Worker VMs    │
│  (browser UI)   │     │  (exe.dev VMs)  │
└────────┬────────┘     └────────┬────────┘
         │                       │
         ▼                       ▼
┌─────────────────────────────────────────┐
│           Coordinator Server            │
│  • Task queue (SQLite)                  │
│  • Worker management                    │
│  • Shared filesystem (~/shared)         │
│  • Auto-scaling                         │
│  • Git integration                      │
└─────────────────────────────────────────┘
```

### Dashboard (`shelley dashboard`)
- Web UI for managing tasks and workers
- Real-time coordinator logs
- Scale workers up/down
- Create task groups
- View conversation history

### Task Groups / Batches
- Group related tasks with shared repo/branch settings
- Parallel execution across workers
- Progress tracking (tasks_total, tasks_completed, tasks_failed)
- Create via API or dashboard

### CLI Management (`shelley coord-cli`)
```bash
shelley coord-cli add-task "Create a landing page"
shelley coord-cli add-group "Name" "Task 1" \| "Task 2" \| "Task 3"
shelley coord-cli scale 3
shelley coord-cli stats
shelley coord-cli workers
shelley watch  # Live auto-refreshing dashboard
```

## Git Integration

### Worker Git Workflow
- Clone repos at task start
- Create feature branches (`task-{id}`)
- Commit changes automatically
- Push to origin
- GitHub token support for HTTPS auth

### CLI Git Commands
| Command | Description |
|---------|-------------|
| `/git` | List recent commits |
| `/git show <id>` | Show files in commit |
| `/git diff <file>` | Colorized diff |

### Git Worktrees (with Tailscale)
- Shared `.git` directory across workers
- Each task gets its own worktree
- Real-time branch visibility
- Fast local merges

## Shared Filesystem (Tailscale)

### Structure
```
Coordinator ~/shared/
├── source/      → Input files for tasks
├── tasks/       ← Worker output
└── results/     ← Structured results (DONE.md)
```

### Features
- SSHFS mount from coordinator to workers
- Bidirectional file sharing
- Task-aware file staging via API
- Immediate visibility of results

## Structured Output

### DONE.md Format
Workers write structured results with YAML frontmatter:
```yaml
---
status: success  # success | partial | failed
files_changed:
  - path: auth/login.go
    action: created
    lines_added: 45
tests:
  passed: 5
  failed: 0
merge_ready: true
blockers: []
---
## Summary
...
```

### Dashboard Display
- Status badges (green/yellow/red)
- File change icons (+/~/−) with line counts
- Test results summary
- Blocker highlighting

## File Ownership & Conflict Detection

### Per-Task Ownership
- `owns_files`: Glob patterns for files task may modify
- `forbidden_files`: Patterns for files task must NOT touch
- Coordinator prevents conflicting tasks from running simultaneously

### Conflict Check API
```bash
curl -X POST http://localhost:8081/api/check-conflicts \
  -d '{"owns_files": ["auth/*.go"]}'
```

## Worker Management

### Auto-Scaling
- Workers spawn automatically when tasks enqueued
- Respects `max-workers` limit
- Idle workers auto-shutdown after 30 minutes

### Health Monitoring
| Status | Heartbeat Age | Action |
|--------|---------------|--------|
| Healthy | < 60s | Normal |
| Warning | 60-120s | Yellow indicator |
| Unhealthy | 120-300s | Orange indicator |
| Dead | > 300s | Auto-replaced, task reset |

### Drain Mode
- Graceful shutdown of all workers
- Idle workers deleted immediately
- Busy workers complete current task first

## Image Support

### Commands
| Command | Description |
|---------|-------------|
| `/attach <path>` | Attach image to next message |
| `/describe <path>` | Analyze with vision model |
| `/attachments` | List pending attachments |

### Remote Image Analysis
- `describe-image` script for local→VM upload
- `/imglist` and `/imgresult` for async results

## Sub-Agent Spawning

### Direct Spawning
```bash
shelley chat -yes -no-sync -prompt "Your task" &
```

### Slash Commands
| Command | Description |
|---------|-------------|
| `/spawn "prompt"` | Spawn single sub-agent |
| `/spawns` | List all sub-agents |
| `/spawn-output <id>` | View agent output |
| `/spawn-wait` | Wait for all to complete |
| `/parallel "A" \| "B" \| "C"` | Spawn multiple |

## Saved Scripts

### Script Registry
- Scripts stored in `~/.config/shelley/scripts/`
- Metadata in `registry.json`

### Commands
| Command | Description |
|---------|-------------|
| `/scripts` | List saved scripts |
| `/script-show <name>` | View script contents |
| `/script-run <name>` | Execute script |
| `/script-save <name> <path>` | Save new script |

## Project Templates

```bash
shelley unpack-template go /path/to/project
```

- Available: `go` (web server with SQLite, migrations, systemd)
- Creates ready-to-run project structure

## Keyboard Shortcuts

### Input
| Key | Action |
|-----|--------|
| Enter | Send message |
| Ctrl+J | Insert newline |
| Escape | Clear input / Cancel |
| Tab | Complete paths/commands |
| Up/Down | Prompt history |

### Scrolling
| Key | Action |
|-----|--------|
| Ctrl+U/D | Half page up/down |
| PgUp/PgDown | Full page |
| Home/End | Top/Bottom |
| Mouse wheel | Scroll (mouse mode) |

### Control
| Key | Action |
|-----|--------|
| Ctrl+C | Quit |
| Escape | Cancel operation |

## Technical Stack

- **Backend**: Go
- **Storage**: SQLite
- **Frontend**: TypeScript, React, esbuild
- **CLI**: Bubble Tea (Go TUI framework)
- **Markdown**: Glamour rendering

---

*For complete documentation, see the [README](../README.md), [CLI Reference](../docs/CLI_REFERENCE.md), and [Coordinator Guide](../coordinator/README.md).*
