# Shelley Technical Overview

## 1. Architecture

### Language & Frameworks

**Backend:** Go (1.23+)
- HTTP server using stdlib `net/http`
- SQLite database via `modernc.org/sqlite` (pure Go, CGO-free)
- Type-safe SQL queries via `sqlc`
- Structured logging via `log/slog`

**Frontend:** TypeScript + React
- Build: esbuild (fast bundling)
- Single-page application with Server-Sent Events (SSE) for real-time updates
- Mobile-first responsive design

### Project Structure

```
shelley/
├── cmd/shelley/        # CLI entry point
├── server/             # HTTP API and conversation management
├── loop/               # Core agent loop (LLM request → tool execution → repeat)
├── llm/                # LLM provider abstractions
│   ├── ant/            # Anthropic (Claude)
│   ├── oai/            # OpenAI (GPT)
│   └── gem/            # Google (Gemini)
├── claudetool/         # Tool implementations (bash, patch, etc.)
├── db/                 # Database layer with schema migrations
├── models/             # Model registry and service factory
├── ui/                 # React frontend
├── cli/                # Terminal-based TUI client (Bubble Tea)
└── memory/             # Persistent memory storage
```

### Main Components

1. **Server** (`server/server.go`): HTTP router, conversation lifecycle, SSE streaming, file operations
2. **ConversationManager** (`server/convo.go`): Per-conversation state, LLM loop management, message persistence
3. **Loop** (`loop/loop.go`): Core agent loop - sends messages to LLM, executes tool calls, handles responses
4. **ToolSet** (`claudetool/toolset.go`): Creates and manages tools for each conversation
5. **Models Manager** (`models/models.go`): Factory pattern for LLM services by model ID

---

## 2. How It Works

### Request Flow (User Message → Execution)

```
1. User sends message via UI (POST /api/conversation/{id}/chat)
   ↓
2. Server.handleChat() validates request, gets ConversationManager
   ↓
3. ConversationManager.AcceptUserMessage() records message to DB, queues to Loop
   ↓
4. Loop.processLLMRequest() sends conversation history + tools to LLM
   ↓
5. LLM responds with text or tool_use blocks
   ↓
6. If tool_use: Loop.handleToolCalls() executes each tool (bash, patch, etc.)
   ↓
7. Tool results are recorded and sent back to LLM (step 4 repeats)
   ↓
8. When LLM responds without tool calls (EndOfTurn), loop iteration ends
   ↓
9. UI receives updates via SSE stream (/api/conversation/{id}/stream)
```

### SSE Real-time Updates

The frontend subscribes to `/api/conversation/{id}/stream`. The server uses a `subpub` (pub/sub) pattern to push incremental `StreamResponse` messages containing:
- New messages (not full history)
- Conversation metadata
- `AgentWorking` boolean
- Context window usage stats

### Database Schema

```sql
conversations (conversation_id, slug, cwd, archived, created_at, updated_at)
messages (message_id, conversation_id, sequence_id, type, llm_data, user_data, display_data, usage_data, created_at)
```
- `llm_data`: Full LLM message JSON (sent to model)
- `display_data`: Tool-specific UI display content (not sent to LLM)
- `sequence_id`: Auto-incrementing per conversation for reliable ordering

### Authentication & exe.dev Integration

Shelley is **single-user by design** with no built-in authentication. Instead:

1. **RequireHeaderMiddleware**: Optional `-require-header` flag enforces that all API requests have a specific header (e.g., `X-Exedev-Userid`). This allows an upstream proxy to handle auth.

2. **Gateway Support**: LLM API calls can be routed through a gateway URL (`-config` file with `gateway` field). This enables:
   - Centralized API key management
   - Cost tracking and rate limiting
   - Routing: `<gateway>/_/gateway/anthropic/v1/messages`, etc.

3. **Terminal URL**: Optional `terminal_url` config injects a link to an external terminal UI.

4. **exe.dev Detection**: The system prompt generator (`server/system_prompt.go`) detects if running on exe.dev and adjusts behavior (e.g., hostname suffix `.exe.xyz`).

**There is no direct communication with a "remote VM"** - Shelley runs locally on the same machine where code execution happens. For exe.dev, Shelley runs inside the VM itself.

---

## 3. Core Capabilities

### Tools Available to the Agent

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands (with timeouts, background support) |
| `patch` | Precise file modifications (replace, append, prepend, overwrite, clipboards) |
| `change_dir` | Persistently change working directory |
| `keyword_search` | Semantic search across codebase |
| `think` | Internal reasoning (no side effects) |
| `browser_*` | Browser automation (navigate, screenshot, eval, etc.) - optional |

### CLI Commands

```bash
shelley serve [flags]        # Start web server (default)
shelley chat [flags]         # Interactive TUI mode
shelley memory add/list/rm   # Persistent memory management
shelley unpack-template      # Project scaffolding
shelley version              # Print version JSON
```

### Web API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/conversations` | GET | List conversations |
| `/api/conversations/new` | POST | Create conversation + send first message |
| `/api/conversation/{id}` | GET | Get conversation with messages |
| `/api/conversation/{id}/stream` | GET | SSE stream for real-time updates |
| `/api/conversation/{id}/chat` | POST | Send message |
| `/api/conversation/{id}/cancel` | POST | Cancel current operation |
| `/api/git/diffs` | GET | Git diff information |

### Supported Models

Configured in `models/models.go`:
- Claude: `claude-opus-4.5`, `claude-sonnet-4.5`, `claude-haiku-4.5`
- OpenAI: `gpt-5`, `gpt-5-nano`, `gpt-5.1-codex`
- Fireworks: `qwen3-coder-fireworks`, `glm-4p6-fireworks`
- Built-in: `predictable` (deterministic test fixture)

---

## 4. Interesting Design Decisions

### 1. **No Persistent Bash State**
Each `bash` tool call runs in isolation via `bash -c`. Working directory, env vars, and aliases don't persist between calls. A separate `change_dir` tool maintains the cwd across the conversation.

### 2. **Simplified Patch Schema for Weaker Models**
The patch tool has two schemas: full (with clipboards, reindentation) for "strong" models (Sonnet/Opus) and simplified (single patch operation) for others. See `isStrongModel()` in `toolset.go`.

### 3. **Just-in-Time Tool Installation**
When the bash tool encounters a missing command, it can optionally:
1. Use a cheap LLM to validate the tool is legitimate
2. Auto-install via the detected package manager

### 4. **Message Sequence IDs**
Instead of relying on timestamps (which can collide), messages use auto-incrementing `sequence_id` per conversation for reliable ordering.

### 5. **Prompt Caching**
The loop sets `Cache: true` on the last tool and last user message content block to leverage Anthropic's prompt caching for cheaper repeat requests.

### 6. **Process Group Killing**
Background bash commands run in their own process group (`Setpgid: true`) so they can be cleanly killed with `kill -PGID`.

### 7. **No Fallbacks**
Per `AGENT.md`: errors propagate or crash rather than silently degrading. No compatibility shims—this is a new project.

### 8. **Git State Tracking**
At end of each turn, the loop checks for git state changes (branch, commit) and records `gitinfo` messages visible in UI but not sent to the LLM.

---

## 5. Current Limitations / TODOs

### Explicit in Code

1. **ARCHITECTURE.md**: UI is marked "TODO: A mobile-first UI" but is actually React (not VueJS as documented).

2. **Subagent Conversations**: Comment in ARCHITECTURE.md: "TODOX: Subagent/tool conversations are done with user_initiated=false" - unclear if fully implemented.

3. **seccomp**: Commented-out code in `main.go` for blocking `pkill -f shelley` via seccomp - disabled because it blocks sudo.

4. **Session Persistence in CLI**: The TUI saves sessions as JSON files in `~/.shelley/sessions/` but doesn't sync with the main DB.

### Observable Gaps

1. **No Multi-User**: Single SQLite database, no user isolation or permissions.

2. **No Sandboxing**: Tools execute with full user permissions. Security relies on external sandboxing (containers, VMs).

3. **Limited Error Recovery**: If an LLM request fails mid-turn, `insertMissingToolResults()` adds placeholder errors but recovery is manual.

4. **No Streaming Responses**: LLM responses are received complete, then displayed. No token-by-token streaming.

5. **Browser Tools Optional**: Only enabled via `ToolSetConfig.EnableBrowser = true` - not exposed in CLI.

6. **Memory System**: `memory/memory.go` exists but integration is minimal (appended to system prompt).

7. **No Rate Limiting**: API endpoints are unprotected against DoS without an upstream proxy.

8. **Gemini Support**: Listed in config but `gem.go` appears less mature than `ant.go` or `oai.go`.

---

## Summary

Shelley is a well-structured, single-user coding agent with:
- Clean separation between LLM abstraction, tool execution, and persistence
- Real-time UI via SSE with incremental updates
- Multi-model support with provider-specific adapters
- Both web and TUI interfaces

It's designed to run locally or inside a VM (like exe.dev), delegating authentication to an upstream proxy. The codebase follows explicit design principles (no fallbacks, propagate errors, single way of doing things) and uses modern Go idioms throughout.
