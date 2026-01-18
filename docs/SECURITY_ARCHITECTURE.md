# Security Architecture

This document describes the security model, authentication mechanisms, and data protection strategies used in Shelley CLI.

## Overview

Shelley CLI operates in a distributed environment with multiple trust boundaries:

```
┌─────────────────────────────────────────────────────────────────────┐
│                         User's Browser                               │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  Web UI (React)                                              │    │
│  │  - X-Shelley-Request header (CSRF protection)               │    │
│  └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
                                   │
                                   │ HTTPS (exe.dev proxy)
                                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Coordinator VM (Primary)                         │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  Dashboard / Shelley Server                                  │    │
│  │  - API token authentication                                  │    │
│  │  - RequireHeader middleware (proxy authentication)           │    │
│  │  - SQLite with local file permissions                       │    │
│  └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
           │                              │
           │ SSH (exe.dev SSH gateway)    │ Tailscale (optional)
           ▼                              ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Worker VMs (Ephemeral)                           │
│  - Spawned via `ssh exe.dev create <worker-id>`                     │
│  - Isolated execution environment                                   │
│  - Auto-shutdown after idle timeout                                 │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Authentication Mechanisms

### 1. Web UI / API Authentication

#### CSRF Protection
All state-changing requests (POST, PUT, DELETE) require the `X-Shelley-Request` header:

```go
// middleware.go
func CSRFMiddleware() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
                if r.Header.Get("X-Shelley-Request") == "" {
                    http.Error(w, "CSRF protection: X-Shelley-Request header required", http.StatusForbidden)
                    return
                }
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

**Why this works:**
- Browsers cannot add custom headers to simple cross-origin requests
- CORS preflight blocks complex requests from unauthorized origins
- The header value doesn't matter—only its presence

#### Proxy Authentication (RequireHeader)
When running behind the exe.dev proxy, Shelley can require a specific header to be present:

```bash
shelley serve -require-header X-Exedev-Userid
```

This ensures all API requests pass through the authenticated proxy:

```go
func RequireHeaderMiddleware(headerName string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if strings.HasPrefix(r.URL.Path, "/api/") {
                if r.Header.Get(headerName) == "" {
                    http.Error(w, "missing required header: "+headerName, http.StatusForbidden)
                    return
                }
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

### 2. Coordinator API Authentication

The coordinator uses bearer token authentication via `X-Coordinator-Token` header:

```go
// handlers.go
func (c *Coordinator) CheckAuth(w http.ResponseWriter, r *http.Request) bool {
    if c.config.APIToken == "" {
        return true
    }
    token := r.Header.Get("X-Coordinator-Token")
    if token == "" {
        token = r.URL.Query().Get("token")
    }
    if token != c.config.APIToken {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return false
    }
    return true
}
```

**Token generation:**
- Tokens are generated using `crypto/rand` for cryptographic randomness
- Stored in SQLite `settings` table for persistence across restarts
- Displayed to the user on coordinator startup

### 3. SSH Key Authentication (Worker Management)

Worker VMs are managed via SSH through the exe.dev gateway:

```go
// SSH command routing
ssh exe.dev "ssh <worker-id> '<command>'"
```

**Trust model:**
- Coordinator VM must have an SSH key registered with exe.dev
- Workers are created/destroyed via `ssh exe.dev create/rm`
- All inter-VM communication goes through exe.dev's SSH infrastructure
- Workers cannot directly SSH to each other

### 4. LLM Gateway Authentication

When using exe.dev's LLM gateway:

```json
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
```

**Security properties:**
- Gateway is only accessible from within exe.dev VMs (link-local address)
- Uses `$EXE_DEV_TOKEN` environment variable for authentication
- No API keys stored in configuration files

---

## Data Protection

### SQLite Database Security

**Storage:**
- Default path: `~/.config/shelley/shelley.db`
- Coordinator DB: `coordinator.db`
- File permissions: Standard Unix user permissions

**Schema highlights:**
```sql
-- Credentials are stored encrypted or in environment
-- No plaintext API keys in database
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

**What's stored:**
- Conversation history
- Message content (including tool inputs/outputs)
- Coordinator tokens (hashed)
- Worker metadata

**What's NOT stored:**
- LLM API keys (use env vars or config file)
- User passwords (single-user system, no auth layer)
- SSH private keys (managed by filesystem)

### Secrets Management

| Secret | Storage Location | Access Method |
|--------|-----------------|---------------|
| Anthropic API Key | Environment or config | `ANTHROPIC_API_KEY` |
| OpenAI API Key | Environment or config | `OPENAI_API_KEY` |
| GitHub Token | Environment | `GITHUB_TOKEN` |
| Tailscale Auth Key | Environment or CLI flag | `TAILSCALE_AUTHKEY` |
| Coordinator API Token | SQLite `settings` table | Auto-generated, displayed on start |

**Best practices:**
1. Use environment variables for API keys
2. Never commit `shelley.json` with API keys to version control
3. Use the LLM gateway when running on exe.dev

---

## Network Security

### HTTPS Proxy (exe.dev)

All external access goes through exe.dev's HTTPS proxy:

```
https://<vm-name>.exe.xyz:<port>/
         │
         ▼
http://localhost:<port>/  (internal)
```

**Properties:**
- TLS termination at proxy
- Automatic HTTPS certificates
- Ports 3000-9999 are proxied

### Tailscale Integration (Optional)

For coordinator-worker communication:

```bash
shelley dashboard -tailscale-authkey tskey-auth-xxx
```

**Security benefits:**
- Private mesh network between coordinator and workers
- No public internet exposure for internal APIs
- Encrypted peer-to-peer communication
- Shared filesystem access (`~/shared`) via NFS over Tailscale

### Firewall Considerations

| Port | Service | Exposure |
|------|---------|----------|
| 8080 | Dashboard | HTTPS proxy |
| 8081 | Coordinator API | Internal/Tailscale |
| 8099 | Igor (file upload) | HTTPS proxy |
| 9999 | Shelley web UI | HTTPS proxy |

---

## Tool Execution Security

### Bash Tool

The `bash` tool executes arbitrary shell commands with these safeguards:

```go
type BashTool struct {
    CheckPermission PermissionCallback  // Called before each command
    SkipPermission  bool                 // --yes mode bypasses checks
    Timeouts        *Timeouts           // Configurable timeouts
}
```

**Timeouts:**
- Fast commands: 30 seconds
- Slow commands (builds, tests): 15 minutes
- Background processes: 24 hours

**Permission model:**
- In interactive mode: User prompted before execution
- In `-yes` mode: Auto-approve (use with caution)
- No sandboxing: Commands run with user's full privileges

### Git Co-author Trailer

All git commits are automatically tagged:

```go
req.Command = bashkit.AddCoauthorTrailer(req.Command, "Co-authored-by: Shelley <shelley@exe.dev>")
```

---

## Worker Security

### Worker Lifecycle

1. **Provisioning:**
   ```bash
   ssh exe.dev new --name=wk-abc --no-email --json
   ```

2. **Installation:**
   - HTTP download of shelley binary from coordinator
   - Config pushed via SSH

3. **Execution:**
   - Isolated VM environment
   - No direct SSH access between workers
   - All communication through coordinator API

4. **Termination:**
   - Auto-shutdown after idle timeout (default: 30 min)
   - VM destroyed: `ssh exe.dev rm <worker-id>`

### Worker Context Injection

Workers receive structured context:

```go
type WorkerContextData struct {
    TaskID         string
    WorkerID       string
    OwnsFiles      []string    // Allowed to modify
    ForbiddenFiles []string    // Must not touch
}
```

**File ownership prevents conflicts:**
- Each task declares which files it may modify
- Forbidden patterns prevent accidental overlap
- Violations logged in task results

---

## File Upload Security (Igor)

### Upload Handling

```go
// igor.go
http.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
    // Timestamp prefix prevents collisions
    filename := fmt.Sprintf("%d_%s", time.Now().Unix(), header.Filename)
    path := filepath.Join(uploadDir, filename)
    // ...
})
```

**Protections:**
- Path traversal prevention: `strings.Contains(name, "..")` rejected
- Uploads scoped to `~/uploads/` directory
- No execution of uploaded files
- DELETE requires explicit action

### Access Control

- Read: Open (serve static files)
- Write: POST only, no overwrite
- Delete: Per-file or bulk

---

## Audit & Logging

### Event Logging

Coordinator maintains an audit log:

```sql
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    event_type TEXT NOT NULL,  -- task.queued, worker.started, etc.
    task_id TEXT,
    worker_id TEXT,
    details TEXT  -- JSON metadata
);
```

### HTTP Request Logging

```go
func LoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
    config := sloghttp.Config{
        DefaultLevel:     slog.LevelInfo,
        ClientErrorLevel: slog.LevelInfo,
        ServerErrorLevel: slog.LevelInfo,
    }
    return sloghttp.NewWithConfig(logger, config)
}
```

---

## Threat Model

### In Scope

| Threat | Mitigation |
|--------|------------|
| CSRF attacks on API | X-Shelley-Request header |
| Unauthorized API access | Token authentication, RequireHeader |
| Man-in-the-middle | HTTPS proxy, Tailscale encryption |
| Command injection | User approval, timeout limits |
| Path traversal (uploads) | Filename sanitization |
| Worker impersonation | SSH key authentication |

### Out of Scope

| Threat | Reason |
|--------|--------|
| Compromised VM host | Trust exe.dev infrastructure |
| Malicious LLM responses | LLM safety is provider's responsibility |
| Local privilege escalation | Runs as user, no sandbox |
| Supply chain attacks | Standard npm/go dependency risk |

---

## Security Recommendations

### For Operators

1. **Use environment variables for API keys:**
   ```bash
   export ANTHROPIC_API_KEY="sk-ant-xxx"
   export GITHUB_TOKEN="ghp_xxx"
   ```

2. **Enable RequireHeader in production:**
   ```bash
   shelley serve -require-header X-Exedev-Userid
   ```

3. **Use Tailscale for coordinator-worker communication:**
   ```bash
   shelley dashboard -tailscale-authkey tskey-auth-xxx
   ```

4. **Regularly rotate coordinator tokens:**
   - Delete token from `settings` table
   - Restart coordinator for new token

### For Developers

1. **Never log API keys or tokens**
2. **Sanitize all file paths**
3. **Use crypto/rand for token generation**
4. **Validate worker responses before acting on them**

---

## Version History

| Date | Version | Changes |
|------|---------|--------|
| 2025-01-18 | 1.0 | Initial security architecture document |
