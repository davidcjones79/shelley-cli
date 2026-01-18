package coordinator

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite driver for sync
)

// APIError represents a structured error response with helpful suggestions.
type APIError struct {
	Error       string   `json:"error"`
	Code        string   `json:"code,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// writeAPIError writes a structured error response.
func writeAPIError(w http.ResponseWriter, status int, message, code string, suggestions ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(APIError{
		Error:       message,
		Code:        code,
		Suggestions: suggestions,
	})
}

// CheckAuth validates the API token.
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

// HandleEnqueue adds a task to the queue.
func (c *Coordinator) HandleEnqueue(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "POST method required", "METHOD_NOT_ALLOWED",
			"Use POST to create tasks: POST /api/enqueue or POST /api/tasks",
			"To list tasks, use GET /api/tasks")
		return
	}

	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "Invalid JSON body", "INVALID_JSON",
			"Request body must be valid JSON",
			"Example: {\"prompt\": \"Your task prompt here\"}")
		return
	}
	if req.Prompt == "" {
		writeAPIError(w, http.StatusBadRequest, "prompt field is required", "MISSING_FIELD",
			"Include a prompt in the request body",
			"Example: {\"prompt\": \"Create a hello world script\"}")
		return
	}

	task, err := c.EnqueueTask(req)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "INTERNAL_ERROR")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// HandleNextTask returns the next task for a worker.
func (c *Coordinator) HandleNextTask(w http.ResponseWriter, r *http.Request) {
	workerID := r.URL.Query().Get("worker")
	if workerID == "" {
		http.Error(w, "worker param required", http.StatusBadRequest)
		return
	}

	// Optional: worker can report its shelley version
	version := r.URL.Query().Get("version")
	if version != "" {
		// Set ready_at on first version report (marks worker as fully provisioned)
		c.db.Exec(`UPDATE workers SET shelley_version = ?, ready_at = COALESCE(ready_at, CURRENT_TIMESTAMP) WHERE id = ?`, version, workerID)
	}

	task, err := c.GetNextTask(workerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if task == nil {
		w.Write([]byte(`{}`))
		return
	}
	json.NewEncoder(w).Encode(task)
}

// HandleComplete receives task completion from workers.
func (c *Coordinator) HandleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req CompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := c.CompleteTask(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleWorkerShutdown handles worker self-shutdown.
func (c *Coordinator) HandleWorkerShutdown(w http.ResponseWriter, r *http.Request) {
	workerID := r.URL.Query().Get("worker")
	if workerID == "" {
		http.Error(w, "worker param required", http.StatusBadRequest)
		return
	}

	c.DeleteWorker(workerID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleScale adjusts worker count.
func (c *Coordinator) HandleScale(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	workersStr := r.URL.Query().Get("workers")
	workers, err := strconv.Atoi(workersStr)
	if err != nil || workers < 0 {
		http.Error(w, "invalid workers count", http.StatusBadRequest)
		return
	}

	// Cancel drain mode when scaling up
	if workers > 0 {
		c.StopDraining()
	}

	if err := c.ScaleWorkers(workers); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleDrain initiates graceful shutdown of all workers.
func (c *Coordinator) HandleDrain(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	idleDeleted, busyDraining := c.DrainWorkers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "draining",
		"idle_deleted":  idleDeleted,
		"busy_draining": busyDraining,
	})
}

// HandleCleanupWorkers triggers cleanup of stale workers.
func (c *Coordinator) HandleCleanupWorkers(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	c.CleanupStaleWorkers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleClearFailedWorkers immediately deletes all failed/deleted worker records from DB.
func (c *Coordinator) HandleClearFailedWorkers(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	result, err := c.db.Exec(`DELETE FROM workers WHERE status IN ('failed', 'deleted')`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	count, _ := result.RowsAffected()
	log.Printf("Cleared %d failed/deleted worker records", count)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"deleted": count})
}

// HandleStuckTasks returns tasks that appear to be stuck.
func (c *Coordinator) HandleStuckTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	// Get task timeout (default 15 minutes)
	taskTimeout := c.config.TaskTimeout
	if taskTimeout == 0 {
		taskTimeout = 15 * time.Minute
	}
	timeoutMinutes := int(taskTimeout.Minutes())

	// Get active workers
	activeWorkers := make(map[string]bool)
	rows, err := c.db.Query(`SELECT id FROM workers WHERE status IN ('idle', 'busy', 'ready')`)
	if err == nil {
		for rows.Next() {
			var id string
			rows.Scan(&id)
			activeWorkers[id] = true
		}
		rows.Close()
	}

	type stuckTask struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		Prompt    string  `json:"prompt"`
		WorkerID  *string `json:"worker_id"`
		Reason    string  `json:"reason"`
		Retries   int     `json:"retry_count"`
	}
	var stuck []stuckTask

	// Find orphaned tasks (assigned to non-existent workers)
	rows, _ = c.db.Query(`SELECT id, status, prompt, worker_id, COALESCE(retry_count, 0) FROM tasks WHERE status IN ('assigned', 'running') AND worker_id IS NOT NULL`)
	for rows.Next() {
		var t stuckTask
		var workerID string
		rows.Scan(&t.ID, &t.Status, &t.Prompt, &workerID, &t.Retries)
		t.WorkerID = &workerID
		if !activeWorkers[workerID] {
			t.Reason = "orphaned (worker missing)"
			stuck = append(stuck, t)
		}
	}
	rows.Close()

	// Find timed out tasks
	query := fmt.Sprintf(`SELECT id, status, prompt, worker_id, COALESCE(retry_count, 0) FROM tasks WHERE status = 'running' AND started_at < datetime('now', '-%d minutes')`, timeoutMinutes)
	rows, _ = c.db.Query(query)
	for rows.Next() {
		var t stuckTask
		var workerID sql.NullString
		rows.Scan(&t.ID, &t.Status, &t.Prompt, &workerID, &t.Retries)
		if workerID.Valid {
			t.WorkerID = &workerID.String
		}
		// Don't double-count
		alreadyStuck := false
		for _, s := range stuck {
			if s.ID == t.ID {
				alreadyStuck = true
				break
			}
		}
		if !alreadyStuck {
			t.Reason = fmt.Sprintf("timeout (>%d min)", timeoutMinutes)
			stuck = append(stuck, t)
		}
	}
	rows.Close()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stuck)
}

// HandleResetStuckTasks resets all stuck tasks to queued.
func (c *Coordinator) HandleResetStuckTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	c.CleanupStuckTasks()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleHeartbeat receives heartbeats from workers during task execution.
func (c *Coordinator) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	workerID := r.URL.Query().Get("worker")
	taskID := r.URL.Query().Get("task")
	if workerID == "" {
		http.Error(w, "worker param required", http.StatusBadRequest)
		return
	}

	c.db.Exec(`UPDATE workers SET last_heartbeat = CURRENT_TIMESTAMP WHERE id = ?`, workerID)
	
	// Optionally log task progress
	if taskID != "" {
		log.Printf("Heartbeat: worker %s, task %s", workerID, taskID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// HandleTaskStart marks a task as started (running) when a worker begins execution.
func (c *Coordinator) HandleTaskStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("task")
	workerID := r.URL.Query().Get("worker")
	if taskID == "" || workerID == "" {
		http.Error(w, "task and worker params required", http.StatusBadRequest)
		return
	}

	result, err := c.db.Exec(`UPDATE tasks SET status = 'running', started_at = CURRENT_TIMESTAMP WHERE id = ? AND worker_id = ?`, taskID, workerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		http.Error(w, "task not found or not assigned to this worker", http.StatusNotFound)
		return
	}

	log.Printf("Task %s started by worker %s", taskID, workerID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleResetTask resets a stuck task to queued status.
func (c *Coordinator) HandleResetTask(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id param required", http.StatusBadRequest)
		return
	}

	result, err := c.db.Exec(`UPDATE tasks SET status = 'queued', worker_id = NULL, assigned_at = NULL, started_at = NULL WHERE id = ? OR id LIKE ?`, taskID, taskID+"%")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleClearTasks clears all tasks from the queue.
func (c *Coordinator) HandleClearTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	_, err := c.db.Exec(`DELETE FROM tasks`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleGetTask returns a specific task.
func (c *Coordinator) HandleGetTask(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		writeAPIError(w, http.StatusBadRequest, "id parameter is required", "MISSING_PARAM",
			"Include task ID: GET /api/task?id=<task-id>",
			"List tasks first: GET /api/tasks")
		return
	}

	task, err := c.GetTask(taskID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, 
			fmt.Sprintf("Task '%s' not found", taskID), 
			"TASK_NOT_FOUND",
			"The task ID may be incorrect or the task was deleted",
			"List all tasks: GET /api/tasks",
			"Use full task ID or prefix (e.g., abc12345)")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// HandleListTasks returns all tasks (GET) or creates a task (POST).
// POST acts as an alias for /api/enqueue for API consistency.
func (c *Coordinator) HandleListTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	// POST /api/tasks creates a task (alias for /api/enqueue)
	if r.Method == http.MethodPost {
		c.HandleEnqueue(w, r)
		return
	}

	// GET /api/tasks lists tasks
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	tasks, err := c.ListTasks(status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// HandleListWorkers returns all workers.
// Optional query param: show_failed=true to include failed workers
func (c *Coordinator) HandleListWorkers(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	showFailed := r.URL.Query().Get("show_failed") == "true"
	workers, err := c.ListWorkers(showFailed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workers)
}

// HandleStats returns current statistics.
func (c *Coordinator) HandleStats(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c.GetStats())
}

// HandleCreateGroup creates a new task group.
func (c *Coordinator) HandleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req GroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	group, err := c.CreateGroup(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(group)
}

// HandleGetGroup returns a specific group.
func (c *Coordinator) HandleGetGroup(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	groupID := r.URL.Query().Get("id")
	if groupID == "" {
		http.Error(w, "id param required", http.StatusBadRequest)
		return
	}

	group, err := c.GetGroup(groupID)
	if err != nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(group)
}

// HandleListGroups returns all groups.
func (c *Coordinator) HandleListGroups(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	groups, err := c.ListGroups(status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

// HandleGetGroupTasks returns all tasks in a group.
func (c *Coordinator) HandleGetGroupTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	groupID := r.URL.Query().Get("id")
	if groupID == "" {
		http.Error(w, "id param required", http.StatusBadRequest)
		return
	}

	tasks, err := c.GetGroupTasks(groupID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// HandleShelleyBinary serves the shelley binary.
func (c *Coordinator) HandleShelleyBinary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, c.config.ShelleyBin)
}

// SyncConversationRequest contains conversation data to sync to main shelley DB
type SyncConversationRequest struct {
	Conversation struct {
		ConversationID string  `json:"conversation_id"`
		Slug           *string `json:"slug"`
		UserInitiated  int     `json:"user_initiated"` // SQLite outputs 0/1 for booleans
		CreatedAt      string  `json:"created_at"`
		UpdatedAt      string  `json:"updated_at"`
		Cwd            *string `json:"cwd"`
		Archived       int     `json:"archived"` // SQLite outputs 0/1 for booleans
	} `json:"conversation"`
	Messages []struct {
		MessageID      string  `json:"message_id"`
		ConversationID string  `json:"conversation_id"`
		SequenceID     int     `json:"sequence_id"`
		Type           string  `json:"type"`
		LLMData        *string `json:"llm_data"`
		UserData       *string `json:"user_data"`
		UsageData      *string `json:"usage_data"`
		CreatedAt      string  `json:"created_at"`
		DisplayData    *string `json:"display_data"`
	} `json:"messages"`
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
}

// HandleSyncConversation receives conversation data from workers and syncs to main shelley DB
func (c *Coordinator) HandleSyncConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if c.config.ShelleyDB == "" {
		http.Error(w, "Shelley DB not configured", http.StatusServiceUnavailable)
		return
	}

	var req SyncConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Open the main shelley DB
	shelleyDB, err := sql.Open("sqlite", c.config.ShelleyDB)
	if err != nil {
		http.Error(w, "Failed to open shelley DB: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer shelleyDB.Close()

	// Generate a slug for the conversation based on task ID
	slug := fmt.Sprintf("worker-%s-%s", req.WorkerID, req.TaskID[:8])

	// Insert conversation (use INSERT OR REPLACE to handle duplicates)
	_, err = shelleyDB.Exec(`INSERT OR REPLACE INTO conversations 
		(conversation_id, slug, user_initiated, created_at, updated_at, cwd, archived) 
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.Conversation.ConversationID,
		slug,
		false, // Mark as not user-initiated (autonomous)
		req.Conversation.CreatedAt,
		req.Conversation.UpdatedAt,
		req.Conversation.Cwd,
		req.Conversation.Archived,
	)
	if err != nil {
		http.Error(w, "Failed to insert conversation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Insert messages
	for _, msg := range req.Messages {
		_, err = shelleyDB.Exec(`INSERT OR REPLACE INTO messages 
			(message_id, conversation_id, sequence_id, type, llm_data, user_data, usage_data, created_at, display_data) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.MessageID,
			msg.ConversationID,
			msg.SequenceID,
			msg.Type,
			msg.LLMData,
			msg.UserData,
			msg.UsageData,
			msg.CreatedAt,
			msg.DisplayData,
		)
		if err != nil {
			log.Printf("Failed to insert message %s: %v", msg.MessageID, err)
		}
	}

	log.Printf("Synced conversation %s (%d messages) from worker %s", req.Conversation.ConversationID, len(req.Messages), req.WorkerID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "slug": slug})
}

// Artifact represents a file produced by a task.
type Artifact struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	WorkerID    string    `json:"worker_id"`
	Filename    string    `json:"filename"`
	Path        string    `json:"path"`
	URL         string    `json:"url"`
	SizeBytes   *int64    `json:"size_bytes,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ArtifactUploadRequest contains data for uploading an artifact.
type ArtifactUploadRequest struct {
	TaskID      string `json:"task_id"`
	WorkerID    string `json:"worker_id"`
	Filename    string `json:"filename"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}

// HandleUploadArtifact receives artifact metadata from workers.
func (c *Coordinator) HandleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "POST method required", "METHOD_NOT_ALLOWED")
		return
	}

	var req ArtifactUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "Invalid JSON body", "INVALID_JSON")
		return
	}

	if req.TaskID == "" || req.WorkerID == "" || req.Filename == "" {
		writeAPIError(w, http.StatusBadRequest, "task_id, worker_id, and filename are required", "MISSING_FIELD")
		return
	}

	// Generate artifact ID
	id := fmt.Sprintf("art-%d", time.Now().UnixNano())

	_, err := c.db.Exec(`INSERT INTO task_artifacts (id, task_id, worker_id, filename, path, url, size_bytes, content_type) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.TaskID, req.WorkerID, req.Filename, req.Path, req.URL, req.SizeBytes, req.ContentType)
	if err != nil {
		log.Printf("Failed to save artifact: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "Failed to save artifact", "DB_ERROR")
		return
	}

	log.Printf("Artifact saved: %s (%s) for task %s", req.Filename, id, req.TaskID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "ok"})
}

// HandleListArtifacts returns artifacts for a task.
func (c *Coordinator) HandleListArtifacts(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	taskID := r.URL.Query().Get("task")
	if taskID == "" {
		writeAPIError(w, http.StatusBadRequest, "task parameter is required", "MISSING_PARAM",
			"Include task ID: GET /api/artifacts?task=<task-id>")
		return
	}

	rows, err := c.db.Query(`SELECT id, task_id, worker_id, filename, path, url, size_bytes, content_type, created_at 
		FROM task_artifacts WHERE task_id = ? OR task_id LIKE ? ORDER BY created_at`, taskID, taskID+"%")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "DB_ERROR")
		return
	}
	defer rows.Close()

	var artifacts []Artifact
	for rows.Next() {
		var a Artifact
		var sizeBytes sql.NullInt64
		var contentType sql.NullString
		err := rows.Scan(&a.ID, &a.TaskID, &a.WorkerID, &a.Filename, &a.Path, &a.URL, &sizeBytes, &contentType, &a.CreatedAt)
		if err != nil {
			continue
		}
		if sizeBytes.Valid {
			a.SizeBytes = &sizeBytes.Int64
		}
		if contentType.Valid {
			a.ContentType = contentType.String
		}
		artifacts = append(artifacts, a)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifacts)
}

// HandleRegisterSSHKey allows workers to register their SSH public key for SSHFS access.
func (c *Coordinator) HandleRegisterSSHKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST method required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkerID string `json:"worker_id"`
		PubKey   string `json:"pub_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.WorkerID == "" || req.PubKey == "" {
		http.Error(w, "worker_id and pub_key required", http.StatusBadRequest)
		return
	}

	// Validate the public key format (basic check)
	if !strings.HasPrefix(req.PubKey, "ssh-") {
		http.Error(w, "Invalid SSH public key format", http.StatusBadRequest)
		return
	}

	// Store the public key in the database for cleanup on worker deletion
	c.db.Exec(`UPDATE workers SET ssh_pubkey = ? WHERE id = ?`, req.PubKey, req.WorkerID)

	// Add to exe.dev authorized_keys (primary location for exe.dev VMs)
	authKeysPath := "/exe.dev/etc/ssh/authorized_keys"
	if _, err := os.Stat(authKeysPath); os.IsNotExist(err) {
		// Fall back to standard location
		authKeysPath = filepath.Join(os.Getenv("HOME"), ".ssh", "authorized_keys")
	}

	// Read existing keys to avoid duplicates
	existingKeys, _ := os.ReadFile(authKeysPath)
	if strings.Contains(string(existingKeys), req.PubKey) {
		// Key already registered
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_registered"})
		return
	}

	// Append the new key with a comment for identification
	f, err := os.OpenFile(authKeysPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("Failed to open authorized_keys: %v", err)
		http.Error(w, "Failed to register key", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Ensure newline before adding key
	if len(existingKeys) > 0 && !strings.HasSuffix(string(existingKeys), "\n") {
		f.WriteString("\n")
	}
	if _, err := f.WriteString(req.PubKey + "\n"); err != nil {
		log.Printf("Failed to write SSH key: %v", err)
		http.Error(w, "Failed to register key", http.StatusInternalServerError)
		return
	}

	log.Printf("Registered SSH key for worker %s", req.WorkerID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

// HandleCheckConflicts checks if file patterns would conflict with running tasks.
// POST with {"owns_files": [...], "forbidden_files": [...]} to check before enqueueing.
func (c *Coordinator) HandleCheckConflicts(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST method required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID         string   `json:"task_id"`         // Optional: exclude this task from conflict check
		OwnsFiles      []string `json:"owns_files"`
		ForbiddenFiles []string `json:"forbidden_files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Generate temp ID if not provided
	taskID := req.TaskID
	if taskID == "" {
		taskID = "__check__"
	}

	conflicts := c.CheckFileConflicts(taskID, req.OwnsFiles, req.ForbiddenFiles)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"has_conflicts": len(conflicts) > 0,
		"conflicts":     conflicts,
	})
}

// HandleListRepos returns all shared repositories.
func (c *Coordinator) HandleListRepos(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	repos, err := c.ListSharedRepos()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "DB_ERROR")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repos)
}

// HandleGetRepo returns a specific shared repository.
func (c *Coordinator) HandleGetRepo(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		writeAPIError(w, http.StatusBadRequest, "id parameter is required", "MISSING_PARAM")
		return
	}

	repo, err := c.GetSharedRepo(repoID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Repository not found", "REPO_NOT_FOUND")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repo)
}

// HandleCreateRepo clones a repository to the shared filesystem.
func (c *Coordinator) HandleCreateRepo(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "POST method required", "METHOD_NOT_ALLOWED")
		return
	}

	var req struct {
		URL        string `json:"url"`
		BaseBranch string `json:"base_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "Invalid JSON body", "INVALID_JSON")
		return
	}
	if req.URL == "" {
		writeAPIError(w, http.StatusBadRequest, "url field is required", "MISSING_FIELD")
		return
	}

	repo, err := c.GetOrCreateSharedRepo(req.URL, req.BaseBranch)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "CLONE_FAILED")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repo)
}

// HandleListWorktrees returns all worktrees for a repository.
func (c *Coordinator) HandleListWorktrees(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	repoID := r.URL.Query().Get("repo")
	if repoID == "" {
		writeAPIError(w, http.StatusBadRequest, "repo parameter is required", "MISSING_PARAM")
		return
	}

	repo, err := c.GetSharedRepo(repoID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Repository not found", "REPO_NOT_FOUND")
		return
	}

	worktrees, err := c.ListWorktrees(repo)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "GIT_ERROR")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(worktrees)
}

// HandleFetchRepo fetches the latest changes for a repository.
func (c *Coordinator) HandleFetchRepo(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "POST method required", "METHOD_NOT_ALLOWED")
		return
	}

	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		writeAPIError(w, http.StatusBadRequest, "id parameter is required", "MISSING_PARAM")
		return
	}

	repo, err := c.GetSharedRepo(repoID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Repository not found", "REPO_NOT_FOUND")
		return
	}

	if err := c.fetchRepo(repo); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "FETCH_FAILED")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repo)
}

// HandleListRepoFiles returns all files in a repository.
func (c *Coordinator) HandleListRepoFiles(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		writeAPIError(w, http.StatusBadRequest, "id parameter is required", "MISSING_PARAM")
		return
	}

	// Get repo path
	repo, err := c.GetSharedRepo(repoID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "repository not found", "NOT_FOUND")
		return
	}

	// Use git ls-files to get tracked files
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = repo.Path
	output, err := cmd.Output()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "failed to list files: "+err.Error(), "GIT_ERROR")
		return
	}

	// Split output into lines and filter empty
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// HandleDeleteRepo removes a shared repository.
func (c *Coordinator) HandleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeAPIError(w, http.StatusMethodNotAllowed, "POST or DELETE method required", "METHOD_NOT_ALLOWED")
		return
	}

	repoID := r.URL.Query().Get("id")
	if repoID == "" {
		writeAPIError(w, http.StatusBadRequest, "id parameter is required", "MISSING_PARAM")
		return
	}

	if err := c.DeleteSharedRepo(repoID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error(), "DELETE_FAILED")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "id": repoID})
}
