package coordinator

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	_ "modernc.org/sqlite" // SQLite driver for sync
)

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
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}

	task, err := c.EnqueueTask(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		http.Error(w, "id param required", http.StatusBadRequest)
		return
	}

	task, err := c.GetTask(taskID)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// HandleListTasks returns all tasks.
func (c *Coordinator) HandleListTasks(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

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
func (c *Coordinator) HandleListWorkers(w http.ResponseWriter, r *http.Request) {
	if !c.CheckAuth(w, r) {
		return
	}

	workers, err := c.ListWorkers()
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
