// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"crypto/rand"
	"database/sql"
	"embed"
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
	GitToken     string // GitHub/GitLab token for HTTPS auth
	GitUser      string // Git username (default: git)
}

// Task represents a unit of work.
type Task struct {
	ID          string     `json:"id"`
	Prompt      string     `json:"prompt"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	WorkerID    *string    `json:"worker_id,omitempty"`
	Result      *string    `json:"result,omitempty"`
	Error       *string    `json:"error,omitempty"`
	RepoURL     *string    `json:"repo_url,omitempty"`
	BaseBranch  *string    `json:"base_branch,omitempty"`
	BranchName  *string    `json:"branch_name,omitempty"`
	CommitSHA   *string    `json:"commit_sha,omitempty"`
	PRURL       *string    `json:"pr_url,omitempty"`
	PRNumber    *int       `json:"pr_number,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	AssignedAt  *time.Time `json:"assigned_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Worker represents a VM in the pool.
type Worker struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	CurrentTaskID  *string    `json:"current_task_id,omitempty"`
	TailscaleIP    *string    `json:"tailscale_ip,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	LastHeartbeat  *time.Time `json:"last_heartbeat,omitempty"`
	TasksCompleted int        `json:"tasks_completed"`
}

// TaskRequest contains parameters for creating a task.
type TaskRequest struct {
	ID         string `json:"id"`
	Prompt     string `json:"prompt"`
	Priority   int    `json:"priority"`
	RepoURL    string `json:"repo_url"`
	BaseBranch string `json:"base_branch"`
}

// CompleteRequest contains the result of a completed task.
type CompleteRequest struct {
	TaskID    string `json:"task_id"`
	WorkerID  string `json:"worker_id"`
	Result    string `json:"result"`
	Error     string `json:"error"`
	CommitSHA string `json:"commit_sha"`
	PRURL     string `json:"pr_url"`
	PRNumber  int    `json:"pr_number"`
}

// Coordinator manages the worker pool and task queue.
type Coordinator struct {
	mu       sync.RWMutex
	db       *sql.DB
	config   Config
	logsDir  string
	shutdown chan struct{}
}

// New creates a new Coordinator.
func New(config Config) (*Coordinator, error) {
	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	schema, _ := schemaFS.ReadFile("schema.sql")
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	logsDir := "logs"
	os.MkdirAll(logsDir, 0755)

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

	branchName := fmt.Sprintf("task-%s", req.ID)

	var repoURL, baseBranch, branch interface{}
	if req.RepoURL != "" {
		repoURL = req.RepoURL
		baseBranch = req.BaseBranch
		branch = branchName
	}

	_, err := c.db.Exec(`INSERT INTO tasks (id, prompt, priority, repo_url, base_branch, branch_name) VALUES (?, ?, ?, ?, ?, ?)`,
		req.ID, req.Prompt, req.Priority, repoURL, baseBranch, branch)
	if err != nil {
		return nil, fmt.Errorf("enqueue task: %w", err)
	}

	c.LogEvent("task.queued", req.ID, "", map[string]interface{}{
		"priority": req.Priority,
		"repo_url": req.RepoURL,
		"branch":   branchName,
	})

	return c.GetTask(req.ID)
}

// GetTask retrieves a task by ID.
func (c *Coordinator) GetTask(id string) (*Task, error) {
	var t Task
	var workerID, result, errorMsg sql.NullString
	var repoURL, baseBranch, branchName, commitSHA, prURL sql.NullString
	var prNumber sql.NullInt64
	var assignedAt, startedAt, completedAt sql.NullTime

	err := c.db.QueryRow(`SELECT id, prompt, status, priority, worker_id, result, error, 
		repo_url, base_branch, branch_name, commit_sha, pr_url, pr_number,
		created_at, assigned_at, started_at, completed_at FROM tasks WHERE id = ?`, id).Scan(
		&t.ID, &t.Prompt, &t.Status, &t.Priority, &workerID, &result, &errorMsg,
		&repoURL, &baseBranch, &branchName, &commitSHA, &prURL, &prNumber,
		&t.CreatedAt, &assignedAt, &startedAt, &completedAt)
	if err != nil {
		return nil, err
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

	c.db.Exec(`UPDATE workers SET last_heartbeat = CURRENT_TIMESTAMP, status = 'idle' WHERE id = ?`, workerID)

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

	if c.config.GitLogging {
		go c.gitLogTask(req.TaskID)
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

// SpawnWorker creates a new worker VM.
func (c *Coordinator) SpawnWorker() (*Worker, error) {
	b := make([]byte, 6)
	rand.Read(b)
	workerID := fmt.Sprintf("%s-%s", c.config.WorkerPrefix, hex.EncodeToString(b))

	_, err := c.db.Exec(`INSERT INTO workers (id, status) VALUES (?, 'starting')`, workerID)
	if err != nil {
		return nil, err
	}

	c.LogEvent("worker.starting", "", workerID, nil)
	go c.setupWorker(workerID)

	return c.GetWorker(workerID)
}

func (c *Coordinator) setupWorker(workerID string) {
	workerHost := workerID + ".exe.xyz"

	log.Printf("Spawning worker VM: %s", workerID)
	cmd := exec.Command("ssh", "exe.dev", fmt.Sprintf(`new --name=%s --prompt="Worker VM" --no-email --json`, workerID))
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to spawn %s: %v\n%s", workerID, err, output)
		c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
		c.LogEvent("worker.failed", "", workerID, map[string]interface{}{"error": err.Error()})
		return
	}

	log.Printf("Waiting for %s SSH...", workerID)
	for i := 0; i < 60; i++ {
		time.Sleep(3 * time.Second)
		checkCmd := exec.Command("ssh", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=no",
			workerHost, "echo", "ready")
		if out, err := checkCmd.CombinedOutput(); err == nil && strings.Contains(string(out), "ready") {
			break
		}
		if i == 59 {
			log.Printf("Timeout waiting for %s", workerID)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
	}

	log.Printf("Installing shelley-cli on %s...", workerID)
	downloadURL := fmt.Sprintf("https://%s:%d/shelley-bin", c.config.CoordHost, c.config.Port)

	installCmds := [][]string{
		{"mkdir", "-p", ".local/bin", ".config/shelley"},
		{"curl", "-sSL", "-o", ".local/bin/shelley", downloadURL},
		{"chmod", "+x", ".local/bin/shelley"},
	}

	for _, args := range installCmds {
		cmd := exec.Command("ssh", append([]string{"-o", "ConnectTimeout=60", "-o", "StrictHostKeyChecking=no", workerHost}, args...)...)
		if _, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Install failed on %s: %v", workerID, err)
			c.db.Exec(`UPDATE workers SET status = 'failed' WHERE id = ?`, workerID)
			return
		}
	}

	configJSON := `{"llm_gateway": "http://169.254.169.254/gateway/llm", "default_model": "claude-sonnet-4.5"}`
	configCmd := exec.Command("ssh", "-o", "ConnectTimeout=30", "-o", "StrictHostKeyChecking=no",
		workerHost, "tee", ".config/shelley/shelley.json")
	configCmd.Stdin = strings.NewReader(configJSON)
	configCmd.Run()

	// Configure git credentials if token provided
	if c.config.GitToken != "" {
		gitUser := c.config.GitUser
		if gitUser == "" {
			gitUser = "git"
		}
		// Configure git credential helper to use token
		gitSetupCmds := [][]string{
			{"git", "config", "--global", "credential.helper", "store"},
			{"git", "config", "--global", "user.email", "shelley-worker@exe.dev"},
			{"git", "config", "--global", "user.name", fmt.Sprintf("Shelley Worker (%s)", workerID)},
		}
		for _, args := range gitSetupCmds {
			exec.Command("ssh", append([]string{"-o", "StrictHostKeyChecking=no", workerHost}, args...)...).Run()
		}
		// Store credentials for GitHub
		credentials := fmt.Sprintf("https://%s:%s@github.com\n", gitUser, c.config.GitToken)
		credCmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", workerHost, "tee", ".git-credentials")
		credCmd.Stdin = strings.NewReader(credentials)
		credCmd.Run()
		exec.Command("ssh", "-o", "StrictHostKeyChecking=no", workerHost, "chmod", "600", ".git-credentials").Run()
	}

	c.startWorkerLoop(workerID, workerHost)
}

func (c *Coordinator) startWorkerLoop(workerID, workerHost string) {
	pollScript := fmt.Sprintf(`#!/bin/bash
set -e
export PATH="$HOME/.local/bin:$PATH"
COORD="https://%s:%d"
WORKER_ID="%s"
IDLE_COUNT=0
MAX_IDLE=24
WORKDIR="$HOME/workspaces"

mkdir -p "$WORKDIR"

while true; do
    RESPONSE=$(curl -s "$COORD/api/next-task?worker=$WORKER_ID")
    TASK_ID=$(echo "$RESPONSE" | jq -r '.id // empty')
    
    if [ -n "$TASK_ID" ]; then
        IDLE_COUNT=0
        PROMPT=$(echo "$RESPONSE" | jq -r '.prompt')
        REPO_URL=$(echo "$RESPONSE" | jq -r '.repo_url // empty')
        BASE_BRANCH=$(echo "$RESPONSE" | jq -r '.base_branch // "main"')
        BRANCH_NAME=$(echo "$RESPONSE" | jq -r '.branch_name // empty')
        
        echo "=== Task $TASK_ID ==="
        echo "Prompt: $PROMPT"
        echo "Repo: $REPO_URL"
        echo "Branch: $BRANCH_NAME"
        
        TASK_DIR="$WORKDIR/$TASK_ID"
        COMMIT_SHA=""
        ERROR=""
        
        # If repo URL provided, clone and setup git
        if [ -n "$REPO_URL" ]; then
            echo "Cloning $REPO_URL..."
            rm -rf "$TASK_DIR"
            
            if ! git clone --depth=50 --branch="$BASE_BRANCH" "$REPO_URL" "$TASK_DIR" 2>&1; then
                ERROR="Failed to clone repository"
                curl -s -X POST "$COORD/api/complete" \
                    -H "Content-Type: application/json" \
                    -d "$(jq -n --arg tid "$TASK_ID" --arg wid "$WORKER_ID" --arg err "$ERROR" \
                        '{task_id: $tid, worker_id: $wid, error: $err}')"
                continue
            fi
            
            cd "$TASK_DIR"
            
            # Create task branch
            if [ -n "$BRANCH_NAME" ]; then
                git checkout -b "$BRANCH_NAME"
            fi
            
            # Configure git for commits
            git config user.email "shelley-worker@exe.dev"
            git config user.name "Shelley Worker ($WORKER_ID)"
        else
            # No repo - work in a temp directory
            TASK_DIR="$WORKDIR/$TASK_ID"
            mkdir -p "$TASK_DIR"
            cd "$TASK_DIR"
        fi
        
        echo "Running shelley in $(pwd)..."
        
        # Run shelley with the prompt
        OUTPUT=$(shelley -config ~/.config/shelley/shelley.json chat -yes -no-sync -prompt "$PROMPT" 2>&1) || true
        
        # If we have a repo, commit and push changes
        if [ -n "$REPO_URL" ] && [ -n "$BRANCH_NAME" ]; then
            echo "Checking for changes..."
            
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
        
        # Report completion
        curl -s -X POST "$COORD/api/complete" \
            -H "Content-Type: application/json" \
            -d "$(jq -n --arg tid "$TASK_ID" --arg wid "$WORKER_ID" --arg out "$OUTPUT" \
                       --arg sha "$COMMIT_SHA" --arg err "$ERROR" \
                '{task_id: $tid, worker_id: $wid, result: $out, commit_sha: $sha, error: $err}')"
        
        echo "Task $TASK_ID completed"
        
        # Cleanup
        cd "$HOME"
        rm -rf "$TASK_DIR"
    else
        IDLE_COUNT=$((IDLE_COUNT + 1))
        if [ $IDLE_COUNT -ge $MAX_IDLE ]; then
            echo "Idle timeout, shutting down"
            curl -s -X POST "$COORD/api/worker-shutdown?worker=$WORKER_ID"
            exit 0
        fi
        sleep 5
    fi
done
`, c.config.CoordHost, c.config.Port, workerID)

	scriptCmd := exec.Command("ssh", "-o", "ConnectTimeout=30", "-o", "StrictHostKeyChecking=no",
		workerHost, "tee", "/tmp/worker-loop.sh")
	scriptCmd.Stdin = strings.NewReader(pollScript)
	scriptCmd.Run()

	exec.Command("ssh", "-o", "StrictHostKeyChecking=no", workerHost,
		"chmod", "+x", "/tmp/worker-loop.sh").Run()

	go func() {
		cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ServerAliveInterval=30",
			workerHost, "nohup", "/tmp/worker-loop.sh", ">", "/tmp/worker.log", "2>&1", "&")
		cmd.Run()
	}()

	c.db.Exec(`UPDATE workers SET status = 'idle', last_heartbeat = CURRENT_TIMESTAMP WHERE id = ?`, workerID)
	c.LogEvent("worker.ready", "", workerID, nil)
	log.Printf("Worker %s is ready", workerID)
}

// GetWorker retrieves a worker by ID.
func (c *Coordinator) GetWorker(id string) (*Worker, error) {
	var w Worker
	var currentTaskID, tailscaleIP sql.NullString
	var lastHeartbeat sql.NullTime

	err := c.db.QueryRow(`SELECT id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed 
		FROM workers WHERE id = ?`, id).Scan(
		&w.ID, &w.Status, &currentTaskID, &tailscaleIP, &w.CreatedAt, &lastHeartbeat, &w.TasksCompleted)
	if err != nil {
		return nil, err
	}

	if currentTaskID.Valid {
		w.CurrentTaskID = &currentTaskID.String
	}
	if tailscaleIP.Valid {
		w.TailscaleIP = &tailscaleIP.String
	}
	if lastHeartbeat.Valid {
		w.LastHeartbeat = &lastHeartbeat.Time
	}

	return &w, nil
}

// DeleteWorker removes a worker VM.
func (c *Coordinator) DeleteWorker(workerID string) error {
	log.Printf("Deleting worker: %s", workerID)
	cmd := exec.Command("ssh", "exe.dev", "rm", workerID)
	cmd.Run()

	c.db.Exec(`UPDATE workers SET status = 'deleted' WHERE id = ?`, workerID)
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
func (c *Coordinator) ListWorkers() ([]Worker, error) {
	rows, err := c.db.Query(`SELECT id, status, current_task_id, tailscale_ip, created_at, last_heartbeat, tasks_completed 
		FROM workers WHERE status != 'deleted' ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []Worker
	for rows.Next() {
		var w Worker
		var currentTaskID, tailscaleIP, lastHeartbeat interface{}
		rows.Scan(&w.ID, &w.Status, &currentTaskID, &tailscaleIP, &w.CreatedAt, &lastHeartbeat, &w.TasksCompleted)
		workers = append(workers, w)
	}

	return workers, nil
}
