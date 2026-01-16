# Coordinator Improvements - January 16, 2026

This document describes the changes made to improve coordinator reliability and usability.
Use this as a reference if you need to understand or extend these features.

## Summary of Changes

### 1. Persistent Worker Prefix (P1 - Reliability)

**Problem:** Each coordinator restart generated a new random worker prefix (e.g., `wk-abc`, `wk-def`), making it impossible to find/manage workers from previous runs.

**Solution:** Store the worker prefix in the `settings` table on first run, then load it on subsequent restarts.

**File:** `coordinator/coordinator.go` (~line 180)

```go
// Load or create persistent worker prefix
if config.WorkerPrefix != "" {
    var savedPrefix string
    err := db.QueryRow(`SELECT value FROM settings WHERE key = 'worker_prefix'`).Scan(&savedPrefix)
    if err == nil && savedPrefix != "" {
        config.WorkerPrefix = savedPrefix
    } else {
        // Generate new prefix and persist it
        b := make([]byte, 2)
        rand.Read(b)
        config.WorkerPrefix = fmt.Sprintf("%s-%s", config.WorkerPrefix, hex.EncodeToString(b)[:3])
        db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('worker_prefix', ?)`, config.WorkerPrefix)
    }
}
```

### 2. Startup Cleanup (P1 - Reliability)

**Problem:** Orphaned workers and stuck tasks from previous runs weren't cleaned up until periodic cleanup ran (30+ seconds after start).

**Solution:** Run cleanup immediately on startup.

**File:** `coordinator/coordinator.go` - `StartBackgroundTasks()`

```go
func (c *Coordinator) StartBackgroundTasks() {
    // Run immediate cleanup on startup
    log.Printf("Running startup cleanup...")
    c.CleanupStaleWorkers()
    c.CleanupStuckTasks()
    
    go c.periodicCleanup()
}
```

### 3. Reduced Failed Worker Retention (P0 - Quick Win)

**Problem:** Failed worker records stayed in DB for 1 hour, cluttering the dashboard.

**Solution:** Reduce retention to 10 minutes.

**File:** `coordinator/coordinator.go` - `CleanupStaleWorkers()`

```go
// Changed from '-1 hour' to '-10 minutes'
res, _ := c.db.Exec(`DELETE FROM workers WHERE status IN ('failed', 'deleted') AND created_at < datetime('now', '-10 minutes')`)
```

### 4. Filter Failed Workers from Dashboard (P0 - UX)

**Problem:** Failed workers cluttered the dashboard UI.

**Solution:** 
- Add `show_failed` query parameter to `/api/workers` endpoint
- Add checkbox in dashboard to optionally show failed workers
- Show failed worker count in section header

**File:** `coordinator/coordinator.go` - `ListWorkers()`

```go
func (c *Coordinator) ListWorkers(showFailed bool) ([]Worker, error) {
    query := `SELECT ... FROM workers WHERE status != 'deleted'`
    if !showFailed {
        query = `SELECT ... FROM workers WHERE status NOT IN ('deleted', 'failed')`
    }
    // ...
}
```

**File:** `coordinator/handlers.go` - `HandleListWorkers()`

```go
func (c *Coordinator) HandleListWorkers(w http.ResponseWriter, r *http.Request) {
    showFailed := r.URL.Query().Get("show_failed") == "true"
    workers, err := c.ListWorkers(showFailed)
    // ...
}
```

**File:** `coordinator/dashboard.html` - Added checkbox and badge

### 5. Clear Failed Workers Button (P2 - UX)

**Problem:** No easy way to clear failed worker records.

**Solution:** Add API endpoint and dashboard button.

**File:** `coordinator/handlers.go` - New handler

```go
func (c *Coordinator) HandleClearFailedWorkers(w http.ResponseWriter, r *http.Request) {
    result, _ := c.db.Exec(`DELETE FROM workers WHERE status IN ('failed', 'deleted')`)
    count, _ := result.RowsAffected()
    json.NewEncoder(w).Encode(map[string]int64{"deleted": count})
}
```

**File:** `cmd/shelley/main.go` - Register route

```go
mux.HandleFunc("/api/workers/clear-failed", coord.HandleClearFailedWorkers)
```

### 6. Show Worker Error Messages (P2 - UX)

**Problem:** Failed workers didn't show why they failed.

**Solution:** Query events table for error details and include in Worker struct.

**File:** `coordinator/coordinator.go` - Added `Error` field to Worker struct

```go
type Worker struct {
    // ... existing fields ...
    Error *string `json:"error,omitempty"` // error message if status is 'failed'
}
```

**File:** `coordinator/coordinator.go` - `ListWorkers()` - Query error from events

```go
if w.Status == "failed" {
    var details sql.NullString
    c.db.QueryRow(`SELECT details FROM events WHERE worker_id = ? AND event_type = 'worker.failed' ORDER BY timestamp DESC LIMIT 1`, w.ID).Scan(&details)
    if details.Valid {
        var d map[string]interface{}
        if json.Unmarshal([]byte(details.String), &d) == nil {
            if errMsg, ok := d["error"].(string); ok {
                w.Error = &errMsg
            }
        }
    }
}
```

### 7. Fix Conversation Sync "Argument List Too Long" (Bug Fix)

**Problem:** Worker loop script failed with "Argument list too long" when syncing large conversations via jq command-line arguments.

**Solution:** Use temp files instead of command-line arguments.

**File:** `coordinator/coordinator.go` - Worker loop script (~line 1310)

```bash
# Before (broken for large conversations):
SYNC_PAYLOAD=$(jq -n --argjson conv "$CONV_DATA" --argjson msgs "$MSG_DATA" ...)

# After (uses temp files):
SYNC_TMP=$(mktemp)
sqlite3 -json "$SHELLEY_DB" "SELECT * FROM conversations WHERE conversation_id='$CONV_ID'" | jq '.[0]' > "$SYNC_TMP.conv"
sqlite3 -json "$SHELLEY_DB" "SELECT * FROM messages WHERE conversation_id='$CONV_ID' ORDER BY sequence_id" > "$SYNC_TMP.msgs"
jq -n --slurpfile conv "$SYNC_TMP.conv" --slurpfile msgs "$SYNC_TMP.msgs" ... > "$SYNC_TMP.payload"
curl ... -d @"$SYNC_TMP.payload"
rm -f "$SYNC_TMP" "$SYNC_TMP.conv" "$SYNC_TMP.msgs" "$SYNC_TMP.payload"
```

### 8. New CLI Commands (P2 - UX)

**Problem:** No easy way to create task groups or manage failed workers from CLI.

**Solution:** Add new commands to `coord-cli`.

**File:** `cmd/shelley/coord_cli.go`

New commands:
- `add-group <name> <prompts>` - Create task group (prompts separated by `|`)
- `groups` - List all task groups
- `group <id>` - Show group details and tasks
- `clear-failed` - Clear failed worker records

Example usage:
```bash
# Create a task group
shelley coord-cli add-group "Landing Pages" \
  "Create docker.html" \| "Create k8s.html" \| "Create terraform.html"

# Check progress
shelley coord-cli groups
shelley coord-cli group ca0327cc48c02ef1

# Clear failed workers
shelley coord-cli clear-failed
```

## Files Modified

| File | Changes |
|------|---------|
| `coordinator/coordinator.go` | Persistent prefix, startup cleanup, reduced retention, worker error field, list workers filter, conversation sync fix |
| `coordinator/handlers.go` | `HandleClearFailedWorkers`, `HandleListWorkers` with `show_failed` param |
| `coordinator/dashboard.html` | Show failed checkbox, clear failed button, error display, failed count badge |
| `cmd/shelley/main.go` | Register `/api/workers/clear-failed` route |
| `cmd/shelley/coord_cli.go` | `add-group`, `groups`, `group`, `clear-failed` commands |
| `docs/CLI_REFERENCE.md` | Document new commands |

## Testing

```bash
# Test persistent prefix (restart coordinator, check prefix stays same)
sqlite3 ~/.config/shelley/coordinator.db "SELECT * FROM settings WHERE key='worker_prefix';"

# Test clear failed
shelley coord-cli -port 8080 clear-failed

# Test add-group
shelley coord-cli -port 8080 add-group "Test" "task 1" \| "task 2"
shelley coord-cli -port 8080 groups

# Test show_failed filter
curl -s "http://localhost:8080/api/workers" -H "X-Coordinator-Token: $TOKEN"
curl -s "http://localhost:8080/api/workers?show_failed=true" -H "X-Coordinator-Token: $TOKEN"
```

## Commits

- `9cca36f` - Coordinator reliability improvements (P0-P2)
- `6ffb758` - Fix conversation sync 'argument list too long' error
- `fd2f550` - Add group commands and clear-failed to coord-cli
- `eb8f401` - Update CLI_REFERENCE.md with new coord-cli commands
