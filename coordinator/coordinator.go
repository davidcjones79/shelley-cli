// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Config holds coordinator configuration.
type Config struct {
	Port         int
	DBPath       string
	WorkerPrefix string
	MinWorkers   int
	MaxWorkers   int
	IdleTimeout  time.Duration
	ShelleyBin   string
	CoordHost    string
	APIToken     string
	GitLogging   bool
	GitToken      string // GitHub/GitLab token for HTTPS auth
	GitUser       string // Git username (default: git)
	ShelleyDB     string // Path to main shelley DB for syncing conversations
	InstallScript string // URL to install script (if set, uses this instead of scp binary)
	TaskTimeout   time.Duration // Max time for a task before it's considered stuck (default: 15 min)
	MaxRetries    int           // Max retries for failed tasks (default: 2)
}

// Task represents a unit of work.
type Task struct {
	ID             string     `json:"id"`
	Prompt         string     `json:"prompt"`
	Status         string     `json:"status"`
	Priority       int        `json:"priority"`
	WorkerID       *string    `json:"worker_id,omitempty"`
	Result         *string    `json:"result,omitempty"`
	Error          *string    `json:"error,omitempty"`
	RepoURL        *string    `json:"repo_url,omitempty"`
	BaseBranch     *string    `json:"base_branch,omitempty"`
	BranchName     *string    `json:"branch_name,omitempty"`
	CommitSHA      *string    `json:"commit_sha,omitempty"`
	PRURL          *string    `json:"pr_url,omitempty"`
	PRNumber       *int       `json:"pr_number,omitempty"`
	ConversationID *string    `json:"conversation_id,omitempty"` // shelley conversation for viewing
	Source         string     `json:"source"`                    // manual, autonomous, api
	GroupID        *string    `json:"group_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	AssignedAt     *time.Time `json:"assigned_at,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Worker represents a VM in the pool.
type Worker struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	CurrentTaskID   *string    `json:"current_task_id,omitempty"`
	ShelleyVersion  *string    `json:"shelley_version,omitempty"` // commit hash of shelley on worker
	CreatedAt       time.Time  `json:"created_at"`
	ReadyAt         *time.Time `json:"ready_at,omitempty"`         // when worker first reported version
	ProvisioningSec *int       `json:"provisioning_sec,omitempty"` // seconds from created to ready
	LastHeartbeat   *time.Time `json:"last_heartbeat,omitempty"`
	TasksCompleted  int        `json:"tasks_completed"`
	Error           *string    `json:"error,omitempty"` // error message if status is 'failed'
}

// TaskRequest contains parameters for creating a task.
type TaskRequest struct {
	ID         string `json:"id"`
	Prompt     string `json:"prompt"`
	Priority   int    `json:"priority"`
	RepoURL    string `json:"repo_url"`
	BaseBranch string `json:"base_branch"`
	GroupID    string `json:"group_id"` // Optional: assign to existing group
}

// TaskGroup represents a batch of related tasks.
type TaskGroup struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	RepoURL        *string    `json:"repo_url,omitempty"`
	BaseBranch     *string    `json:"base_branch,omitempty"`
	Status         string     `json:"status"`
	TasksTotal     int        `json:"tasks_total"`
	TasksCompleted int        `json:"tasks_completed"`
	TasksFailed    int        `json:"tasks_failed"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// GroupRequest contains parameters for creating a task group.
type GroupRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	RepoURL     string   `json:"repo_url"`
	BaseBranch  string   `json:"base_branch"`
	Prompts     []string `json:"prompts"` // Optional: create tasks immediately
}

// CompleteRequest contains the result of a completed task.
type CompleteRequest struct {
	TaskID         string `json:"task_id"`
	WorkerID       string `json:"worker_id"`
	Result         string `json:"result"`
	Error          string `json:"error"`
	CommitSHA      string `json:"commit_sha"`
	PRURL          string `json:"pr_url"`
	PRNumber       int    `json:"pr_number"`
	ConversationID string `json:"conversation_id"`
}

// Coordinator manages the worker pool and task queue.
type Coordinator struct {
	mu       sync.RWMutex
	db       *sql.DB
	config   Config
	logsDir  string
	shutdown chan struct{}
	draining bool // When true, workers should shut down after completing current task
}

// New creates a new Coordinator.
func New(config Config) (*Coordinator, error) {
	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Set busy timeout to 5 seconds to reduce SQLITE_BUSY errors
	db.Exec("PRAGMA busy_timeout = 5000")

	schema, _ := schemaFS.ReadFile("schema.sql")
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	logsDir := "logs"
	os.MkdirAll(logsDir, 0755)

	// Load or generate persistent API token
	if config.APIToken == "" {
		var savedToken string
		err := db.QueryRow(`SELECT value FROM settings WHERE key = 'api_token'`).Scan(&savedToken)
		if err == nil && savedToken != "" {
			config.APIToken = savedToken
			log.Printf("Loaded API token from database")
		} else {
			// Generate new token and save it
			b := make([]byte, 16)
			rand.Read(b)
			config.APIToken = hex.EncodeToString(b)
			db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('api_token', ?)`, config.APIToken)
			log.Printf("Generated and saved new API token")
		}
	} else {
		// Token provided via flag - save it for future restarts
		db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('api_token', ?)`, config.APIToken)
	}

	// Load or create persistent worker prefix so coordinator can find workers after restart
	// Format: wk-abc where abc is a random 3-char hex string
	if config.WorkerPrefix != "" {
		var savedPrefix string
		err := db.QueryRow(`SELECT value FROM settings WHERE key = 'worker_prefix'`).Scan(&savedPrefix)
		if err == nil && savedPrefix != "" {
			config.WorkerPrefix = savedPrefix
			log.Printf("Loaded worker prefix from database: %s", config.WorkerPrefix)
		} else {
			// Generate new prefix and persist it
			b := make([]byte, 2)
			rand.Read(b)
			config.WorkerPrefix = fmt.Sprintf("%s-%s", config.WorkerPrefix, hex.EncodeToString(b)[:3])
			db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('worker_prefix', ?)`, config.WorkerPrefix)
			log.Printf("Generated and saved new worker prefix: %s", config.WorkerPrefix)
		}
	}

	// Save API token to well-known file for easy scripting access
	tokenFile := "/tmp/coordinator-token"
	if err := os.WriteFile(tokenFile, []byte(config.APIToken), 0600); err != nil {
		log.Printf("Warning: failed to save token to %s: %v", tokenFile, err)
	} else {
		log.Printf("API token saved to %s", tokenFile)
	}

	return &Coordinator{
		db:       db,
		config:   config,
		logsDir:  logsDir,
		shutdown: make(chan struct{}),
	}, nil
}

// Config returns the coordinator's configuration.
func (c *Coordinator) Config() Config {
	return c.config
}

// StartBackgroundTasks starts periodic background tasks like cleanup.
func (c *Coordinator) StartBackgroundTasks() {
	// Run immediate cleanup on startup to handle orphans from previous run
	log.Printf("Running startup cleanup...")
	c.CleanupStaleWorkers()
	c.CleanupStuckTasks()
	
	go c.periodicCleanup()
}

// periodicCleanup runs cleanup tasks periodically.
func (c *Coordinator) periodicCleanup() {
	// Run initial cleanup after a short delay
	time.Sleep(30 * time.Second)
	c.CleanupStaleWorkers()
	c.CleanupStuckTasks()
	c.ensureMinWorkers()

	// Then run every minute for task checks, every 5 minutes for worker cleanup
	taskTicker := time.NewTicker(30 * time.Second)  // Check stuck tasks every 30s
	workerTicker := time.NewTicker(2 * time.Minute)  // Check stale workers every 2min
	minWorkerTicker := time.NewTicker(30 * time.Second) // Check min workers more frequently
	defer taskTicker.Stop()
	defer workerTicker.Stop()
	defer minWorkerTicker.Stop()

	for {
		select {
		case <-taskTicker.C:
			c.CleanupStuckTasks()
		case <-workerTicker.C:
			c.CleanupStaleWorkers()
		case <-minWorkerTicker.C:
			c.ensureMinWorkers()
		case <-c.shutdown:
			return
		}
	}
}

// ensureMinWorkers spawns workers if below minimum threshold.
func (c *Coordinator) ensureMinWorkers() {
	if c.config.MinWorkers <= 0 || c.draining {
		return
	}

	var idleCount, totalCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'idle'`).Scan(&idleCount)
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status IN ('starting', 'idle', 'busy')`).Scan(&totalCount)

	// Spawn workers if we're below minimum idle count
	if idleCount < c.config.MinWorkers && totalCount < c.config.MaxWorkers {
		needed := c.config.MinWorkers - idleCount
		if totalCount+needed > c.config.MaxWorkers {
			needed = c.config.MaxWorkers - totalCount
		}
		if needed > 0 {
			log.Printf("Pre-warming: spawning %d workers (idle: %d, min: %d)", needed, idleCount, c.config.MinWorkers)
			for i := 0; i < needed; i++ {
				go c.SpawnWorker()
			}
		}
	}
}

// CleanupStaleWorkers finds and deletes workers that are stale or orphaned.
// A worker is considered stale if:
// - Status is 'failed' or 'deleted' (cleanup DB records)
// - Status is 'starting' for more than 10 minutes (stuck)
// - Status is 'idle' for more than 30 minutes (idle timeout)
// - Exists on exe.dev but not in coordinator DB (orphaned)
func (c *Coordinator) CleanupStaleWorkers() {
	log.Printf("Running worker cleanup...")

	// Clean up failed/deleted workers from DB (older than 10 minutes)
	res, _ := c.db.Exec(`DELETE FROM workers WHERE status IN ('failed', 'deleted') AND created_at < datetime('now', '-10 minutes')`)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("Cleanup: removed %d failed/deleted worker records", n)
	}

	// Find stuck 'starting' workers (more than 10 minutes)
	rows, err := c.db.Query(`SELECT id FROM workers WHERE status = 'starting' AND created_at < datetime('now', '-10 minutes')`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var workerID string
			rows.Scan(&workerID)
			log.Printf("Cleanup: deleting stuck starting worker %s", workerID)
			c.DeleteWorker(workerID)
		}
	}

	// Find idle workers that have been idle too long (30 minutes)
	rows, err = c.db.Query(`SELECT id FROM workers WHERE status = 'idle' AND last_heartbeat < datetime('now', '-30 minutes')`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var workerID string
			rows.Scan(&workerID)
			log.Printf("Cleanup: deleting idle worker %s (idle timeout)", workerID)
			c.DeleteWorker(workerID)
		}
	}

	// Find orphaned VMs on exe.dev that aren't in our DB
	c.cleanupOrphanedVMs()

	// Find workers in DB whose VMs no longer exist (missing VMs)
	c.cleanupMissingVMs()
}

// cleanupOrphanedVMs finds worker VMs on exe.dev that aren't tracked in the coordinator DB.
func (c *Coordinator) cleanupOrphanedVMs() {
	// Get list of VMs from exe.dev
	cmd := exec.Command("ssh", "exe.dev", "ls")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Cleanup: failed to list VMs: %v", err)
		return
	}

	// Parse VM names that match our worker prefix
	prefix := c.config.WorkerPrefix + "-"
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		// Line format: "  • wk-xxx.exe.xyz - running (image)"
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "• "+prefix) {
			continue
		}

		// Extract worker ID
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		vmName := strings.TrimPrefix(parts[1], "• ")
		workerID := strings.TrimSuffix(vmName, ".exe.xyz")

		// Check if this worker is in our DB with active status
		var status string
		err := c.db.QueryRow(`SELECT status FROM workers WHERE id = ?`, workerID).Scan(&status)
		if err == sql.ErrNoRows {
			// Orphaned VM - not in our DB
			log.Printf("Cleanup: deleting orphaned VM %s (not in coordinator DB)", workerID)
			exec.Command("ssh", "exe.dev", "rm", workerID).Run()
		} else if status == "deleted" || status == "failed" {
			// VM still exists but marked as deleted/failed in DB
			log.Printf("Cleanup: deleting leftover VM %s (status: %s)", workerID, status)
			exec.Command("ssh", "exe.dev", "rm", workerID).Run()
		}
	}
}

// cleanupMissingVMs finds workers in the DB whose VMs no longer exist on exe.dev.
func (c *Coordinator) cleanupMissingVMs() {
	// Get list of VMs from exe.dev
	cmd := exec.Command("ssh", "exe.dev", "ls")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("Cleanup: failed to list VMs for missing check: %v", err)
		return
	}

	// Build set of existing VM names
	existingVMs := make(map[string]bool)
	prefix := c.config.WorkerPrefix + "-"
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "• ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			vmName := strings.TrimPrefix(parts[1], "• ")
			workerID := strings.TrimSuffix(vmName, ".exe.xyz")
			if strings.HasPrefix(workerID, prefix) {
				existingVMs[workerID] = true
			}
		}
	}

	// Find workers in DB that don't have corresponding VMs
	// Skip 'starting' workers as their VM might still be creating
	rows, err := c.db.Query(`SELECT id, status FROM workers WHERE status NOT IN ('deleted', 'failed', 'starting')`)
	if err != nil {
		return
	}
	
	// Collect missing workers first (can't modify DB while iterating)
	type missingWorker struct {
		id     string
		status string
	}
	var missing []missingWorker
	
	for rows.Next() {
		var workerID, status string
		rows.Scan(&workerID, &status)
		if !existingVMs[workerID] {
			missing = append(missing, missingWorker{workerID, status})
		}
	}
	rows.Close()
	
	// Now delete the missing workers
	for _, w := range missing {
		log.Printf("Cleanup: removing missing worker %s from DB (VM no longer exists)", w.id)
		c.LogEvent("worker.missing", "", w.id, map[string]interface{}{"previous_status": w.status})
		_, err := c.db.Exec(`DELETE FROM workers WHERE id = ?`, w.id)
		if err != nil {
			log.Printf("Cleanup: failed to delete worker %s: %v", w.id, err)
		}
	}
}

// CleanupStuckTasks finds and resets tasks that are stuck.
// A task is considered stuck if:
// - Status is 'assigned' or 'running' but assigned worker doesn't exist
// - Status is 'running' for longer than TaskTimeout (default 15 min)
func (c *Coordinator) CleanupStuckTasks() {
	log.Printf("Running stuck task cleanup...")

	// Get task timeout (default 15 minutes)
	taskTimeout := c.config.TaskTimeout
	if taskTimeout == 0 {
		taskTimeout = 15 * time.Minute
	}
	timeoutMinutes := int(taskTimeout.Minutes())

	// Get max retries (default 2)
	maxRetries := c.config.MaxRetries
	if maxRetries == 0 {
		maxRetries = 2
	}

	// Get list of active workers
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

	// Find tasks assigned to non-existent workers (orphaned)
	rows, err = c.db.Query(`SELECT id, worker_id, COALESCE(retry_count, 0) as retries FROM tasks WHERE status IN ('assigned', 'running') AND worker_id IS NOT NULL`)
	if err != nil {
		log.Printf("Cleanup: failed to query tasks: %v", err)
		return
	}

	type stuckTask struct {
		id       string
		workerID string
		retries  int
		reason   string
	}
	var stuck []stuckTask

	for rows.Next() {
		var id, workerID string
		var retries int
		rows.Scan(&id, &workerID, &retries)
		if !activeWorkers[workerID] {
			stuck = append(stuck, stuckTask{id, workerID, retries, "orphaned (worker missing)"})
		}
	}
	rows.Close()

	// Find tasks that have been running too long (timed out)
	query := fmt.Sprintf(`SELECT id, worker_id, COALESCE(retry_count, 0) as retries FROM tasks WHERE status = 'running' AND started_at < datetime('now', '-%d minutes')`, timeoutMinutes)
	rows, err = c.db.Query(query)
	if err == nil {
		for rows.Next() {
			var id, workerID string
			var retries int
			rows.Scan(&id, &workerID, &retries)
			// Don't double-count tasks already marked as orphaned
			alreadyStuck := false
			for _, s := range stuck {
				if s.id == id {
					alreadyStuck = true
					break
				}
			}
			if !alreadyStuck {
				stuck = append(stuck, stuckTask{id, workerID, retries, fmt.Sprintf("timeout (>%d min)", timeoutMinutes)})
			}
		}
		rows.Close()
	}

	// Reset or fail stuck tasks
	for _, t := range stuck {
		if t.retries >= maxRetries {
			// Max retries exceeded, mark as failed
			log.Printf("Cleanup: task %s failed after %d retries (%s)", t.id, t.retries, t.reason)
			c.db.Exec(`UPDATE tasks SET status = 'failed', error = ?, completed_at = datetime('now') WHERE id = ?`,
				fmt.Sprintf("Max retries exceeded: %s", t.reason), t.id)
		} else {
			// Reset to queued for retry
			log.Printf("Cleanup: resetting stuck task %s (%s), retry %d/%d", t.id, t.reason, t.retries+1, maxRetries)
			c.db.Exec(`UPDATE tasks SET status = 'queued', worker_id = NULL, assigned_at = NULL, started_at = NULL, retry_count = COALESCE(retry_count, 0) + 1 WHERE id = ?`, t.id)
		}

		// If worker was busy with this task, mark it as potentially dead
		if t.workerID != "" && activeWorkers[t.workerID] {
			c.db.Exec(`UPDATE workers SET status = 'idle' WHERE id = ? AND status = 'busy'`, t.workerID)
		}
	}

	if len(stuck) > 0 {
		log.Printf("Cleanup: processed %d stuck tasks", len(stuck))
	}
}

// LogEvent records an event to the database.
func (c *Coordinator) LogEvent(eventType, taskID, workerID string, details map[string]interface{}) {
	detailsJSON, _ := json.Marshal(details)
	c.db.Exec(`INSERT INTO events (event_type, task_id, worker_id, details) VALUES (?, ?, ?, ?)`,
		eventType, taskID, workerID, string(detailsJSON))
	log.Printf("[%s] task=%s worker=%s %v", eventType, taskID, workerID, details)
}

// EnqueueTask adds a task to the queue.
func (c *Coordinator) EnqueueTask(req TaskRequest) (*Task, error) {
	if req.ID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		req.ID = hex.EncodeToString(b)
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}

	// If task belongs to a group, inherit repo settings from the group
	var groupID interface{}
	if req.GroupID != "" {
		group, err := c.GetGroup(req.GroupID)
		if err != nil {
			return nil, fmt.Errorf("group not found: %w", err)
		}
		groupID = req.GroupID
		if req.RepoURL == "" && group.RepoURL != nil {
			req.RepoURL = *group.RepoURL
		}
		if req.BaseBranch == "main" && group.BaseBranch != nil {
			req.BaseBranch = *group.BaseBranch
		}
	}

	branchName := fmt.Sprintf("task-%s", req.ID)

	var repoURL, baseBranch, branch interface{}
	if req.RepoURL != "" {
		repoURL = req.RepoURL
		baseBranch = req.BaseBranch
		branch = branchName
	}

	_, err := c.db.Exec(`INSERT INTO tasks (id, prompt, priority, repo_url, base_branch, branch_name, group_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.Prompt, req.Priority, repoURL, baseBranch, branch, groupID)
	if err != nil {
		return nil, fmt.Errorf("enqueue task: %w", err)
	}

	// Update group task count
	if req.GroupID != "" {
		c.db.Exec(`UPDATE task_groups SET tasks_total = tasks_total + 1, status = 'running' WHERE id = ?`, req.GroupID)
	}

	c.LogEvent("task.queued", req.ID, "", map[string]interface{}{
		"priority": req.Priority,
		"repo_url": req.RepoURL,
		"branch":   branchName,
		"group_id": req.GroupID,
	})

	// Auto-scale: spawn a worker if there are queued tasks and no available workers
	go c.maybeSpawnWorker()

	return c.GetTask(req.ID)
}

// GetTask retrieves a task by ID.
func (c *Coordinator) GetTask(id string) (*Task, error) {
	var t Task
	var workerID, result, errorMsg sql.NullString
	var repoURL, baseBranch, branchName, commitSHA, prURL, groupID sql.NullString
	var conversationID, source sql.NullString
	var prNumber sql.NullInt64
	var assignedAt, startedAt, completedAt sql.NullTime

	err := c.db.QueryRow(`SELECT id, prompt, status, priority, worker_id, result, error, 
		repo_url, base_branch, branch_name, commit_sha, pr_url, pr_number,
		conversation_id, COALESCE(source, 'autonomous') as source, group_id,
		created_at, assigned_at, started_at, completed_at FROM tasks WHERE id = ?`, id).Scan(
		&t.ID, &t.Prompt, &t.Status, &t.Priority, &workerID, &result, &errorMsg,
		&repoURL, &baseBranch, &branchName, &commitSHA, &prURL, &prNumber,
		&conversationID, &source, &groupID,
		&t.CreatedAt, &assignedAt, &startedAt, &completedAt)
	if err != nil {
		return nil, err
	}
	
	if conversationID.Valid {
		t.ConversationID = &conversationID.String
	}
	t.Source = source.String
	if t.Source == "" {
		t.Source = "autonomous"
	}

	if workerID.Valid {
		t.WorkerID = &workerID.String
	}
	if result.Valid {
		t.Result = &result.String
	}
	if errorMsg.Valid {
		t.Error = &errorMsg.String
	}
	if repoURL.Valid {
		t.RepoURL = &repoURL.String
	}
	if baseBranch.Valid {
		t.BaseBranch = &baseBranch.String
	}
	if branchName.Valid {
		t.BranchName = &branchName.String
	}
	if commitSHA.Valid {
		t.CommitSHA = &commitSHA.String
	}
	if prURL.Valid {
		t.PRURL = &prURL.String
	}
	if prNumber.Valid {
		n := int(prNumber.Int64)
		t.PRNumber = &n
	}
	if groupID.Valid {
		t.GroupID = &groupID.String
	}
	if assignedAt.Valid {
		t.AssignedAt = &assignedAt.Time
	}
	if startedAt.Valid {
		t.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		t.CompletedAt = &completedAt.Time
	}

	return &t, nil
}

// GetNextTask assigns the next queued task to a worker.
func (c *Coordinator) GetNextTask(workerID string) (*Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if this is the first poll from a starting worker
	var currentStatus string
	c.db.QueryRow(`SELECT status FROM workers WHERE id = ?`, workerID).Scan(&currentStatus)
	
	if currentStatus == "" {
		// Worker not in DB - re-register it (e.g., after coordinator restart)
		log.Printf("Re-registering worker %s (not in DB)", workerID)
		c.db.Exec(`INSERT INTO workers (id, status, last_heartbeat) VALUES (?, 'idle', CURRENT_TIMESTAMP)`, workerID)
	} else if currentStatus == "starting" {
		// First poll from a new worker - it's now ready!
		log.Printf("Worker %s is now ready (first poll received)", workerID)
		c.db.Exec(`UPDATE workers SET status = 'idle', last_heartbeat = CURRENT_TIMESTAMP WHERE id = ?`, workerID)
		c.LogEvent("worker.ready", "", workerID, nil)
	} else {
		// Regular heartbeat update
		c.db.Exec(`UPDATE workers SET last_heartbeat = CURRENT_TIMESTAMP WHERE id = ? AND status != 'busy'`, workerID)
	}

	var taskID string
	err := c.db.QueryRow(`SELECT id FROM tasks WHERE status = 'queued' ORDER BY priority DESC, created_at ASC LIMIT 1`).Scan(&taskID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	_, err = c.db.Exec(`UPDATE tasks SET status = 'assigned', worker_id = ?, assigned_at = CURRENT_TIMESTAMP WHERE id = ?`,
		workerID, taskID)
	if err != nil {
		return nil, err
	}

	c.db.Exec(`UPDATE workers SET status = 'busy', current_task_id = ? WHERE id = ?`, taskID, workerID)
	c.LogEvent("task.assigned", taskID, workerID, nil)

	return c.GetTask(taskID)
}

// CompleteTask marks a task as completed with result.
func (c *Coordinator) CompleteTask(req CompleteRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := "completed"
	if req.Error != "" {
		status = "failed"
	}

	query := `UPDATE tasks SET status = ?, result = ?, error = ?, completed_at = CURRENT_TIMESTAMP`
	args := []interface{}{status, req.Result, req.Error}

	if req.CommitSHA != "" {
		query += `, commit_sha = ?`
		args = append(args, req.CommitSHA)
	}
	if req.PRURL != "" {
		query += `, pr_url = ?`
		args = append(args, req.PRURL)
	}
	if req.PRNumber > 0 {
		query += `, pr_number = ?`
		args = append(args, req.PRNumber)
	}
	if req.ConversationID != "" {
		query += `, conversation_id = ?`
		args = append(args, req.ConversationID)
	}

	query += ` WHERE id = ?`
	args = append(args, req.TaskID)

	_, err := c.db.Exec(query, args...)
	if err != nil {
		return err
	}

	c.db.Exec(`UPDATE workers SET status = 'idle', current_task_id = NULL, tasks_completed = tasks_completed + 1 WHERE id = ?`, req.WorkerID)

	c.LogEvent("task."+status, req.TaskID, req.WorkerID, map[string]interface{}{
		"result_length": len(req.Result),
		"commit_sha":    req.CommitSHA,
		"pr_url":        req.PRURL,
	})

	// If draining, delete the worker now that it's done
	if c.draining {
		go c.DeleteWorker(req.WorkerID)
	}

	// Update group status if task belongs to a group
	var groupID sql.NullString
	c.db.QueryRow(`SELECT group_id FROM tasks WHERE id = ?`, req.TaskID).Scan(&groupID)
	if groupID.Valid {
		c.updateGroupStatus(groupID.String)
	}

	if c.config.GitLogging {
		go c.gitLogTask(req.TaskID)
	}

	// Auto-scale: check if more workers needed for remaining queued tasks
	if !c.draining {
		go c.maybeSpawnWorker()
	}

	return nil
}

func (c *Coordinator) gitLogTask(taskID string) {
	task, err := c.GetTask(taskID)
	if err != nil {
		return
	}

	dateDir := filepath.Join(c.logsDir, time.Now().Format("2006-01-02"))
	os.MkdirAll(dateDir, 0755)

	logFile := filepath.Join(dateDir, taskID+".json")
	data, _ := json.MarshalIndent(task, "", "  ")
	os.WriteFile(logFile, data, 0644)

	exec.Command("git", "add", logFile).Run()
	msg := fmt.Sprintf("Task %s %s", taskID, task.Status)
	if task.WorkerID != nil {
		msg += fmt.Sprintf(" by %s", *task.WorkerID)
	}
	exec.Command("git", "commit", "-m", msg).Run()
}

// CreateGroup creates a new task group.
func (c *Coordinator) CreateGroup(req GroupRequest) (*TaskGroup, error) {
	if req.ID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		req.ID = hex.EncodeToString(b)
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("Group %s", req.ID[:8])
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}

	var repoURL, baseBranch, description interface{}
	if req.RepoURL != "" {
		repoURL = req.RepoURL
		baseBranch = req.BaseBranch
	}
	if req.Description != "" {
		description = req.Description
	}

	_, err := c.db.Exec(`INSERT INTO task_groups (id, name, description, repo_url, base_branch) VALUES (?, ?, ?, ?, ?)`,
		req.ID, req.Name, description, repoURL, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}

	c.LogEvent("group.created", "", "", map[string]interface{}{
		"group_id": req.ID,
		"name":     req.Name,
		"repo_url": req.RepoURL,
	})

	// If prompts provided, create tasks for each with retry logic
	for i, prompt := range req.Prompts {
		var lastErr error
		for retry := 0; retry < 3; retry++ {
			_, err := c.EnqueueTask(TaskRequest{
				Prompt:  prompt,
				GroupID: req.ID,
			})
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			// Only retry on SQLITE_BUSY errors
			if !strings.Contains(err.Error(), "SQLITE_BUSY") && !strings.Contains(err.Error(), "database is locked") {
				break
			}
			log.Printf("Retrying task %d for group %s (attempt %d): %v", i+1, req.ID, retry+1, err)
			time.Sleep(time.Duration(100*(1<<retry)) * time.Millisecond) // 100ms, 200ms, 400ms
		}
		if lastErr != nil {
			return nil, fmt.Errorf("create task %d for group: %w", i+1, lastErr)
		}
	}

	return c.GetGroup(req.ID)
}

// GetGroup retrieves a task group by ID.
func (c *Coordinator) GetGroup(id string) (*TaskGroup, error) {
	var g TaskGroup
	var description, repoURL, baseBranch sql.NullString
	var completedAt sql.NullTime

	err := c.db.QueryRow(`SELECT id, name, description, repo_url, base_branch, status, 
		tasks_total, tasks_completed, tasks_failed, created_at, completed_at 
		FROM task_groups WHERE id = ?`, id).Scan(
		&g.ID, &g.Name, &description, &repoURL, &baseBranch, &g.Status,
		&g.TasksTotal, &g.TasksCompleted, &g.TasksFailed, &g.CreatedAt, &completedAt)
	if err != nil {
		return nil, err
	}

	if description.Valid {
		g.Description = &description.String
	}
	if repoURL.Valid {
		g.RepoURL = &repoURL.String
	}
	if baseBranch.Valid {
		g.BaseBranch = &baseBranch.String
	}
	if completedAt.Valid {
		g.CompletedAt = &completedAt.Time
	}

	return &g, nil
}

// ListGroups returns all task groups.
func (c *Coordinator) ListGroups(status string, limit int) ([]TaskGroup, error) {
	query := `SELECT id FROM task_groups`
	args := []interface{}{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []TaskGroup
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if group, err := c.GetGroup(id); err == nil {
			groups = append(groups, *group)
		}
	}

	return groups, nil
}

// GetGroupTasks returns all tasks in a group.
func (c *Coordinator) GetGroupTasks(groupID string) ([]Task, error) {
	rows, err := c.db.Query(`SELECT id FROM tasks WHERE group_id = ? ORDER BY created_at ASC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if task, err := c.GetTask(id); err == nil {
			tasks = append(tasks, *task)
		}
	}

	return tasks, nil
}

// updateGroupStatus recalculates and updates a group's status.
func (c *Coordinator) updateGroupStatus(groupID string) {
	if groupID == "" {
		return
	}

	var total, completed, failed int
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE group_id = ?`, groupID).Scan(&total)
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE group_id = ? AND status = 'completed'`, groupID).Scan(&completed)
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE group_id = ? AND status = 'failed'`, groupID).Scan(&failed)

	status := "running"
	var completedAt interface{}
	if completed+failed >= total && total > 0 {
		if failed > 0 {
			status = "failed"
		} else {
			status = "completed"
		}
		completedAt = time.Now()
	} else if total == 0 {
		status = "pending"
	}

	c.db.Exec(`UPDATE task_groups SET tasks_total = ?, tasks_completed = ?, tasks_failed = ?, status = ?, completed_at = ? WHERE id = ?`,
		total, completed, failed, status, completedAt, groupID)
}

// maybeSpawnWorker checks if we need to spawn a worker for queued tasks.
// It spawns a worker if there are queued tasks and no idle/starting workers available.
func (c *Coordinator) maybeSpawnWorker() {
	c.mu.Lock()
	if c.draining {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Check if there are queued tasks
	var queuedCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'queued'`).Scan(&queuedCount)
	if queuedCount == 0 {
		return
	}

	// Check current worker count
	var activeWorkers, idleWorkers, startingWorkers int
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status IN ('idle', 'busy', 'starting')`).Scan(&activeWorkers)
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'idle'`).Scan(&idleWorkers)
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'starting'`).Scan(&startingWorkers)

	// If there are idle or starting workers, they'll pick up the task
	if idleWorkers > 0 || startingWorkers > 0 {
		return
	}

	// Check if we're at max workers
	if activeWorkers >= c.config.MaxWorkers {
		log.Printf("Auto-scale: at max workers (%d), not spawning", c.config.MaxWorkers)
		return
	}

	// Spawn a new worker
	log.Printf("Auto-scale: %d queued tasks, %d active workers, spawning new worker", queuedCount, activeWorkers)
	_, err := c.SpawnWorker()
	if err != nil {
		log.Printf("Auto-scale: failed to spawn worker: %v", err)
	}
}

// SpawnWorker creates a new worker VM.
func (c *Coordinator) SpawnWorker() (*Worker, error) {
	b := make([]byte, 6)
	rand.Read(b)
	// exe.dev rejects VM names that are all digits, so we add an 'x' prefix
	// to ensure the hex suffix always contains at least one letter
	workerID := fmt.Sprintf("%s-x%s", c.config.WorkerPrefix, hex.EncodeToString(b))

	_, err := c.db.Exec(`INSERT INTO workers (id, status) VALUES (?, 'starting')`, workerID)
	if err != nil {
		return nil, err
	}

	c.LogEvent("worker.starting", "", workerID, nil)
	go c.setupWorker(workerID)

	return c.GetWorker(workerID)
}

// sshToWorker creates an exec.Command to SSH into a worker via exe.dev proxy
// Since exe.dev VMs can't SSH directly to each other, we go through: ssh exe.dev ssh <vmname> <cmd...>
// The command args must be quoted together because exe.dev's SSH command parses flags from all args
func sshToWorker(workerID string, args ...string) *exec.Cmd {
	// Join all args into a single quoted command string
	// The exe.dev ssh command needs to receive "ssh <vmname> '<cmd>'" as a single argument
	remoteCmd := strings.Join(args, " ")
	// Escape single quotes in the command by replacing ' with '"'"'
	remoteCmd = strings.ReplaceAll(remoteCmd, "'", "'\"'\"'")
	// The full command string that ssh receives should be: ssh <vmname> '<cmd>'
	// We need to pass this as a single argument to exe.dev, so we wrap it in double quotes
	sshCmd := fmt.Sprintf(`ssh %s '%s'`, workerID, remoteCmd)
	return exec.Command("ssh", "exe.dev", sshCmd)
}

// runSSHWithTimeout runs an SSH command with a timeout, preventing hangs.
func runSSHWithTimeout(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", args...)
	return cmd.CombinedOutput()
}

// sshWithRetry runs an SSH command with timeout and exponential backoff retry.
func sshWithRetry(timeout time.Duration, maxRetries int, args ...string) ([]byte, error) {
	var lastErr error
	var out []byte
	
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, ...
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			log.Printf("SSH retry %d/%d after %v", attempt, maxRetries, backoff)
			time.Sleep(backoff)
		}
		
		out, lastErr = runSSHWithTimeout(timeout, args...)
		if lastErr == nil {
			return out, nil
		}
		log.Printf("SSH attempt %d failed: %v", attempt+1, lastErr)
	}
	
	return out, lastErr
}

func (c *Coordinator) setupWorker(workerID string) {
	workerHost := workerID + ".exe.xyz" // Still used for some operations

	log.Printf("Spawning worker VM: %s", workerID)
	log.Printf("Running command: ssh exe.dev new --name=%s --no-email --json", workerID)
	// Use retry for VM creation - can fail transiently
	output, err := sshWithRetry(60*time.Second, 2, "exe.dev", "new", "--name="+workerID, "--no-email", "--json")
	log.Printf("Spawn output for %s: %s", workerID, string(output))
	if err != nil {
		log.Printf("Failed to spawn %s: %v\n%s", workerID, err, output)
		c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
		c.LogEvent("worker.failed", "", workerID, map[string]interface{}{"error": err.Error()})
		return
	}

	log.Printf("Waiting for %s SSH...", workerID)
	for i := 0; i < 60; i++ {
		time.Sleep(3 * time.Second)
		// Use retry for SSH check
		out, err := sshWithRetry(15*time.Second, 1, "exe.dev", fmt.Sprintf("ssh %s 'echo ready'", workerID))
		if err == nil && strings.Contains(string(out), "ready") {
			break
		}
		if i == 59 {
			log.Printf("Timeout waiting for %s", workerID)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
	}

	log.Printf("Installing shelley-cli on %s...", workerID)

	// Determine install method: config > env var > default (https)
	installScript := c.config.InstallScript
	if installScript == "" {
		installScript = os.Getenv("SHELLEY_INSTALL_SCRIPT")
	}
	if installScript == "" {
		installScript = "https" // Default: workers download binary from coordinator via HTTPS
	}

	// Use HTTPS download (default), SCP, or custom install script URL
	if installScript != "scp" && installScript != "https" {
		log.Printf("Running install script on %s...", workerID)
		installCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'curl -fsSL %s | bash'", workerID, installScript))
		if out, err := installCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to run install script on %s: %v\n%s", workerID, err, out)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
		log.Printf("Install script completed on %s", workerID)
		// Write config for LLM gateway
		exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'mkdir -p .config/shelley'", workerID)).Run()
		configJSON := `{"llm_gateway": "http://169.254.169.254/gateway/llm", "default_model": "claude-sonnet-4.5"}`
		configCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'cat > .config/shelley/shelley.json << EOF\n%s\nEOF'", workerID, configJSON))
		configCmd.Run()
	} else {
		// Create directories on worker
		mkdirCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'mkdir -p .local/bin .config/shelley'", workerID))
		if out, err := mkdirCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to create directories on %s: %v\nOutput: %s", workerID, err, string(out))
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}

		// Download shelley binary from coordinator host
		// The coordinator serves the binary on its HTTP port at /api/shelley-bin
		// exe.dev proxies HTTPS to the coordinator port
		log.Printf("Downloading shelley binary to %s from coordinator...", workerID)
		downloadURL := fmt.Sprintf("https://%s:%d/api/shelley-bin?token=%s", c.config.CoordHost, c.config.Port, c.config.APIToken)
		downloadCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'curl -fsSL \"%s\" -o .local/bin/shelley'", workerID, downloadURL))
		if out, err := downloadCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to download shelley to %s: %v\n%s", workerID, err, out)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
		// Make it executable
		chmodCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'chmod +x .local/bin/shelley'", workerID))
		chmodCmd.Run()

		// Write config using tee to avoid shell quoting issues with redirects
		configJSON := `{"llm_gateway": "http://169.254.169.254/gateway/llm", "default_model": "claude-sonnet-4.5"}`
		configCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'cat > .config/shelley/shelley.json << EOF\n%s\nEOF'", workerID, configJSON))
		configCmd.Run()
	}

	// Configure git credentials if token provided
	if c.config.GitToken != "" {
		gitUser := c.config.GitUser
		if gitUser == "" {
			gitUser = "git"
		}
		// Configure git credential helper to use token
		gitSetupCmds := []string{
			"git config --global credential.helper store",
			"git config --global user.email shelley-worker@exe.dev",
			fmt.Sprintf("git config --global user.name 'Shelley Worker (%s)'", workerID),
		}
		for _, cmd := range gitSetupCmds {
			exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s '%s'", workerID, cmd)).Run()
		}
		// Store credentials for GitHub
		credentials := fmt.Sprintf("https://%s:%s@github.com", gitUser, c.config.GitToken)
		credCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'echo \"%s\" > .git-credentials && chmod 600 .git-credentials'", workerID, credentials))
		credCmd.Run()
	}

	c.startWorkerLoop(workerID, workerHost)
}

func (c *Coordinator) startWorkerLoop(workerID, workerHost string) {
	log.Printf("Starting worker loop on %s...", workerID)
	
	// The worker agent script polls for tasks and runs shelley chat directly
	// (No shelley serve needed - simplifies worker setup)
	pollScript := fmt.Sprintf(`#!/bin/bash
set -e
export PATH="$HOME/.local/bin:$PATH"
COORD="https://%s:%d"
WORKER_ID="%s"
API_TOKEN="%s"
SHELLEY_DB="/tmp/shelley-worker.db"
IDLE_COUNT=0
MAX_IDLE=360
WORKDIR="$HOME/workspaces"

# Get shelley version for reporting
SHELLEY_VERSION=$(shelley version 2>/dev/null | jq -r '.commit // "unknown"' || echo "unknown")

mkdir -p "$WORKDIR"
echo "Worker loop started (shelley version: $SHELLEY_VERSION)"

while true; do
    RESPONSE=$(curl -s -H "X-Coordinator-Token: $API_TOKEN" "$COORD/api/next-task?worker=$WORKER_ID&version=$SHELLEY_VERSION")
    TASK_ID=$(echo "$RESPONSE" | jq -r '.id // empty')
    
    if [ -n "$TASK_ID" ]; then
        IDLE_COUNT=0
        PROMPT=$(echo "$RESPONSE" | jq -r '.prompt')
        REPO_URL=$(echo "$RESPONSE" | jq -r '.repo_url // empty')
        BASE_BRANCH=$(echo "$RESPONSE" | jq -r '.base_branch // "main"')
        BRANCH_NAME=$(echo "$RESPONSE" | jq -r '.branch_name // empty')
        
        echo "=== Task $TASK_ID ==="
        echo "Prompt: $PROMPT"
        
        TASK_DIR="$WORKDIR/$TASK_ID"
        COMMIT_SHA=""
        ERROR=""
        CWD="$HOME"
        
        # If repo URL provided, clone and setup git
        if [ -n "$REPO_URL" ]; then
            echo "Cloning $REPO_URL..."
            rm -rf "$TASK_DIR"
            
            if ! git clone --depth=50 --branch="$BASE_BRANCH" "$REPO_URL" "$TASK_DIR" 2>&1; then
                ERROR="Failed to clone repository"
                curl -s -X POST "$COORD/api/complete" \
                    -H "Content-Type: application/json" \
                    -H "X-Coordinator-Token: $API_TOKEN" \
                    -d "$(jq -n --arg tid "$TASK_ID" --arg wid "$WORKER_ID" --arg err "$ERROR" \
                        '{task_id: $tid, worker_id: $wid, error: $err}')"
                continue
            fi
            
            CWD="$TASK_DIR"
            cd "$TASK_DIR"
            
            if [ -n "$BRANCH_NAME" ]; then
                git checkout -b "$BRANCH_NAME"
            fi
            
            git config user.email "shelley-worker@exe.dev"
            git config user.name "Shelley Worker ($WORKER_ID)"
        else
            TASK_DIR="$WORKDIR/$TASK_ID"
            mkdir -p "$TASK_DIR"
            CWD="$TASK_DIR"
        fi
        
        echo "Starting autonomous shelley chat..."
        echo "View progress at: https://${WORKER_ID}.exe.xyz:8000/"
        
        # Mark task as started (running)
        curl -s -X POST -H "X-Coordinator-Token: $API_TOKEN" "$COORD/api/task-start?task=$TASK_ID&worker=$WORKER_ID" || true
        
        # Start background heartbeat to keep worker alive during long tasks
        (
            while true; do
                sleep 30
                curl -s -H "X-Coordinator-Token: $API_TOKEN" "$COORD/api/heartbeat?worker=$WORKER_ID&task=$TASK_ID" > /dev/null 2>&1 || true
            done
        ) &
        HEARTBEAT_PID=$!
        
        # Run shelley chat with full autonomy (-yes auto-approves all tool calls)
        # Uses the same DB as shelley serve, so conversation is viewable in real-time
        cd "$CWD"
        OUTPUT=$(shelley -db "$SHELLEY_DB" -config ~/.config/shelley/shelley.json \
            chat -yes -prompt "$PROMPT" 2>&1) || true
        
        # Stop background heartbeat
        kill $HEARTBEAT_PID 2>/dev/null || true
        
        # Extract conversation ID from output (format: [Conversation: xxx])
        CONV_ID=$(echo "$OUTPUT" | grep -oP '\[Conversation: \K[^\]]+' | tail -1)
        if [ -z "$CONV_ID" ]; then
            # Fallback: get most recent from DB
            CONV_ID=$(sqlite3 "$SHELLEY_DB" "SELECT conversation_id FROM conversations ORDER BY created_at DESC LIMIT 1" 2>/dev/null)
        fi
        
        echo "Task execution complete (conversation: $CONV_ID)"
        
        # Sync conversation to main shelley DB for viewing in main UI
        if [ -n "$CONV_ID" ]; then
            echo "Syncing conversation to main shelley..."
            # Export conversation and messages as JSON to temp files (avoids argument too long errors)
            SYNC_TMP=$(mktemp)
            sqlite3 -json "$SHELLEY_DB" "SELECT * FROM conversations WHERE conversation_id='$CONV_ID'" 2>/dev/null | jq '.[0]' > "$SYNC_TMP.conv"
            sqlite3 -json "$SHELLEY_DB" "SELECT * FROM messages WHERE conversation_id='$CONV_ID' ORDER BY sequence_id" 2>/dev/null > "$SYNC_TMP.msgs"
            
            if [ -s "$SYNC_TMP.conv" ] && [ "$(cat $SYNC_TMP.conv)" != "null" ]; then
                # Build payload using files instead of command-line args
                jq -n --slurpfile conv "$SYNC_TMP.conv" --slurpfile msgs "$SYNC_TMP.msgs" \
                    --arg tid "$TASK_ID" --arg wid "$WORKER_ID" \
                    '{conversation: $conv[0], messages: $msgs[0], task_id: $tid, worker_id: $wid}' > "$SYNC_TMP.payload"
                curl -s -X POST "$COORD/api/sync-conversation" \
                    -H "Content-Type: application/json" \
                    -H "X-Coordinator-Token: $API_TOKEN" \
                    -d @"$SYNC_TMP.payload" || echo "Sync failed (non-fatal)"
            fi
            rm -f "$SYNC_TMP" "$SYNC_TMP.conv" "$SYNC_TMP.msgs" "$SYNC_TMP.payload" 2>/dev/null
        fi
        
        # If we have a repo, commit and push changes
        if [ -n "$REPO_URL" ] && [ -n "$BRANCH_NAME" ]; then
            cd "$TASK_DIR"
            
            if [ -n "$(git status --porcelain)" ]; then
                echo "Committing changes..."
                git add -A
                git commit -m "Task $TASK_ID: $PROMPT" -m "Automated commit by Shelley Coordinator" || true
                COMMIT_SHA=$(git rev-parse HEAD)
                
                echo "Pushing branch $BRANCH_NAME..."
                if git push -u origin "$BRANCH_NAME" 2>&1; then
                    echo "Push successful: $COMMIT_SHA"
                else
                    ERROR="Failed to push branch"
                fi
            else
                echo "No changes to commit"
            fi
        fi
        
        # Report completion with conversation ID for reference
        curl -s -X POST "$COORD/api/complete" \
            -H "Content-Type: application/json" \
            -H "X-Coordinator-Token: $API_TOKEN" \
            -d "$(jq -n --arg tid "$TASK_ID" --arg wid "$WORKER_ID" \
                       --arg cid "$CONV_ID" --arg sha "$COMMIT_SHA" --arg err "$ERROR" \
                '{task_id: $tid, worker_id: $wid, conversation_id: $cid, commit_sha: $sha, error: $err}')"
        
        echo "Task $TASK_ID completed"
    else
        IDLE_COUNT=$((IDLE_COUNT + 1))
        if [ $IDLE_COUNT -ge $MAX_IDLE ]; then
            echo "Idle timeout, shutting down"
            curl -s -X POST -H "X-Coordinator-Token: $API_TOKEN" "$COORD/api/worker-shutdown?worker=$WORKER_ID"
            exit 0
        fi
        sleep 5
    fi
done
`, c.config.CoordHost, c.config.Port, workerID, c.config.APIToken)

	// Write the worker loop script using base64 encoding to avoid shell quoting issues
	scriptB64 := base64.StdEncoding.EncodeToString([]byte(pollScript))
	scriptCmd := exec.Command("ssh", "exe.dev",
		fmt.Sprintf("ssh %s 'echo %s | base64 -d > /tmp/worker-loop.sh'", workerID, scriptB64))
	if out, err := scriptCmd.CombinedOutput(); err != nil {
		log.Printf("Failed to write worker loop script on %s: %v\nOutput: %s", workerID, err, string(out))
	}

	// Make script executable
	chmodCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'chmod +x /tmp/worker-loop.sh'", workerID))
	chmodCmd.Run()

	// Start the worker loop in background
	go func() {
		startCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'nohup /tmp/worker-loop.sh > /tmp/worker.log 2>&1 &'", workerID))
		startCmd.Run()
	}()

	// Keep status as 'starting' - will change to 'idle' when worker first polls and reports version
	log.Printf("Worker %s setup complete, waiting for first poll...", workerID)
}

// GetWorker retrieves a worker by ID.
func (c *Coordinator) GetWorker(id string) (*Worker, error) {
	var w Worker
	var currentTaskID, shelleyVersion sql.NullString
	var readyAt, lastHeartbeat sql.NullTime

	err := c.db.QueryRow(`SELECT id, status, current_task_id, shelley_version, created_at, ready_at, last_heartbeat, tasks_completed 
		FROM workers WHERE id = ?`, id).Scan(
		&w.ID, &w.Status, &currentTaskID, &shelleyVersion, &w.CreatedAt, &readyAt, &lastHeartbeat, &w.TasksCompleted)
	if err != nil {
		return nil, err
	}

	if currentTaskID.Valid {
		w.CurrentTaskID = &currentTaskID.String
	}
	if shelleyVersion.Valid {
		w.ShelleyVersion = &shelleyVersion.String
	}
	if readyAt.Valid {
		w.ReadyAt = &readyAt.Time
		provSec := int(readyAt.Time.Sub(w.CreatedAt).Seconds())
		w.ProvisioningSec = &provSec
	}
	if lastHeartbeat.Valid {
		w.LastHeartbeat = &lastHeartbeat.Time
	}

	return &w, nil
}

// DeleteWorker removes a worker VM.
func (c *Coordinator) DeleteWorker(workerID string) error {
	log.Printf("Deleting worker: %s", workerID)
	// Use timeout to prevent SSH from hanging
	out, err := runSSHWithTimeout(30*time.Second, "exe.dev", "rm", workerID)
	if err != nil {
		log.Printf("Warning: failed to delete VM %s: %v (output: %s)", workerID, err, string(out))
		// Continue anyway - remove from DB even if VM deletion failed
		// The cleanup routine will catch orphaned VMs later
	}

	// Remove from DB entirely (not just mark as deleted)
	c.db.Exec(`DELETE FROM workers WHERE id = ?`, workerID)
	c.LogEvent("worker.deleted", "", workerID, nil)
	return nil
}

// ScaleWorkers ensures the desired number of workers are running.
func (c *Coordinator) ScaleWorkers(desired int) error {
	var activeCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status IN ('starting', 'idle', 'busy')`).Scan(&activeCount)

	for activeCount < desired {
		_, err := c.SpawnWorker()
		if err != nil {
			return err
		}
		activeCount++
	}
	return nil
}

// DrainWorkers marks all workers for shutdown after completing current tasks.
// Idle workers are deleted immediately, busy workers complete their task first.
func (c *Coordinator) DrainWorkers() (int, int) {
	c.mu.Lock()
	c.draining = true
	c.mu.Unlock()

	// Delete idle and starting workers immediately
	rows, _ := c.db.Query(`SELECT id FROM workers WHERE status IN ('idle', 'starting')`)
	var idleDeleted int
	var toDelete []string
	if rows != nil {
		for rows.Next() {
			var id string
			rows.Scan(&id)
			toDelete = append(toDelete, id)
		}
		rows.Close()
	}
	
	// Delete workers outside of query loop
	for _, id := range toDelete {
		c.DeleteWorker(id)
		idleDeleted++
	}

	// Count busy workers that will drain after completing their task
	var busyCount int
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'busy'`).Scan(&busyCount)

	c.LogEvent("workers.draining", "", "", map[string]interface{}{
		"idle_deleted": idleDeleted,
		"busy_draining": busyCount,
	})

	return idleDeleted, busyCount
}

// IsDraining returns whether the coordinator is in drain mode.
func (c *Coordinator) IsDraining() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.draining
}

// StopDraining cancels drain mode.
func (c *Coordinator) StopDraining() {
	c.mu.Lock()
	c.draining = false
	c.mu.Unlock()
	c.LogEvent("workers.drain_cancelled", "", "", nil)
}

// GetStats returns current statistics.
func (c *Coordinator) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	var queuedTasks, runningTasks, completedTasks, failedTasks int
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'queued'`).Scan(&queuedTasks)
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status IN ('assigned', 'running')`).Scan(&runningTasks)
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'completed'`).Scan(&completedTasks)
	c.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'failed'`).Scan(&failedTasks)

	var idleWorkers, busyWorkers, totalWorkers int
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'idle'`).Scan(&idleWorkers)
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status = 'busy'`).Scan(&busyWorkers)
	c.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE status IN ('starting', 'idle', 'busy')`).Scan(&totalWorkers)

	stats["tasks"] = map[string]int{
		"queued":    queuedTasks,
		"running":   runningTasks,
		"completed": completedTasks,
		"failed":    failedTasks,
	}
	stats["workers"] = map[string]int{
		"idle":  idleWorkers,
		"busy":  busyWorkers,
		"total": totalWorkers,
	}

	return stats
}

// ListTasks returns tasks with optional filtering.
func (c *Coordinator) ListTasks(status string, limit int) ([]Task, error) {
	query := `SELECT id FROM tasks`
	args := []interface{}{}

	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if task, err := c.GetTask(id); err == nil {
			tasks = append(tasks, *task)
		}
	}

	return tasks, nil
}

// ListWorkers returns all non-deleted workers.
// ListWorkers returns all workers. If showFailed is false, failed workers are excluded.
func (c *Coordinator) ListWorkers(showFailed bool) ([]Worker, error) {
	query := `SELECT id, status, current_task_id, shelley_version, created_at, ready_at, last_heartbeat, tasks_completed 
		FROM workers WHERE status != 'deleted'`
	if !showFailed {
		query = `SELECT id, status, current_task_id, shelley_version, created_at, ready_at, last_heartbeat, tasks_completed 
			FROM workers WHERE status NOT IN ('deleted', 'failed')`
	}
	query += " ORDER BY created_at DESC"
	
	rows, err := c.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []Worker
	for rows.Next() {
		var w Worker
		var currentTaskID, shelleyVersion sql.NullString
		var readyAtTime, lastHeartbeatTime sql.NullTime
		rows.Scan(&w.ID, &w.Status, &currentTaskID, &shelleyVersion, &w.CreatedAt, &readyAtTime, &lastHeartbeatTime, &w.TasksCompleted)
		if shelleyVersion.Valid {
			w.ShelleyVersion = &shelleyVersion.String
		}
		if readyAtTime.Valid {
			w.ReadyAt = &readyAtTime.Time
			// Calculate provisioning time in seconds
			provSec := int(readyAtTime.Time.Sub(w.CreatedAt).Seconds())
			w.ProvisioningSec = &provSec
		}
		if lastHeartbeatTime.Valid {
			w.LastHeartbeat = &lastHeartbeatTime.Time
		}
		
		// For failed workers, get error from events table
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
		
		workers = append(workers, w)
	}

	return workers, nil
}
