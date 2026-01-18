// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"bytes"
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
	"text/template"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

//go:embed templates/worker_context.md
var workerContextTemplate string

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
	// Tailscale configuration for private network between coordinator and workers
	TailscaleAuthKey string // Tailscale auth key for workers to join the network
	TailscaleIP      string // Coordinator's Tailscale IP (auto-detected if empty)
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
	WorktreePath   *string    `json:"worktree_path,omitempty"`   // path to git worktree (shared fs)
	CommitSHA      *string    `json:"commit_sha,omitempty"`
	PRURL          *string    `json:"pr_url,omitempty"`
	PRNumber       *int       `json:"pr_number,omitempty"`
	ConversationID *string    `json:"conversation_id,omitempty"` // shelley conversation for viewing
	Source         string     `json:"source"`                    // manual, autonomous, api
	GroupID        *string    `json:"group_id,omitempty"`
	InputDir       *string    `json:"input_dir,omitempty"`       // path to staged input files
	OwnsFiles      []string   `json:"owns_files,omitempty"`      // glob patterns this task may modify
	ForbiddenFiles []string   `json:"forbidden_files,omitempty"` // glob patterns this task must not touch
	SkillContext   *string    `json:"skill_context,omitempty"`   // system-level context from group
	WorkerContext  *string    `json:"worker_context,omitempty"`  // rendered worker context template
	CreatedAt      time.Time  `json:"created_at"`
	AssignedAt     *time.Time `json:"assigned_at,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Heartbeat health thresholds for detecting stale/dead workers
const (
	HeartbeatWarningAge = 60 * time.Second   // Show warning in UI (yellow)
	HeartbeatStaleAge   = 120 * time.Second  // Mark as unhealthy (orange)
	HeartbeatDeadAge    = 300 * time.Second  // Auto-replace worker (red)
)

// Worker represents a VM in the pool.
type Worker struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	CurrentTaskID    *string    `json:"current_task_id,omitempty"`
	ShelleyVersion   *string    `json:"shelley_version,omitempty"` // commit hash of shelley on worker
	CreatedAt        time.Time  `json:"created_at"`
	ReadyAt          *time.Time `json:"ready_at,omitempty"`         // when worker first reported version
	ProvisioningSec  *int       `json:"provisioning_sec,omitempty"` // seconds from created to ready
	LastHeartbeat    *time.Time `json:"last_heartbeat,omitempty"`
	TasksCompleted   int        `json:"tasks_completed"`
	Error            *string    `json:"error,omitempty"`            // error message if status is 'failed'
	Health           string     `json:"health,omitempty"`           // healthy, warning, unhealthy, dead
	HeartbeatAgeSec  *int       `json:"heartbeat_age_sec,omitempty"` // seconds since last heartbeat
	HeartbeatWarning bool       `json:"heartbeat_warning"`          // true if heartbeat is stale
}

// TaskRequest contains parameters for creating a task.
// InputFile specifies a file to stage for a task.
type InputFile struct {
	Path    string `json:"path"`    // Path relative to ~/shared/source/<task-id>/
	Content string `json:"content"` // File content (base64 encoded if binary)
	Source  string `json:"source"`  // Alternative: source path on coordinator (relative to ~/shared/source/)
}

type TaskRequest struct {
	ID             string      `json:"id"`
	Prompt         string      `json:"prompt"`
	Priority       int         `json:"priority"`
	RepoURL        string      `json:"repo_url"`
	BaseBranch     string      `json:"base_branch"`
	GroupID        string      `json:"group_id"`        // Optional: assign to existing group
	InputFiles     []InputFile `json:"input_files"`     // Optional: files to stage in ~/shared/source/<task-id>/
	UseWorktree    bool        `json:"use_worktree"`    // Use shared repo with git worktree (default: true if Tailscale enabled)
	WorktreePath   string      `json:"worktree_path"`   // Set by coordinator: path to worktree
	OwnsFiles      []string    `json:"owns_files"`      // Glob patterns this task may modify
	ForbiddenFiles []string    `json:"forbidden_files"` // Glob patterns this task must not touch
}

// TaskGroup represents a batch of related tasks.
type TaskGroup struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	RepoURL        *string    `json:"repo_url,omitempty"`
	BaseBranch     *string    `json:"base_branch,omitempty"`
	SkillContext   *string    `json:"skill_context,omitempty"` // System-level context for workers
	Status         string     `json:"status"`
	TasksTotal     int        `json:"tasks_total"`
	TasksCompleted int        `json:"tasks_completed"`
	TasksFailed    int        `json:"tasks_failed"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// GroupRequest contains parameters for creating a task group.
// GroupTaskSpec specifies a task to create within a group.
// Either Prompt (simple string) or TaskRequest (detailed spec) should be set.
type GroupTaskSpec struct {
	Prompt         string   `json:"prompt"`          // Simple prompt string
	OwnsFiles      []string `json:"owns_files"`      // Optional: files this task may modify
	ForbiddenFiles []string `json:"forbidden_files"` // Optional: files this task must not touch
}

type GroupRequest struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	RepoURL      string          `json:"repo_url"`
	BaseBranch   string          `json:"base_branch"`
	SkillContext string          `json:"skill_context"` // System-level context for workers
	Prompts      []string        `json:"prompts"`       // Simple prompts (backwards compatible)
	Tasks        []GroupTaskSpec `json:"tasks"`         // Detailed task specs with file ownership
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
	DoneMD         string `json:"done_md"` // base64-encoded DONE.md content
}

// WorkerContextData contains data for rendering the worker context template.
type WorkerContextData struct {
	TaskID         string
	WorkerID       string
	GroupName      string
	TasksInGroup   int
	OwnsFiles      []string
	ForbiddenFiles []string
	InputDir       string
	RepoURL        string
	BaseBranch     string
	BranchName     string
	TaskTimeout    string
}

// RenderWorkerContext renders the worker context template with the given data.
func RenderWorkerContext(data WorkerContextData) (string, error) {
	tmpl, err := template.New("worker_context").Parse(workerContextTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse worker context template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render worker context template: %w", err)
	}

	return buf.String(), nil
}

// Coordinator manages the worker pool and task queue.
type Coordinator struct {
	mu       sync.RWMutex
	db       *sql.DB
	config   Config
	logsDir  string
	shutdown chan struct{}
	draining bool // When true, workers should shut down after completing current task

	// WebSocket clients for real-time updates
	wsClients   map[chan []byte]struct{}
	wsClientsMu sync.RWMutex
}

// New creates a new Coordinator.
// getTailscaleIP returns the Tailscale IPv4 address if available
func getTailscaleIP() string {
	cmd := exec.Command("tailscale", "ip", "-4")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

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

	// Migration: add skill_context column to task_groups if not exists
	db.Exec(`ALTER TABLE task_groups ADD COLUMN skill_context TEXT`)

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
		db:        db,
		config:    config,
		logsDir:   logsDir,
		shutdown:  make(chan struct{}),
		wsClients: make(map[chan []byte]struct{}),
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
		var stuckStarting []string
		for rows.Next() {
			var workerID string
			rows.Scan(&workerID)
			stuckStarting = append(stuckStarting, workerID)
		}
		rows.Close()
		for _, workerID := range stuckStarting {
			log.Printf("Cleanup: deleting stuck starting worker %s", workerID)
			c.DeleteWorker(workerID)
		}
	}

	// Find idle workers that have been idle too long (30 minutes)
	rows, err = c.db.Query(`SELECT id FROM workers WHERE status = 'idle' AND last_heartbeat < datetime('now', '-30 minutes')`)
	if err == nil {
		var idleTimeout []string
		for rows.Next() {
			var workerID string
			rows.Scan(&workerID)
			idleTimeout = append(idleTimeout, workerID)
		}
		rows.Close()
		for _, workerID := range idleTimeout {
			log.Printf("Cleanup: deleting idle worker %s (idle timeout)", workerID)
			c.DeleteWorker(workerID)
		}
	}

	// NEW: Find workers with stale heartbeats (dead workers)
	// These are workers that haven't sent a heartbeat in HeartbeatDeadAge (5 minutes)
	// and aren't in 'starting' status (which don't have heartbeats yet)
	c.cleanupDeadWorkers()

	// Find orphaned VMs on exe.dev that aren't in our DB
	c.cleanupOrphanedVMs()

	// Find workers in DB whose VMs no longer exist (missing VMs)
	c.cleanupMissingVMs()
}

// cleanupDeadWorkers finds and replaces workers with stale heartbeats.
// A worker is considered dead if it hasn't sent a heartbeat in HeartbeatDeadAge (5 minutes).
func (c *Coordinator) cleanupDeadWorkers() {
	deadAgeSeconds := int(HeartbeatDeadAge.Seconds())
	query := fmt.Sprintf(`SELECT id, status, current_task_id FROM workers 
		WHERE status IN ('idle', 'busy') 
		AND last_heartbeat IS NOT NULL 
		AND last_heartbeat < datetime('now', '-%d seconds')`, deadAgeSeconds)

	rows, err := c.db.Query(query)
	if err != nil {
		log.Printf("Cleanup: failed to query dead workers: %v", err)
		return
	}

	type deadWorker struct {
		id            string
		status        string
		currentTaskID sql.NullString
	}
	var dead []deadWorker

	for rows.Next() {
		var w deadWorker
		rows.Scan(&w.id, &w.status, &w.currentTaskID)
		dead = append(dead, w)
	}
	rows.Close()

	for _, w := range dead {
		log.Printf("Cleanup: worker %s is DEAD (no heartbeat for >%d seconds, status: %s)", w.id, deadAgeSeconds, w.status)
		c.LogEvent("worker.dead", "", w.id, map[string]interface{}{
			"previous_status": w.status,
			"reason":          fmt.Sprintf("no heartbeat for >%d seconds", deadAgeSeconds),
		})

		// If worker was busy with a task, reset the task to queued so it can be retried
		if w.currentTaskID.Valid && w.currentTaskID.String != "" {
			log.Printf("Cleanup: resetting task %s from dead worker %s to queued", w.currentTaskID.String, w.id)
			c.db.Exec(`UPDATE tasks SET status = 'queued', worker_id = NULL, assigned_at = NULL, started_at = NULL, 
				retry_count = COALESCE(retry_count, 0) + 1 WHERE id = ? AND status IN ('assigned', 'running')`,
				w.currentTaskID.String)
		}

		// Delete the dead worker
		c.DeleteWorker(w.id)

		// Spawn a replacement worker if not draining
		if !c.IsDraining() {
			log.Printf("Cleanup: spawning replacement worker for dead worker %s", w.id)
			go c.SpawnWorker()
		}
	}

	if len(dead) > 0 {
		log.Printf("Cleanup: found and replaced %d dead workers", len(dead))
	}
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

	var repoURL, baseBranch, branch, worktreePath interface{}
	if req.RepoURL != "" {
		repoURL = req.RepoURL
		baseBranch = req.BaseBranch
		branch = branchName
		
		// Use git worktree if Tailscale is enabled (shared filesystem available)
		// or if explicitly requested
		useWorktree := req.UseWorktree || c.config.TailscaleAuthKey != ""
		if useWorktree {
			repo, err := c.GetOrCreateSharedRepo(req.RepoURL, req.BaseBranch)
			if err != nil {
				log.Printf("Warning: failed to create shared repo, falling back to clone: %v", err)
			} else {
				wt, err := c.CreateWorktree(repo, req.ID, req.BaseBranch)
				if err != nil {
					log.Printf("Warning: failed to create worktree, falling back to clone: %v", err)
				} else {
					worktreePath = wt.Path
					log.Printf("Task %s will use worktree at %s", req.ID, wt.Path)
				}
			}
		}
	}

	// Stage input files if provided
	var inputDir interface{}
	if len(req.InputFiles) > 0 {
		stagePath, err := c.stageInputFiles(req.ID, req.InputFiles)
		if err != nil {
			return nil, fmt.Errorf("stage input files: %w", err)
		}
		inputDir = stagePath
	}

	// Serialize file ownership patterns as JSON
	var ownsFilesJSON, forbiddenFilesJSON interface{}
	if len(req.OwnsFiles) > 0 {
		if b, err := json.Marshal(req.OwnsFiles); err == nil {
			ownsFilesJSON = string(b)
		}
	}
	if len(req.ForbiddenFiles) > 0 {
		if b, err := json.Marshal(req.ForbiddenFiles); err == nil {
			forbiddenFilesJSON = string(b)
		}
	}

	_, err := c.db.Exec(`INSERT INTO tasks (id, prompt, priority, repo_url, base_branch, branch_name, worktree_path, group_id, input_dir, owns_files, forbidden_files) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.Prompt, req.Priority, repoURL, baseBranch, branch, worktreePath, groupID, inputDir, ownsFilesJSON, forbiddenFilesJSON)
	if err != nil {
		return nil, fmt.Errorf("enqueue task: %w", err)
	}

	// Update group task count
	if req.GroupID != "" {
		c.db.Exec(`UPDATE task_groups SET tasks_total = tasks_total + 1, status = 'running' WHERE id = ?`, req.GroupID)
	}

	c.LogEvent("task.queued", req.ID, "", map[string]interface{}{
		"priority":      req.Priority,
		"repo_url":      req.RepoURL,
		"branch":        branchName,
		"worktree_path": worktreePath,
		"group_id":      req.GroupID,
		"input_dir":     inputDir,
	})

	// Auto-scale: spawn a worker if there are queued tasks and no available workers
	go c.maybeSpawnWorker()

	task, err := c.GetTask(req.ID)
	if err == nil {
		// Broadcast update to WebSocket clients
		c.BroadcastUpdate("task_created", task)
		c.BroadcastUpdate("stats", c.GetStats())
	}
	return task, err
}

// stageInputFiles creates the input directory and stages files for a task.
// Files are staged to ~/shared/source/<task-id>/
func (c *Coordinator) stageInputFiles(taskID string, files []InputFile) (string, error) {
	// Create the task input directory
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/exedev"
	}
	inputDir := filepath.Join(home, "shared", "source", taskID)
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		return "", fmt.Errorf("create input dir: %w", err)
	}

	for _, f := range files {
		if f.Path == "" {
			continue
		}

		destPath := filepath.Join(inputDir, f.Path)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return "", fmt.Errorf("create parent dir for %s: %w", f.Path, err)
		}

		if f.Content != "" {
			// Write content directly
			// Try to decode as base64, fall back to raw string
			var content []byte
			if decoded, err := base64.StdEncoding.DecodeString(f.Content); err == nil {
				content = decoded
			} else {
				content = []byte(f.Content)
			}
			if err := os.WriteFile(destPath, content, 0644); err != nil {
				return "", fmt.Errorf("write file %s: %w", f.Path, err)
			}
		} else if f.Source != "" {
			// Copy from source path
			sourcePath := filepath.Join(home, "shared", "source", f.Source)
			if err := copyFile(sourcePath, destPath); err != nil {
				return "", fmt.Errorf("copy file %s: %w", f.Source, err)
			}
		}
	}

	log.Printf("Staged %d input files for task %s at %s", len(files), taskID, inputDir)
	return inputDir, nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// GetTask retrieves a task by ID.
func (c *Coordinator) GetTask(id string) (*Task, error) {
	var t Task
	var workerID, result, errorMsg sql.NullString
	var repoURL, baseBranch, branchName, worktreePath, commitSHA, prURL, groupID sql.NullString
	var conversationID, source, inputDir sql.NullString
	var ownsFilesJSON, forbiddenFilesJSON sql.NullString
	var prNumber sql.NullInt64
	var assignedAt, startedAt, completedAt sql.NullTime

	err := c.db.QueryRow(`SELECT id, prompt, status, priority, worker_id, result, error, 
		repo_url, base_branch, branch_name, worktree_path, commit_sha, pr_url, pr_number,
		conversation_id, COALESCE(source, 'autonomous') as source, group_id, input_dir,
		owns_files, forbidden_files,
		created_at, assigned_at, started_at, completed_at FROM tasks WHERE id = ?`, id).Scan(
		&t.ID, &t.Prompt, &t.Status, &t.Priority, &workerID, &result, &errorMsg,
		&repoURL, &baseBranch, &branchName, &worktreePath, &commitSHA, &prURL, &prNumber,
		&conversationID, &source, &groupID, &inputDir,
		&ownsFilesJSON, &forbiddenFilesJSON,
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
	if worktreePath.Valid {
		t.WorktreePath = &worktreePath.String
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
	if inputDir.Valid {
		t.InputDir = &inputDir.String
	}
	if ownsFilesJSON.Valid {
		json.Unmarshal([]byte(ownsFilesJSON.String), &t.OwnsFiles)
	}
	if forbiddenFilesJSON.Valid {
		json.Unmarshal([]byte(forbiddenFilesJSON.String), &t.ForbiddenFiles)
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

	// If task belongs to a group, fetch skill context from group
	if t.GroupID != nil {
		var skillContext sql.NullString
		err := c.db.QueryRow(`SELECT skill_context FROM task_groups WHERE id = ?`, *t.GroupID).Scan(&skillContext)
		if err == nil && skillContext.Valid {
			t.SkillContext = &skillContext.String
		}
	}

	return &t, nil
}

// FileConflict describes a conflict between two tasks' file ownership.
type FileConflict struct {
	TaskID         string `json:"task_id"`
	ConflictTaskID string `json:"conflict_task_id"`
	Pattern1       string `json:"pattern1"`
	Pattern2       string `json:"pattern2"`
	Reason         string `json:"reason"` // "overlap", "owns_forbidden", "forbidden_owns"
}

// patternsOverlap checks if two glob patterns could match the same files.
// This is a conservative check - it may report false positives but not false negatives.
func patternsOverlap(p1, p2 string) bool {
	// Exact match
	if p1 == p2 {
		return true
	}

	// Check if one pattern could match a file that the other could also match.
	// We do this by checking if either pattern is a prefix/suffix of the other,
	// or if they share common path components.

	// Normalize patterns
	p1 = strings.TrimPrefix(p1, "./")
	p2 = strings.TrimPrefix(p2, "./")

	// If either is "*" or "**", they overlap with everything
	if p1 == "*" || p1 == "**" || p1 == "**/*" || p2 == "*" || p2 == "**" || p2 == "**/*" {
		return true
	}

	// Extract directory parts
	dir1 := filepath.Dir(p1)
	dir2 := filepath.Dir(p2)

	// If directories don't overlap, patterns can't overlap
	if dir1 != "." && dir2 != "." {
		// Check if one dir is prefix of the other or they're equal
		if !strings.HasPrefix(dir1, dir2) && !strings.HasPrefix(dir2, dir1) && dir1 != dir2 {
			// Check for ** which matches any depth
			if !strings.Contains(p1, "**") && !strings.Contains(p2, "**") {
				return false
			}
		}
	}

	// Extract base patterns (filename part)
	base1 := filepath.Base(p1)
	base2 := filepath.Base(p2)

	// If either base is *, they could match anything in that dir
	if base1 == "*" || base2 == "*" {
		return true
	}

	// Check for extension patterns like *.go
	if strings.HasPrefix(base1, "*.") || strings.HasPrefix(base2, "*.") {
		ext1 := strings.TrimPrefix(base1, "*")
		ext2 := strings.TrimPrefix(base2, "*")
		
		// Both are extension patterns
		if strings.HasPrefix(base1, "*.") && strings.HasPrefix(base2, "*.") {
			// *.go vs *.ts don't overlap
			return ext1 == ext2
		}
		
		// One is extension pattern, one is specific file
		if strings.HasPrefix(base1, "*.") {
			// *.go overlaps with main.go but not main.rs
			return strings.HasSuffix(base2, ext1)
		}
		if strings.HasPrefix(base2, "*.") {
			return strings.HasSuffix(base1, ext2)
		}
	}

	// Check exact filename match
	if base1 == base2 {
		return true
	}

	// Different specific filenames in same directory don't overlap
	// (unless one contains wildcards, which we've handled above)
	if !strings.Contains(base1, "*") && !strings.Contains(base2, "*") {
		return false
	}

	// If we can't prove they don't overlap, assume they might (conservative)
	return true
}

// CheckFileConflicts checks if a task would conflict with any running/assigned tasks.
// Returns a list of conflicts (empty if no conflicts).
func (c *Coordinator) CheckFileConflicts(taskID string, ownsFiles, forbiddenFiles []string) []FileConflict {
	var conflicts []FileConflict

	// If no file ownership specified, no conflicts possible
	if len(ownsFiles) == 0 && len(forbiddenFiles) == 0 {
		return conflicts
	}

	// Get all running/assigned tasks with file ownership
	rows, err := c.db.Query(`
		SELECT id, owns_files, forbidden_files 
		FROM tasks 
		WHERE status IN ('assigned', 'running') 
		  AND id != ? 
		  AND (owns_files IS NOT NULL OR forbidden_files IS NOT NULL)`,
		taskID)
	if err != nil {
		log.Printf("Warning: failed to check file conflicts: %v", err)
		return conflicts
	}
	defer rows.Close()

	for rows.Next() {
		var otherID string
		var otherOwnsJSON, otherForbiddenJSON sql.NullString
		if err := rows.Scan(&otherID, &otherOwnsJSON, &otherForbiddenJSON); err != nil {
			continue
		}

		var otherOwns, otherForbidden []string
		if otherOwnsJSON.Valid {
			json.Unmarshal([]byte(otherOwnsJSON.String), &otherOwns)
		}
		if otherForbiddenJSON.Valid {
			json.Unmarshal([]byte(otherForbiddenJSON.String), &otherForbidden)
		}

		// Check: our owns vs their owns (overlap)
		for _, p1 := range ownsFiles {
			for _, p2 := range otherOwns {
				if patternsOverlap(p1, p2) {
					conflicts = append(conflicts, FileConflict{
						TaskID:         taskID,
						ConflictTaskID: otherID,
						Pattern1:       p1,
						Pattern2:       p2,
						Reason:         "overlap",
					})
				}
			}
		}

		// Check: our owns vs their forbidden
		for _, p1 := range ownsFiles {
			for _, p2 := range otherForbidden {
				if patternsOverlap(p1, p2) {
					conflicts = append(conflicts, FileConflict{
						TaskID:         taskID,
						ConflictTaskID: otherID,
						Pattern1:       p1,
						Pattern2:       p2,
						Reason:         "owns_forbidden",
					})
				}
			}
		}

		// Check: our forbidden vs their owns
		for _, p1 := range forbiddenFiles {
			for _, p2 := range otherOwns {
				if patternsOverlap(p1, p2) {
					conflicts = append(conflicts, FileConflict{
						TaskID:         taskID,
						ConflictTaskID: otherID,
						Pattern1:       p1,
						Pattern2:       p2,
						Reason:         "forbidden_owns",
					})
				}
			}
		}
	}

	return conflicts
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

	// Get queued tasks in priority order
	rows, err := c.db.Query(`
		SELECT id, owns_files, forbidden_files 
		FROM tasks 
		WHERE status = 'queued' 
		ORDER BY priority DESC, created_at ASC 
		LIMIT 10`) // Check up to 10 tasks for conflicts
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var taskID string
	for rows.Next() {
		var id string
		var ownsFilesJSON, forbiddenFilesJSON sql.NullString
		if err := rows.Scan(&id, &ownsFilesJSON, &forbiddenFilesJSON); err != nil {
			continue
		}

		// Parse file ownership patterns
		var ownsFiles, forbiddenFiles []string
		if ownsFilesJSON.Valid {
			json.Unmarshal([]byte(ownsFilesJSON.String), &ownsFiles)
		}
		if forbiddenFilesJSON.Valid {
			json.Unmarshal([]byte(forbiddenFilesJSON.String), &forbiddenFiles)
		}

		// Check for conflicts with running tasks
		conflicts := c.CheckFileConflicts(id, ownsFiles, forbiddenFiles)
		if len(conflicts) > 0 {
			// Log the conflict and skip this task
			log.Printf("Task %s has file conflicts with running tasks, skipping: %v", id, conflicts)
			continue
		}

		taskID = id
		break
	}

	if taskID == "" {
		return nil, nil // No conflict-free tasks available
	}

	_, err = c.db.Exec(`UPDATE tasks SET status = 'assigned', worker_id = ?, assigned_at = CURRENT_TIMESTAMP WHERE id = ?`,
		workerID, taskID)
	if err != nil {
		return nil, err
	}

	c.db.Exec(`UPDATE workers SET status = 'busy', current_task_id = ? WHERE id = ?`, taskID, workerID)
	c.LogEvent("task.assigned", taskID, workerID, nil)

	task, err := c.GetTask(taskID)
	if err != nil {
		return nil, err
	}

	// Render worker context template
	c.renderWorkerContext(task, workerID)

	return task, nil
}

// renderWorkerContext populates the WorkerContext field with rendered template.
func (c *Coordinator) renderWorkerContext(task *Task, workerID string) {
	data := WorkerContextData{
		TaskID:      task.ID,
		WorkerID:    workerID,
		TaskTimeout: "15m", // default
	}

	if c.config.TaskTimeout > 0 {
		data.TaskTimeout = c.config.TaskTimeout.String()
	}

	// Get group info if task belongs to a group
	if task.GroupID != nil {
		var groupName string
		var tasksInGroup int
		err := c.db.QueryRow(`SELECT name, tasks_total FROM task_groups WHERE id = ?`, *task.GroupID).Scan(&groupName, &tasksInGroup)
		if err == nil {
			data.GroupName = groupName
			data.TasksInGroup = tasksInGroup
		}
	}

	// Set git info
	if task.RepoURL != nil {
		data.RepoURL = *task.RepoURL
	}
	if task.BaseBranch != nil {
		data.BaseBranch = *task.BaseBranch
	}
	if task.BranchName != nil {
		data.BranchName = *task.BranchName
	}
	if task.InputDir != nil {
		data.InputDir = *task.InputDir
	}

	// Set file ownership patterns
	data.OwnsFiles = task.OwnsFiles
	data.ForbiddenFiles = task.ForbiddenFiles

	// Render template
	context, err := RenderWorkerContext(data)
	if err != nil {
		log.Printf("Warning: failed to render worker context: %v", err)
		return
	}

	task.WorkerContext = &context
}

// CompleteTask marks a task as completed with result.
func (c *Coordinator) CompleteTask(req CompleteRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	status := "completed"
	if req.Error != "" {
		status = "failed"
	}

	// Parse DONE.md if provided
	var result string
	if req.DoneMD != "" {
		// Decode base64 DONE.md content
		doneContent, err := base64.StdEncoding.DecodeString(req.DoneMD)
		if err == nil {
			// Parse structured report
			report, parseErr := ParseDoneReport(string(doneContent))
			if parseErr == nil {
				// Store as JSON for structured access
				jsonResult, _ := report.ToJSON()
				result = jsonResult
				
				// Override status based on DONE.md
				if report.IsFailed() {
					status = "failed"
				} else if report.IsPartial() {
					status = "partial"
				}
				
				log.Printf("Task %s: parsed DONE.md - status=%s, files=%d, tests=%d/%d",
					req.TaskID, report.Status, len(report.FilesChanged), report.Tests.Passed, report.Tests.Failed)
			} else {
				// Fall back to raw content if parsing fails
				result = string(doneContent)
				log.Printf("Task %s: DONE.md parse failed, storing raw: %v", req.TaskID, parseErr)
			}
		}
	}
	if result == "" {
		result = req.Result
	}

	query := `UPDATE tasks SET status = ?, result = ?, error = ?, completed_at = CURRENT_TIMESTAMP`
	args := []interface{}{status, result, req.Error}

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

	// Broadcast updates to WebSocket clients
	if task, err := c.GetTask(req.TaskID); err == nil {
		c.BroadcastUpdate("task_updated", task)
	}
	if workers, err := c.ListWorkers(false); err == nil {
		c.BroadcastUpdate("workers", workers)
	}
	c.BroadcastUpdate("stats", c.GetStats())

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

	var repoURL, baseBranch, description, skillContext interface{}
	if req.RepoURL != "" {
		repoURL = req.RepoURL
		baseBranch = req.BaseBranch
	}
	if req.Description != "" {
		description = req.Description
	}
	if req.SkillContext != "" {
		skillContext = req.SkillContext
	}

	_, err := c.db.Exec(`INSERT INTO task_groups (id, name, description, repo_url, base_branch, skill_context) VALUES (?, ?, ?, ?, ?, ?)`,
		req.ID, req.Name, description, repoURL, baseBranch, skillContext)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}

	c.LogEvent("group.created", "", "", map[string]interface{}{
		"group_id": req.ID,
		"name":     req.Name,
		"repo_url": req.RepoURL,
	})

	// If simple prompts provided, create tasks for each with retry logic
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

	// If detailed task specs provided, create tasks with file ownership
	for i, taskSpec := range req.Tasks {
		var lastErr error
		for retry := 0; retry < 3; retry++ {
			_, err := c.EnqueueTask(TaskRequest{
				Prompt:         taskSpec.Prompt,
				GroupID:        req.ID,
				OwnsFiles:      taskSpec.OwnsFiles,
				ForbiddenFiles: taskSpec.ForbiddenFiles,
			})
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			if !strings.Contains(err.Error(), "SQLITE_BUSY") && !strings.Contains(err.Error(), "database is locked") {
				break
			}
			log.Printf("Retrying task spec %d for group %s (attempt %d): %v", i+1, req.ID, retry+1, err)
			time.Sleep(time.Duration(100*(1<<retry)) * time.Millisecond)
		}
		if lastErr != nil {
			return nil, fmt.Errorf("create task spec %d for group: %w", i+1, lastErr)
		}
	}

	return c.GetGroup(req.ID)
}

// GetGroup retrieves a task group by ID.
func (c *Coordinator) GetGroup(id string) (*TaskGroup, error) {
	var g TaskGroup
	var description, repoURL, baseBranch, skillContext sql.NullString
	var completedAt sql.NullTime

	err := c.db.QueryRow(`SELECT id, name, description, repo_url, base_branch, skill_context, status, 
		tasks_total, tasks_completed, tasks_failed, created_at, completed_at 
		FROM task_groups WHERE id = ?`, id).Scan(
		&g.ID, &g.Name, &description, &repoURL, &baseBranch, &skillContext, &g.Status,
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
	if skillContext.Valid {
		g.SkillContext = &skillContext.String
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

	// Use HTTPS download (default) or custom install script URL
	// Note: SCP method was removed as it doesn't work through exe.dev SSH (binary data gets corrupted)
	if installScript == "scp" {
		log.Printf("WARNING: SCP install method is deprecated and non-functional, falling back to HTTPS")
		installScript = "https"
	}

	if installScript != "https" && installScript != "" {
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
		// Always use HTTPS through exe.dev proxy for initial download
		// (Worker hasn't joined Tailscale yet at this point)
		log.Printf("Downloading shelley binary to %s from coordinator...", workerID)
		downloadURL := fmt.Sprintf("https://%s:%d/api/shelley-bin?token=%s", c.config.CoordHost, c.config.Port, c.config.APIToken)
		downloadCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'curl -fsSL \"%s\" -o ~/.local/bin/shelley'", workerID, downloadURL))
		if out, err := downloadCmd.CombinedOutput(); err != nil {
			log.Printf("Failed to download shelley to %s: %v\n%s", workerID, err, out)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
		log.Printf("Downloaded shelley binary to %s", workerID)
		// Make it executable
		chmodCmd := exec.Command("ssh", "exe.dev", fmt.Sprintf("ssh %s 'chmod +x ~/.local/bin/shelley'", workerID))
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
	// Determine whether Tailscale and MinIO bootstrap should be included
	tailscaleBootstrap := ""
	if c.config.TailscaleAuthKey != "" {
		// Get coordinator's Tailscale IP
		coordTailscaleIP := c.config.TailscaleIP
		if coordTailscaleIP == "" {
			coordTailscaleIP = getTailscaleIP()
		}
		
		tailscaleBootstrap = fmt.Sprintf(`
# === Tailscale Network Bootstrap ===
echo "Setting up Tailscale network..."

COORD_TAILSCALE_IP="%s"

if ! command -v tailscale &>/dev/null; then
    echo "Installing Tailscale..."
    curl -fsSL https://tailscale.com/install.sh | sh
fi

# Ensure tailscaled is running
if ! pgrep tailscaled &>/dev/null; then
    echo "Starting tailscaled..."
    sudo systemctl start tailscaled || sudo tailscaled &
    sleep 2
fi

# Check if already connected
if ! tailscale status &>/dev/null; then
    echo "Joining Tailscale network..."
    sudo tailscale up --authkey="%s" --hostname="$WORKER_ID"
    sleep 3
fi

TAILSCALE_IP=$(tailscale ip -4 2>/dev/null || echo "")
if [ -n "$TAILSCALE_IP" ]; then
    echo "✓ Tailscale connected: $TAILSCALE_IP"
    
    # Set up SSH access to coordinator (for file sync)
    echo "Setting up SSH to coordinator ($COORD_TAILSCALE_IP)..."
    mkdir -p ~/.ssh
    chmod 700 ~/.ssh
    
    # Generate SSH key if not present
    if [ ! -f ~/.ssh/id_ed25519 ]; then
        echo "Generating SSH key for SSHFS access..."
        ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N "" -q
    fi
    
    # Register our public key with the coordinator
    echo "Registering SSH key with coordinator..."
    SSH_PUBKEY=$(cat ~/.ssh/id_ed25519.pub)
    curl -sf -X POST "http://$COORD_TAILSCALE_IP:${COORD_PORT:-8081}/api/register-ssh-key" \
        -H "Content-Type: application/json" \
        -d "{\"worker_id\": \"$WORKER_ID\", \"pub_key\": \"$SSH_PUBKEY\"}" || echo "Warning: Failed to register SSH key"
    
    # Add coordinator to known_hosts
    ssh-keyscan -H $COORD_TAILSCALE_IP >> ~/.ssh/known_hosts 2>/dev/null
    
    # Install sshfs and rsync if not present
    if ! command -v sshfs &>/dev/null || ! command -v rsync &>/dev/null; then
        echo "Installing sshfs and rsync..."
        sudo apt-get update -qq && sudo apt-get install -y -qq sshfs rsync
    fi
    
    # Mount coordinator's shared directory via SSHFS with retry logic
    echo "Mounting coordinator's ~/shared via SSHFS..."
    mkdir -p ~/shared
    
    # Unmount if already mounted
    if mountpoint -q ~/shared 2>/dev/null; then
        fusermount -u ~/shared 2>/dev/null || true
        sleep 1
    fi
    
    # Wait for SSH key registration to propagate
    sleep 2
    
    # Mount with retry logic (3 attempts with exponential backoff)
    SSHFS_MOUNTED=false
    for attempt in 1 2 3; do
        echo "SSHFS mount attempt $attempt/3..."
        if sshfs exedev@$COORD_TAILSCALE_IP:shared ~/shared \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o reconnect \
            -o ServerAliveInterval=15 \
            -o ServerAliveCountMax=3 \
            -o allow_other 2>/dev/null || \
           sshfs exedev@$COORD_TAILSCALE_IP:shared ~/shared \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o reconnect \
            -o ServerAliveInterval=15 \
            -o ServerAliveCountMax=3 2>&1; then
            # Verify mount actually succeeded
            sleep 1
            if mountpoint -q ~/shared 2>/dev/null; then
                SSHFS_MOUNTED=true
                break
            fi
        fi
        echo "Mount attempt $attempt failed, retrying in $((attempt * 2)) seconds..."
        sleep $((attempt * 2))
    done
    
    # Report result and create directories
    if [ "$SSHFS_MOUNTED" = true ]; then
        echo "✓ SSHFS mount successful: ~/shared -> coordinator:~/shared"
        # Ensure subdirectories exist (create on coordinator if needed)
        mkdir -p ~/shared/source ~/shared/tasks ~/shared/results 2>/dev/null || true
        export SSHFS_AVAILABLE=true
    else
        echo "WARNING: SSHFS mount failed after 3 attempts"
        echo "  Worker will continue with DEGRADED functionality (no shared filesystem)"
        echo "  Tasks that require ~/shared will fail"
        mkdir -p ~/shared/source ~/shared/tasks ~/shared/results
        export SSHFS_AVAILABLE=false
    fi
    
    # Export coordinator IP for use in tasks
    echo "export COORD_TAILSCALE_IP=$COORD_TAILSCALE_IP" >> ~/.bashrc
    export COORD_TAILSCALE_IP
    
    # Create rsync helper for bulk transfers (faster than SSHFS for large files)
    mkdir -p ~/bin
    cat > ~/bin/coord-sync << 'SYNCEOF'
#!/bin/bash
# coord-sync - Fast bulk file sync with coordinator via rsync over Tailscale
# Usage: coord-sync pull <remote_path> [local_path]
#        coord-sync push <local_path> [remote_path]
#
# For large files/directories, rsync is 2-3x faster than SSHFS

set -e
ACTION="$1"
PATH1="$2"
PATH2="$3"

if [ -z "$COORD_TAILSCALE_IP" ]; then
    COORD_TAILSCALE_IP=$(grep COORD_TAILSCALE_IP ~/.bashrc | cut -d= -f2)
fi

if [ -z "$ACTION" ] || [ -z "$PATH1" ]; then
    echo "Usage: coord-sync <pull|push> <path> [dest_path]"
    echo ""
    echo "Examples:"
    echo "  coord-sync pull shared/source/repo ~/work/    # Pull from coordinator"
    echo "  coord-sync push ~/results shared/tasks/       # Push to coordinator"
    echo ""
    echo "Note: For small files, use ~/shared directly (SSHFS mounted)"
    echo "      For large transfers (>1MB), coord-sync is 2-3x faster"
    exit 1
fi

case "$ACTION" in
    pull)
        REMOTE="$PATH1"
        LOCAL="${PATH2:-.}"
        echo "Pulling $REMOTE from coordinator..."
        rsync -az --progress exedev@$COORD_TAILSCALE_IP:"$REMOTE" "$LOCAL"
        echo "Done."
        ;;
    push)
        LOCAL="$PATH1"
        REMOTE="${PATH2:-shared/tasks/}"
        echo "Pushing $LOCAL to coordinator:$REMOTE..."
        rsync -az --progress "$LOCAL" exedev@$COORD_TAILSCALE_IP:"$REMOTE"
        echo "Done."
        ;;
    *)
        echo "Unknown action: $ACTION (use pull or push)"
        exit 1
        ;;
esac
SYNCEOF
    chmod +x ~/bin/coord-sync
    echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
    
    # Override COORD to use Tailscale IP (bypasses exe.dev HTTPS proxy)
    export COORD="http://$COORD_TAILSCALE_IP:${COORD_PORT:-8081}"
    
    # Download shelley binary via HTTP now that Tailscale is connected
    echo "Downloading shelley binary via Tailscale..."
    curl -fsSL "$COORD/api/shelley-bin?token=$API_TOKEN" -o ~/.local/bin/shelley && chmod +x ~/.local/bin/shelley
    if [ -x ~/.local/bin/shelley ]; then
        echo "✓ Shelley binary downloaded successfully"
        ~/.local/bin/shelley version 2>/dev/null | head -1 || true
    else
        echo "Warning: Failed to download shelley binary"
    fi
    
    echo "✓ Tailscale setup complete"
    echo "  - Coordinator reachable at: $COORD_TAILSCALE_IP"
    echo "  - Shared filesystem: ~/shared (SSHFS mounted)"
    echo "  - Bulk transfer: coord-sync pull/push (rsync, 2-3x faster)"
    echo "  - API endpoint: $COORD"
else
    echo "Warning: Tailscale connection failed"
fi
echo "=== Tailscale Bootstrap Complete ==="
`, coordTailscaleIP, c.config.TailscaleAuthKey)
	}

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
%s
# Start HTTP file server for artifact access (serves home directory on port 8000)
# This allows the coordinator and other VMs to pull files via HTTPS
echo "Starting HTTP file server on port 8000..."
cd $HOME && nohup python3 -m http.server 8000 > /tmp/http-server.log 2>&1 &
HTTP_SERVER_PID=$!
echo "HTTP server started (PID: $HTTP_SERVER_PID) - files available at https://${WORKER_ID}.exe.xyz:8000/"

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
        WORKTREE_PATH=$(echo "$RESPONSE" | jq -r '.worktree_path // empty')
        INPUT_DIR=$(echo "$RESPONSE" | jq -r '.input_dir // empty')
        SKILL_CONTEXT=$(echo "$RESPONSE" | jq -r '.skill_context // empty')
        WORKER_CONTEXT=$(echo "$RESPONSE" | jq -r '.worker_context // empty')
        
        echo "=== Task $TASK_ID ==="
        echo "Prompt: $PROMPT"
        if [ -n "$INPUT_DIR" ]; then
            echo "Input files staged at: ~/shared/source/$TASK_ID/"
        fi
        
        # Create results directory for this task
        RESULTS_DIR="$HOME/shared/results/$TASK_ID"
        mkdir -p "$RESULTS_DIR" 2>/dev/null || true
        
        TASK_DIR="$WORKDIR/$TASK_ID"
        COMMIT_SHA=""
        ERROR=""
        CWD="$HOME"
        USE_WORKTREE=false
        
        # Check if we have a worktree path (shared repo via SSHFS)
        if [ -n "$WORKTREE_PATH" ] && [ -d "$WORKTREE_PATH" ]; then
            echo "Using shared git worktree at: $WORKTREE_PATH"
            TASK_DIR="$WORKTREE_PATH"
            CWD="$WORKTREE_PATH"
            USE_WORKTREE=true
            
            # Verify the worktree is valid
            cd "$WORKTREE_PATH"
            if ! git status &>/dev/null; then
                echo "Warning: Worktree appears invalid, will clone instead"
                USE_WORKTREE=false
            else
                git config user.email "shelley-worker@exe.dev"
                git config user.name "Shelley Worker ($WORKER_ID)"
                echo "Branch: $(git branch --show-current)"
            fi
        fi
        
        # If no worktree or worktree invalid, fall back to cloning
        if [ "$USE_WORKTREE" = false ] && [ -n "$REPO_URL" ]; then
            echo "Cloning $REPO_URL..."
            TASK_DIR="$WORKDIR/$TASK_ID"
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
        elif [ "$USE_WORKTREE" = false ]; then
            # No repo URL, just create a workspace directory
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
        
        # Build the full prompt with worker context
        FULL_PROMPT=""
        
        # Use worker context if provided (includes task info, file ownership, output format)
        if [ -n "$WORKER_CONTEXT" ]; then
            FULL_PROMPT="[Worker Context]\n$WORKER_CONTEXT\n[End Worker Context]\n\n"
        # Fall back to skill context for backward compatibility
        elif [ -n "$SKILL_CONTEXT" ]; then
            FULL_PROMPT="[System Context]\n$SKILL_CONTEXT\n[End System Context]\n\n"
        fi
        
        # Add the actual task prompt
        FULL_PROMPT="${FULL_PROMPT}[Task]\n$PROMPT"
        
        OUTPUT=$(shelley -db "$SHELLEY_DB" -config ~/.config/shelley/shelley.json \
            chat -yes -prompt "$FULL_PROMPT" 2>&1) || true
        
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
        
        # Check for DONE.md in results directory
        DONE_MD=""
        if [ -f "$RESULTS_DIR/DONE.md" ]; then
            DONE_MD=$(cat "$RESULTS_DIR/DONE.md" | base64 -w0)
            echo "Found DONE.md in results directory"
        fi
        
        # Report completion with conversation ID and DONE.md content
        curl -s -X POST "$COORD/api/complete" \
            -H "Content-Type: application/json" \
            -H "X-Coordinator-Token: $API_TOKEN" \
            -d "$(jq -n --arg tid "$TASK_ID" --arg wid "$WORKER_ID" \
                       --arg cid "$CONV_ID" --arg sha "$COMMIT_SHA" --arg err "$ERROR" \
                       --arg done "$DONE_MD" \
                '{task_id: $tid, worker_id: $wid, conversation_id: $cid, commit_sha: $sha, error: $err, done_md: $done}')"
        
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
`, c.config.CoordHost, c.config.Port, workerID, c.config.APIToken, tailscaleBootstrap)

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

	// Calculate health status based on heartbeat age
	c.calculateWorkerHealth(&w)

	return &w, nil
}

// calculateWorkerHealth determines worker health based on heartbeat staleness.
// This updates the Health, HeartbeatAgeSec, and HeartbeatWarning fields.
func (c *Coordinator) calculateWorkerHealth(w *Worker) {
	// Workers that are starting, failed, or deleted don't need health checks
	if w.Status == "starting" || w.Status == "failed" || w.Status == "deleted" {
		w.Health = w.Status
		return
	}

	// If no heartbeat recorded yet, worker is still initializing
	if w.LastHeartbeat == nil {
		w.Health = "initializing"
		return
	}

	age := time.Since(*w.LastHeartbeat)
	ageSec := int(age.Seconds())
	w.HeartbeatAgeSec = &ageSec

	switch {
	case age > HeartbeatDeadAge:
		w.Health = "dead"
		w.HeartbeatWarning = true
	case age > HeartbeatStaleAge:
		w.Health = "unhealthy"
		w.HeartbeatWarning = true
	case age > HeartbeatWarningAge:
		w.Health = "warning"
		w.HeartbeatWarning = true
	default:
		w.Health = "healthy"
		w.HeartbeatWarning = false
	}
}

// DeleteWorker removes a worker VM and cleans up its SSH key.
func (c *Coordinator) DeleteWorker(workerID string) error {
	log.Printf("Deleting worker: %s", workerID)

	// Get the worker's SSH public key before deleting from DB
	var sshPubkey sql.NullString
	c.db.QueryRow(`SELECT ssh_pubkey FROM workers WHERE id = ?`, workerID).Scan(&sshPubkey)

	// Clean up the SSH key from authorized_keys
	if sshPubkey.Valid && sshPubkey.String != "" {
		c.removeSSHKey(sshPubkey.String, workerID)
	}

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

// removeSSHKey removes a worker's SSH public key from authorized_keys.
func (c *Coordinator) removeSSHKey(pubkey, workerID string) {
	// Find the authorized_keys file
	authKeysPath := "/exe.dev/etc/ssh/authorized_keys"
	if _, err := os.Stat(authKeysPath); os.IsNotExist(err) {
		authKeysPath = filepath.Join(os.Getenv("HOME"), ".ssh", "authorized_keys")
	}

	// Read the current file
	data, err := os.ReadFile(authKeysPath)
	if err != nil {
		log.Printf("Warning: failed to read authorized_keys for cleanup: %v", err)
		return
	}

	// Filter out the key
	lines := strings.Split(string(data), "\n")
	var newLines []string
	for _, line := range lines {
		// Keep lines that don't contain the key
		if !strings.Contains(line, pubkey) {
			newLines = append(newLines, line)
		}
	}

	// Write back
	newData := strings.Join(newLines, "\n")
	if err := os.WriteFile(authKeysPath, []byte(newData), 0600); err != nil {
		log.Printf("Warning: failed to update authorized_keys: %v", err)
		return
	}

	log.Printf("Removed SSH key for worker %s", workerID)
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

	// Broadcast worker update
	if workers, err := c.ListWorkers(false); err == nil {
		c.BroadcastUpdate("workers", workers)
	}
	c.BroadcastUpdate("stats", c.GetStats())

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
		if currentTaskID.Valid {
			w.CurrentTaskID = &currentTaskID.String
		}
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

		// Calculate health status based on heartbeat age
		c.calculateWorkerHealth(&w)
		
		workers = append(workers, w)
	}

	return workers, nil
}

// SharedRepo represents a git repository cloned to the shared filesystem.
type SharedRepo struct {
	ID            string     `json:"id"`
	URL           string     `json:"url"`
	Path          string     `json:"path"`
	DefaultBranch string     `json:"default_branch"`
	LastFetched   *time.Time `json:"last_fetched,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Worktree represents a git worktree for a task.
type Worktree struct {
	TaskID     string `json:"task_id"`
	BranchName string `json:"branch_name"`
	Path       string `json:"path"`
}

// repoIDFromURL extracts a repo identifier from a git URL.
// e.g., "https://github.com/owner/repo.git" -> "owner-repo"
func repoIDFromURL(url string) string {
	// Remove protocol and .git suffix
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git@")
	url = strings.TrimSuffix(url, ".git")
	
	// Handle github.com/owner/repo or github.com:owner/repo
	url = strings.Replace(url, ":", "/", 1)
	
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		// Take last two parts (owner/repo)
		return parts[len(parts)-2] + "-" + parts[len(parts)-1]
	}
	return strings.ReplaceAll(url, "/", "-")
}

// GetOrCreateSharedRepo ensures a repo is cloned to the shared filesystem.
// Returns the SharedRepo with path to the local clone.
func (c *Coordinator) GetOrCreateSharedRepo(repoURL, baseBranch string) (*SharedRepo, error) {
	if baseBranch == "" {
		baseBranch = "main"
	}
	
	repoID := repoIDFromURL(repoURL)
	
	// Check if repo already exists
	var repo SharedRepo
	err := c.db.QueryRow(`SELECT id, url, path, default_branch, last_fetched, created_at 
		FROM shared_repos WHERE id = ?`, repoID).Scan(
		&repo.ID, &repo.URL, &repo.Path, &repo.DefaultBranch, &repo.LastFetched, &repo.CreatedAt)
	
	if err == nil {
		// Repo exists, fetch latest
		log.Printf("Shared repo %s exists at %s, fetching updates...", repoID, repo.Path)
		if err := c.fetchRepo(&repo); err != nil {
			log.Printf("Warning: failed to fetch repo %s: %v", repoID, err)
		}
		return &repo, nil
	}
	
	// Clone new repo
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/exedev"
	}
	repoPath := filepath.Join(home, "shared", "repos", repoID)
	
	log.Printf("Cloning shared repo %s to %s...", repoURL, repoPath)
	
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(repoPath), 0755); err != nil {
		return nil, fmt.Errorf("create repos dir: %w", err)
	}
	
	// Clone with credentials if available
	cloneURL := repoURL
	if c.config.GitToken != "" {
		// Inject token into HTTPS URL
		cloneURL = strings.Replace(repoURL, "https://", fmt.Sprintf("https://%s@", c.config.GitToken), 1)
	}
	
	cmd := exec.Command("git", "clone", "--branch", baseBranch, cloneURL, repoPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clone repo: %s - %w", string(out), err)
	}
	
	// Configure git in the repo
	exec.Command("git", "-C", repoPath, "config", "user.email", "shelley-coordinator@exe.dev").Run()
	exec.Command("git", "-C", repoPath, "config", "user.name", "Shelley Coordinator").Run()
	
	// Store credentials for push
	if c.config.GitToken != "" {
		gitUser := c.config.GitUser
		if gitUser == "" {
			gitUser = "git"
		}
		credentialURL := fmt.Sprintf("https://%s:%s@github.com", gitUser, c.config.GitToken)
		exec.Command("git", "-C", repoPath, "config", "credential.helper", "store").Run()
		credFile := filepath.Join(repoPath, ".git-credentials")
		os.WriteFile(credFile, []byte(credentialURL+"\n"), 0600)
	}
	
	repo = SharedRepo{
		ID:            repoID,
		URL:           repoURL,
		Path:          repoPath,
		DefaultBranch: baseBranch,
		CreatedAt:     time.Now(),
	}
	
	_, err = c.db.Exec(`INSERT INTO shared_repos (id, url, path, default_branch, last_fetched) 
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		repo.ID, repo.URL, repo.Path, repo.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("save repo: %w", err)
	}
	
	log.Printf("Shared repo %s cloned successfully", repoID)
	return &repo, nil
}

// fetchRepo fetches the latest changes from origin.
func (c *Coordinator) fetchRepo(repo *SharedRepo) error {
	cmd := exec.Command("git", "-C", repo.Path, "fetch", "--all", "--prune")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: %s - %w", string(out), err)
	}
	
	c.db.Exec(`UPDATE shared_repos SET last_fetched = CURRENT_TIMESTAMP WHERE id = ?`, repo.ID)
	now := time.Now()
	repo.LastFetched = &now
	return nil
}

// CreateWorktree creates a git worktree for a task.
// The worktree is created at ~/shared/repos/<repo-id>-task-<task-id>/
func (c *Coordinator) CreateWorktree(repo *SharedRepo, taskID, baseBranch string) (*Worktree, error) {
	if baseBranch == "" {
		baseBranch = repo.DefaultBranch
	}
	
	branchName := fmt.Sprintf("task-%s", taskID)
	worktreePath := repo.Path + "-" + branchName
	
	// Check if worktree already exists
	if _, err := os.Stat(worktreePath); err == nil {
		log.Printf("Worktree %s already exists", worktreePath)
		return &Worktree{
			TaskID:     taskID,
			BranchName: branchName,
			Path:       worktreePath,
		}, nil
	}
	
	// Make sure we have the latest base branch
	exec.Command("git", "-C", repo.Path, "fetch", "origin", baseBranch).Run()
	
	// Create worktree with new branch based on origin/baseBranch
	cmd := exec.Command("git", "-C", repo.Path, "worktree", "add", 
		"-b", branchName, worktreePath, "origin/"+baseBranch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create worktree: %s - %w", string(out), err)
	}
	
	log.Printf("Created worktree for task %s at %s (branch: %s from %s)", 
		taskID, worktreePath, branchName, baseBranch)
	
	return &Worktree{
		TaskID:     taskID,
		BranchName: branchName,
		Path:       worktreePath,
	}, nil
}

// RemoveWorktree removes a git worktree.
func (c *Coordinator) RemoveWorktree(repo *SharedRepo, taskID string) error {
	branchName := fmt.Sprintf("task-%s", taskID)
	worktreePath := repo.Path + "-" + branchName
	
	// Remove worktree
	cmd := exec.Command("git", "-C", repo.Path, "worktree", "remove", "--force", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("Warning: failed to remove worktree %s: %s", worktreePath, string(out))
	}
	
	// Optionally delete the branch if it was merged
	// exec.Command("git", "-C", repo.Path, "branch", "-d", branchName).Run()
	
	return nil
}

// ListWorktrees returns all worktrees for a repo.
func (c *Coordinator) ListWorktrees(repo *SharedRepo) ([]Worktree, error) {
	cmd := exec.Command("git", "-C", repo.Path, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	
	var worktrees []Worktree
	var currentPath, currentBranch string
	
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			currentPath = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "branch ") {
			currentBranch = strings.TrimPrefix(line, "branch refs/heads/")
		} else if line == "" && currentPath != "" {
			// Extract task ID from branch name if it's a task branch
			if strings.HasPrefix(currentBranch, "task-") {
				taskID := strings.TrimPrefix(currentBranch, "task-")
				worktrees = append(worktrees, Worktree{
					TaskID:     taskID,
					BranchName: currentBranch,
					Path:       currentPath,
				})
			}
			currentPath = ""
			currentBranch = ""
		}
	}
	
	return worktrees, nil
}

// GetSharedRepo retrieves a shared repo by ID.
func (c *Coordinator) GetSharedRepo(repoID string) (*SharedRepo, error) {
	var repo SharedRepo
	var lastFetched sql.NullTime
	err := c.db.QueryRow(`SELECT id, url, path, default_branch, last_fetched, created_at 
		FROM shared_repos WHERE id = ?`, repoID).Scan(
		&repo.ID, &repo.URL, &repo.Path, &repo.DefaultBranch, &lastFetched, &repo.CreatedAt)
	if err != nil {
		return nil, err
	}
	if lastFetched.Valid {
		repo.LastFetched = &lastFetched.Time
	}
	return &repo, nil
}

// ListSharedRepos returns all shared repos.
func (c *Coordinator) ListSharedRepos() ([]SharedRepo, error) {
	rows, err := c.db.Query(`SELECT id, url, path, default_branch, last_fetched, created_at 
		FROM shared_repos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var repos []SharedRepo
	for rows.Next() {
		var repo SharedRepo
		var lastFetched sql.NullTime
		rows.Scan(&repo.ID, &repo.URL, &repo.Path, &repo.DefaultBranch, &lastFetched, &repo.CreatedAt)
		if lastFetched.Valid {
			repo.LastFetched = &lastFetched.Time
		}
		repos = append(repos, repo)
	}
	return repos, nil
}

// DeleteSharedRepo removes a shared repository and all its worktrees.
func (c *Coordinator) DeleteSharedRepo(repoID string) error {
	// Get the repo to find its path
	repo, err := c.GetSharedRepo(repoID)
	if err != nil {
		return fmt.Errorf("repo not found: %w", err)
	}

	// Remove worktrees first
	worktrees, _ := c.ListWorktrees(repo)
	for _, wt := range worktrees {
		os.RemoveAll(wt.Path)
	}

	// Remove the repo directory
	if repo.Path != "" {
		if err := os.RemoveAll(repo.Path); err != nil {
			log.Printf("Warning: failed to remove repo directory %s: %v", repo.Path, err)
		}
	}

	// Delete from database
	_, err = c.db.Exec(`DELETE FROM shared_repos WHERE id = ?`, repoID)
	if err != nil {
		return fmt.Errorf("delete from db: %w", err)
	}

	log.Printf("Deleted shared repo: %s", repoID)
	return nil
}
