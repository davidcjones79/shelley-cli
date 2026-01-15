package coordinator

import (
	"encoding/json"
	"net/http"
	"strconv"
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

	if err := c.ScaleWorkers(workers); err != nil {
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
