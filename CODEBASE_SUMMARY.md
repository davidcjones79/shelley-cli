# Shelley CLI - Codebase Summary

> A terminal interface and distributed task coordination system for the Shelley coding agent.

## Overview

Shelley CLI is a fork of [boldsoftware/shelley](https://github.com/boldsoftware/shelley) that adds a full-featured terminal UI, distributed task execution via a coordinator system, and file transfer capabilities. It's designed to run on [exe.dev](https://exe.dev/) VMs.

**Primary use case**: AI-powered coding assistant that can execute shell commands, edit files, browse the web, and run parallel tasks across multiple VMs.

---

## Tech Stack

| Layer | Technology |
|-------|------------|
| Backend | Go 1.25+ |
| Database | SQLite (pure Go via `modernc.org/sqlite`) |
| SQL Queries | `sqlc` (type-safe generated code) |
| CLI TUI | [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lip Gloss](https://github.com/charmbracelet/lipgloss) |
| Frontend | TypeScript + React (esbuild bundled) |
| Real-time | Server-Sent Events (SSE) |
| Shared FS | Tailscale (for coordinator/worker communication) |

---

## Project Structure

```
shelley-cli/
├── cmd/shelley/         # CLI entry point, command dispatch
├── server/              # HTTP API, conversation management, SSE streaming
├── loop/                # Core agent loop (LLM → tool execution → repeat)
├── llm/                 # LLM provider abstractions
│   ├── ant/             # Anthropic (Claude)
│   ├── oai/             # OpenAI (GPT)
│   └── gem/             # Google (Gemini)
├── claudetool/          # Tool implementations (bash, patch, keyword_search, etc.)
├── cli/                 # Terminal TUI (Bubble Tea)
├── coordinator/         # Distributed task queue and worker pool
├── db/                  # Database layer with migrations
├── models/              # Model registry and service factory
├── ui/                  # React frontend (web UI)
├── templates/           # Project scaffolding templates
└── docs/                # Documentation
```

---

## Commands

| Command | Description |
|---------|-------------|
| `shelley chat` | Interactive CLI chat mode |
| `shelley serve` | Start the web server |
| `shelley dashboard` | Start dashboard with coordinator management UI |
| `shelley coord` | Start coordinator server (headless) |
| `shelley coord-cli` | Manage coordinator from command line |
| `shelley watch` | Live CLI dashboard for coordinator |
| `shelley igor` | Start Igor file transfer server |
| `shelley status` | Show status of all Shelley services |
| `shelley unpack-template` | Unpack a project template |
| `shelley version` | Print version information as JSON |

---

## Key Components

### 1. Agent Loop (`loop/`)

The core agent loop that:
1. Sends conversation history + tool definitions to the LLM
2. Receives response (text or tool calls)
3. Executes tool calls (bash, patch, etc.)
4. Records results and loops until LLM responds without tool calls

### 2. Tools (`claudetool/`)

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands (with timeouts, background support) |
| `patch` | Precise file modifications (replace, append, prepend, overwrite, clipboards) |
| `change_dir` | Persistently change working directory |
| `keyword_search` | Semantic search across codebase |
| `think` | Internal reasoning (no side effects) |
| `browser_*` | Browser automation (navigate, screenshot, eval) |

### 3. Server (`server/`)

HTTP API handling:
- Conversation CRUD
- Message streaming via SSE
- Git diff endpoints
- File uploads

### 4. CLI (`cli/`)

A full terminal UI built with Bubble Tea featuring:
- Streaming responses with markdown rendering
- Conversation history and switching
- Slash commands (`/models`, `/switch`, `/pick`, `/spawn`, etc.)
- Tab completion for file paths
- Mouse support

### 5. Coordinator (`coordinator/`)

Distributed task execution system:
- **Dashboard**: Web UI for managing tasks and workers
- **Task Queue**: SQLite-backed queue with retry logic
- **Worker Pool**: Auto-scales exe.dev VMs
- **Task Groups**: Batch multiple related tasks
- **Shared Filesystem**: Tailscale-based file sharing between coordinator and workers
- **Git Integration**: Workers clone repos, create branches, commit, and push

### 6. Igor (`server/igor.go`)

File transfer server that provides a web interface for dragging/dropping files to upload to the VM.

---

## Supported LLM Models

| Provider | Models |
|----------|--------|
| Anthropic | `claude-opus-4.5`, `claude-sonnet-4.5`, `claude-haiku-4.5` |
| OpenAI | `gpt-5`, `gpt-5-nano`, `gpt-5.1-codex` |
| Fireworks | `qwen3-coder-fireworks`, `glm-4p6-fireworks` |
| Built-in | `predictable` (deterministic test fixture) |

---

## Database Schema

```sql
conversations (
  conversation_id TEXT PRIMARY KEY,
  slug TEXT UNIQUE,
  cwd TEXT,
  archived BOOLEAN,
  created_at DATETIME,
  updated_at DATETIME
)

messages (
  message_id TEXT PRIMARY KEY,
  conversation_id TEXT,
  sequence_id INTEGER,       -- Auto-incrementing per conversation
  type TEXT,                 -- user, assistant, tool_use, tool_result, etc.
  llm_data TEXT,             -- Full LLM message JSON (sent to model)
  display_data TEXT,         -- Tool-specific UI content (not sent to LLM)
  usage_data TEXT,           -- Token usage statistics
  created_at DATETIME
)
```

---

## Architecture Highlights

### Request Flow

```
User Input → Server.handleChat() → ConversationManager.AcceptUserMessage()
    → Loop.processLLMRequest() → LLM Response
    → If tool_use: Execute tools → Record results → Loop again
    → When complete: SSE stream updates to UI
```

### Design Principles

1. **No Fallbacks**: Errors propagate or crash rather than silently degrading
2. **No Persistent Bash State**: Each `bash` call is isolated; use `change_dir` for persistent cwd
3. **No Compatibility Shims**: Single way of doing things
4. **Prompt Caching**: Leverages Anthropic's caching for cheaper repeat requests
5. **Single-User**: No built-in auth; relies on upstream proxy (exe.dev) for authentication

### Worker Context & Output

Workers receive detailed context including task ID, file ownership rules, and output format. They write structured `DONE.md` files with YAML frontmatter:

```yaml
status: success  # or: partial, failed
files_changed:
  - path: auth/login.go
    action: created
    lines_added: 45
tests:
  passed: 5
  failed: 0
merge_ready: true
```

---

## Key Files

| File | Description |
|------|-------------|
| `cmd/shelley/main.go` | Entry point, command dispatch |
| `server/server.go` | HTTP router, middleware |
| `server/handlers.go` | API endpoint handlers |
| `server/convo.go` | Conversation lifecycle management |
| `loop/loop.go` | Core agent loop |
| `cli/cli.go` | Main TUI logic (~57KB) |
| `cli/commands.go` | Slash command implementations |
| `coordinator/coordinator.go` | Task queue and worker management |
| `coordinator/dashboard.go` | Web dashboard |
| `claudetool/bash.go` | Shell execution tool |
| `claudetool/patch.go` | File editing tool |

---

## Building

```bash
make              # Build everything
make build        # Build Go binary
make ui           # Build React frontend
make test         # Run tests
make serve        # Build and start server
```

---

## Configuration

Create `~/.config/shelley/shelley.json`:

```json
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
```

Or use environment variables:
```bash
export ANTHROPIC_API_KEY="your-key"
export ANTHROPIC_BASE_URL="https://api.anthropic.com"
```

---

## License

Apache 2.0
