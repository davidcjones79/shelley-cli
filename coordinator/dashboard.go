// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DashboardConfig holds dashboard configuration.
type DashboardConfig struct {
	Port          int
	CoordPort     int
	CoordDBPath   string
	ShelleyBin    string
	MinWorkers    int
	MaxWorkers    int
	WorkerPrefix  string
	CoordHost     string
	APIToken      string
	GitToken      string
	GitUser       string
	ShelleyDB     string // Path to main shelley DB for syncing conversations
	InstallScript string // URL to install script for workers
	TailscaleAuthKey string // Tailscale auth key for workers
	ArtifactsDir  string // Directory to store quick task artifacts
}

// Dashboard manages the coordinator subprocess and serves the web UI.
// QuickTask represents a local sub-agent task running on the dashboard VM.
type QuickTask struct {
	ID             string     `json:"id"`
	Prompt         string     `json:"prompt"`
	WorkingDir     string     `json:"working_dir"`
	Status         string     `json:"status"` // running, completed, failed, killed
	PID            int        `json:"pid,omitempty"`
	OutputFile     string     `json:"-"` // path to output file (not sent to client)
	Output         string     `json:"output,omitempty"` // tail of output for preview
	Error          *string    `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	HasArtifacts   bool       `json:"has_artifacts,omitempty"` // true if task has collected artifacts
	ConversationID string     `json:"conversation_id,omitempty"` // shelley conversation ID for continuing
	ParentTaskID   string     `json:"parent_task_id,omitempty"` // if continuation, link to parent
	cmd            *exec.Cmd  `json:"-"` // running process
}

type Dashboard struct {
	config    DashboardConfig
	mu        sync.RWMutex
	cmd       *exec.Cmd
	running   bool
	logs      []string
	maxLogs   int
	proxy     *httputil.ReverseProxy
	apiToken  string

	// Quick tasks (local sub-agents)
	quickTasks   map[string]*QuickTask
	quickTasksMu sync.RWMutex

	// Database for quick task persistence
	db           *sql.DB
	artifactsDir string
}

// NewDashboard creates a new dashboard instance.
func NewDashboard(cfg DashboardConfig) *Dashboard {
	// Set up reverse proxy to coordinator
	coordURL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", cfg.CoordPort))
	proxy := httputil.NewSingleHostReverseProxy(coordURL)

	// Enable streaming/WebSocket support
	proxy.FlushInterval = -1 // Flush immediately for WebSocket

	// Custom error handler for when coordinator is down
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Coordinator not running",
		})
	}

	// Set default artifacts directory
	artifactsDir := cfg.ArtifactsDir
	if artifactsDir == "" {
		artifactsDir = filepath.Join(os.TempDir(), "shelley-artifacts")
	}
	os.MkdirAll(artifactsDir, 0755)

	// Open database for quick task persistence
	var db *sql.DB
	if cfg.CoordDBPath != "" {
		var err error
		db, err = sql.Open("sqlite", cfg.CoordDBPath)
		if err != nil {
			log.Printf("Warning: failed to open database for quick tasks: %v", err)
		} else {
			db.Exec("PRAGMA busy_timeout = 5000")
			// Create quick tasks tables if they don't exist
			db.Exec(`CREATE TABLE IF NOT EXISTS quick_tasks (
				id TEXT PRIMARY KEY,
				prompt TEXT NOT NULL,
				working_dir TEXT,
				status TEXT NOT NULL DEFAULT 'running',
				pid INTEGER,
				output_file TEXT,
				error TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				finished_at DATETIME,
				conversation_id TEXT,
				parent_task_id TEXT
			)`)
			db.Exec(`CREATE TABLE IF NOT EXISTS quick_task_artifacts (
				id TEXT PRIMARY KEY,
				quick_task_id TEXT NOT NULL,
				filename TEXT NOT NULL,
				original_path TEXT NOT NULL,
				stored_path TEXT NOT NULL,
				size_bytes INTEGER,
				content_type TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (quick_task_id) REFERENCES quick_tasks(id)
			)`)
			db.Exec(`CREATE INDEX IF NOT EXISTS idx_quick_tasks_status ON quick_tasks(status)`)
			db.Exec(`CREATE INDEX IF NOT EXISTS idx_quick_tasks_created ON quick_tasks(created_at DESC)`)
			db.Exec(`CREATE INDEX IF NOT EXISTS idx_quick_task_artifacts_task ON quick_task_artifacts(quick_task_id)`)
			// Migrations for new columns
			db.Exec(`ALTER TABLE quick_tasks ADD COLUMN conversation_id TEXT`)
			db.Exec(`ALTER TABLE quick_tasks ADD COLUMN parent_task_id TEXT`)
		}
	}

	return &Dashboard{
		config:       cfg,
		maxLogs:      1000,
		proxy:        proxy,
		apiToken:     cfg.APIToken,
		quickTasks:   make(map[string]*QuickTask),
		db:           db,
		artifactsDir: artifactsDir,
	}
}

// Start begins the coordinator subprocess.
func (d *Dashboard) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return fmt.Errorf("coordinator already running")
	}

	// Pre-flight check for coordinator port
	if err := CheckPortAvailable(d.config.CoordPort); err != nil {
		if portErr, ok := err.(*PortInUseError); ok {
			return fmt.Errorf("coordinator port %d is in use by %s (PID %d). Use -coord-port to specify a different port",
				portErr.Port, portErr.Process, portErr.PID)
		}
		return fmt.Errorf("coordinator port %d is not available: %w", d.config.CoordPort, err)
	}

	// Build command
	args := []string{
		"coord",
		"-port", fmt.Sprintf("%d", d.config.CoordPort),
		"-db", d.config.CoordDBPath,
		"-min-workers", fmt.Sprintf("%d", d.config.MinWorkers),
		"-max-workers", fmt.Sprintf("%d", d.config.MaxWorkers),
		"-prefix", d.config.WorkerPrefix,
	}
	if d.config.CoordHost != "" {
		args = append(args, "-host", d.config.CoordHost)
	}
	// Note: Don't pass token - let coordinator load from DB for persistence
	// We'll fetch the token from the coordinator after it starts
	if d.config.GitToken != "" {
		args = append(args, "-git-token", d.config.GitToken)
	}
	if d.config.GitUser != "" {
		args = append(args, "-git-user", d.config.GitUser)
	}
	if d.config.ShelleyDB != "" {
		args = append(args, "-shelley-db", d.config.ShelleyDB)
	}
	if d.config.InstallScript != "" {
		args = append(args, "-install-script", d.config.InstallScript)
	}
	if d.config.TailscaleAuthKey != "" {
		args = append(args, "-tailscale-authkey", d.config.TailscaleAuthKey)
	}

	d.cmd = exec.Command(d.config.ShelleyBin, args...)
	
	// Set process group so we can kill the coordinator when dashboard exits
	d.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	
	// Capture stdout/stderr
	stdout, err := d.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := d.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := d.cmd.Start(); err != nil {
		return fmt.Errorf("start coordinator: %w", err)
	}

	d.running = true
	d.addLogLocked("Coordinator started")

	// Stream logs
	go d.streamLogs("stdout", stdout)
	go d.streamLogs("stderr", stderr)

	// Monitor process
	go func() {
		err := d.cmd.Wait()
		d.mu.Lock()
		d.running = false
		if err != nil {
			d.addLogLocked(fmt.Sprintf("Coordinator exited: %v", err))
		} else {
			d.addLogLocked("Coordinator stopped")
		}
		d.mu.Unlock()
	}()

	// Fetch API token from coordinator DB (it persists tokens now)
	go func() {
		time.Sleep(500 * time.Millisecond) // Give coordinator time to start
		db, err := sql.Open("sqlite", d.config.CoordDBPath)
		if err != nil {
			return
		}
		defer db.Close()
		
		var token string
		if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'api_token'`).Scan(&token); err == nil {
			d.mu.Lock()
			d.apiToken = token
			d.addLogLocked(fmt.Sprintf("API Token: %s", token))
			d.mu.Unlock()
		}
	}()

	return nil
}

// Stop terminates the coordinator subprocess.
func (d *Dashboard) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running || d.cmd == nil {
		return fmt.Errorf("coordinator not running")
	}

	// Kill the entire process group to ensure coordinator and any children die
	pgid, err := syscall.Getpgid(d.cmd.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		// Fallback to just killing the process
		if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
			d.cmd.Process.Kill()
		}
	}

	d.addLogLocked("Stop signal sent")
	return nil
}

// IsRunning returns whether the coordinator is running.
func (d *Dashboard) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.running
}

// GetLogs returns recent log lines.
func (d *Dashboard) GetLogs() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	logs := make([]string, len(d.logs))
	copy(logs, d.logs)
	return logs
}

func (d *Dashboard) addLog(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addLogLocked(msg)
}

func (d *Dashboard) addLogLocked(msg string) {
	timestamp := time.Now().Format("15:04:05")
	d.logs = append(d.logs, fmt.Sprintf("[%s] %s", timestamp, msg))
	if len(d.logs) > d.maxLogs {
		d.logs = d.logs[len(d.logs)-d.maxLogs:]
	}
}

func (d *Dashboard) streamLogs(name string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		d.addLog(scanner.Text())
	}
}

// HTTP Handlers

// HandleDashboardIndex serves the dashboard web UI.
func (d *Dashboard) HandleDashboardIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Require exe.dev authentication (or allow local access)
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		// Check if this is local access (no exe.dev proxy)
		host := r.Host
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
			// Allow local access without auth
			userID = "local"
		} else {
			// Redirect to exe.dev login
			http.Redirect(w, r, "/__exe.dev/login?redirect=/", http.StatusFound)
			return
		}
	}

	data, err := dashboardHTML.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "Dashboard not found", http.StatusInternalServerError)
		return
	}

	// Inject API token for authenticated users
	data = bytes.Replace(data,
		[]byte(`let token = localStorage.getItem('coordToken') || '';`),
		[]byte(`let token = localStorage.getItem('coordToken') || '`+d.apiToken+`';`),
		1)

	// Inject coordinator port for direct WebSocket connection
	data = bytes.Replace(data,
		[]byte(`const coordPort = window.COORD_PORT || 8081;`),
		[]byte(fmt.Sprintf(`const coordPort = %d;`, d.config.CoordPort)),
		1)

	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

// HandleDashboardStart starts the coordinator.
func (d *Dashboard) HandleDashboardStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if err := d.Start(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Wait a moment for startup
	time.Sleep(500 * time.Millisecond)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"running": d.IsRunning()})
}

// HandleDashboardStop stops the coordinator.
func (d *Dashboard) HandleDashboardStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if err := d.Stop(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"stopping": true})
}

// HandleDashboardStatus returns coordinator status.
func (d *Dashboard) HandleDashboardStatus(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	token := d.apiToken
	d.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running": d.IsRunning(),
		"logs":    d.GetLogs(),
		"token":   token,
	})
}

// HandleHelp serves the help documentation page.
func (d *Dashboard) HandleHelp(w http.ResponseWriter, r *http.Request) {
	// Require exe.dev authentication (or allow local access)
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		host := r.Host
		if !strings.HasPrefix(host, "localhost") && !strings.HasPrefix(host, "127.0.0.1") {
			http.Redirect(w, r, "/__exe.dev/login?redirect=/help", http.StatusFound)
			return
		}
	}

	data := PageData{
		Title:    "Help",
		Page:     "help",
		APIToken: d.apiToken,
	}

	tmpl, err := template.ParseFS(templatesFS,
		"templates/base.html",
		"templates/help.html",
	)
	if err != nil {
		http.Error(w, "Template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleSettingsPage serves the settings configuration page.
func (d *Dashboard) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	// Require exe.dev authentication (or allow local access)
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		host := r.Host
		if !strings.HasPrefix(host, "localhost") && !strings.HasPrefix(host, "127.0.0.1") {
			http.Redirect(w, r, "/__exe.dev/login?redirect=/settings", http.StatusFound)
			return
		}
	}

	data := PageData{
		Title:    "Settings",
		Page:     "settings",
		APIToken: d.apiToken,
	}

	tmpl, err := template.ParseFS(templatesFS,
		"templates/base.html",
		"templates/settings.html",
	)
	if err != nil {
		http.Error(w, "Template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// HandleConversation serves a synced conversation from the main Shelley DB.
func (d *Dashboard) HandleConversation(w http.ResponseWriter, r *http.Request) {
	// Extract conversation ID from path: /conversation/{id}
	path := strings.TrimPrefix(r.URL.Path, "/conversation/")
	convID := strings.TrimSuffix(path, "/")
	if convID == "" {
		http.Error(w, "Conversation ID required", http.StatusBadRequest)
		return
	}

	if d.config.ShelleyDB == "" {
		http.Error(w, "Shelley DB not configured", http.StatusServiceUnavailable)
		return
	}

	db, err := sql.Open("sqlite", d.config.ShelleyDB)
	if err != nil {
		http.Error(w, "Failed to open database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Get conversation metadata
	var slug, cwd sql.NullString
	var createdAt, updatedAt string
	err = db.QueryRow(`SELECT slug, cwd, created_at, updated_at FROM conversations WHERE conversation_id = ?`, convID).Scan(&slug, &cwd, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get messages
	rows, err := db.Query(`SELECT sequence_id, type, llm_data, user_data FROM messages WHERE conversation_id = ? ORDER BY sequence_id`, convID)
	if err != nil {
		http.Error(w, "Failed to fetch messages: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Message struct {
		SequenceID int
		Type       string
		Content    string
	}
	var messages []Message

	for rows.Next() {
		var seqID int
		var msgType string
		var llmData, userData sql.NullString
		if err := rows.Scan(&seqID, &msgType, &llmData, &userData); err != nil {
			continue
		}

		// Extract content from LLM data
		var content string
		if llmData.Valid && llmData.String != "" {
			var llm struct {
				Content []struct {
					Type      int             `json:"Type"`
					Text      string          `json:"Text"`
					ToolName  string          `json:"ToolName"`
					ToolInput json.RawMessage `json:"ToolInput"`
				} `json:"Content"`
			}
			if err := json.Unmarshal([]byte(llmData.String), &llm); err == nil {
				for _, c := range llm.Content {
					if c.Text != "" {
						content += c.Text + "\n"
					}
					if c.ToolName != "" {
						content += fmt.Sprintf("[Tool: %s]\n%s\n", c.ToolName, string(c.ToolInput))
					}
				}
			}
		}
		if content == "" && userData.Valid {
			var user struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(userData.String), &user); err == nil {
				content = user.Text
			}
		}

		messages = append(messages, Message{
			SequenceID: seqID,
			Type:       msgType,
			Content:    strings.TrimSpace(content),
		})
	}

	// Render HTML
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Conversation %s</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { 
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #0d1117; color: #c9d1d9; line-height: 1.6;
            padding: 20px; max-width: 900px; margin: 0 auto;
        }
        h1 { font-size: 1.5em; margin-bottom: 10px; color: #58a6ff; }
        .meta { color: #8b949e; font-size: 0.9em; margin-bottom: 20px; }
        .message { margin: 16px 0; padding: 16px; border-radius: 8px; }
        .message.user { background: #161b22; border-left: 3px solid #58a6ff; }
        .message.agent { background: #1c2128; border-left: 3px solid #3fb950; }
        .message.tool { background: #1a1f24; border-left: 3px solid #d29922; font-family: monospace; }
        .message-header { font-weight: 600; margin-bottom: 8px; font-size: 0.85em; text-transform: uppercase; }
        .message.user .message-header { color: #58a6ff; }
        .message.agent .message-header { color: #3fb950; }
        .message.tool .message-header { color: #d29922; }
        .message-content { white-space: pre-wrap; word-break: break-word; }
        .back-link { display: inline-block; margin-bottom: 20px; color: #58a6ff; text-decoration: none; }
        .back-link:hover { text-decoration: underline; }
        code { background: #262c36; padding: 2px 6px; border-radius: 4px; font-size: 0.9em; }
    </style>
</head>
<body>
    <a href="/" class="back-link">← Back to Dashboard</a>
    <h1>Conversation</h1>
    <div class="meta">
        <div>ID: <code>%s</code></div>
        <div>Slug: %s</div>
        <div>Created: %s</div>
    </div>
`, html.EscapeString(convID), html.EscapeString(convID), html.EscapeString(slug.String), html.EscapeString(createdAt))

	for _, msg := range messages {
		fmt.Fprintf(w, `    <div class="message %s">
        <div class="message-header">%s</div>
        <div class="message-content">%s</div>
    </div>
`, html.EscapeString(msg.Type), html.EscapeString(msg.Type), html.EscapeString(msg.Content))
	}

	fmt.Fprintf(w, `</body>
</html>`)
}

// HandleAPIProxy proxies requests to the coordinator.
func (d *Dashboard) HandleAPIProxy(w http.ResponseWriter, r *http.Request) {
	if !d.IsRunning() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Coordinator not running",
		})
		return
	}
	d.proxy.ServeHTTP(w, r)
}

// HandleLocalSkills returns a list of locally installed skills.
func (d *Dashboard) HandleLocalSkills(w http.ResponseWriter, r *http.Request) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "Failed to get home directory", http.StatusInternalServerError)
		return
	}

	skillsDir := filepath.Join(homeDir, ".config", "shelley", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		// No local skills directory, return empty list
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	var skills []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check if SKILL.md exists
		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			continue
		}

		// Get skill info
		skill := map[string]interface{}{
			"name":  entry.Name(),
			"local": true,
		}

		// Parse SKILL.md frontmatter for parallel_friendly
		if content, err := os.ReadFile(skillPath); err == nil {
			lines := strings.Split(string(content), "\n")
			inFrontmatter := false
			for _, line := range lines {
				if line == "---" {
					if inFrontmatter {
						break // End of frontmatter
					}
					inFrontmatter = true
					continue
				}
				if inFrontmatter && strings.HasPrefix(line, "parallel_friendly:") {
					val := strings.TrimSpace(strings.TrimPrefix(line, "parallel_friendly:"))
					skill["parallel_friendly"] = val == "true"
					break
				}
			}
		}

		// Check for reference files
		refDir := filepath.Join(skillsDir, entry.Name(), "reference")
		if refs, err := os.ReadDir(refDir); err == nil {
			var refList []map[string]interface{}
			for _, ref := range refs {
				if ref.IsDir() {
					continue
				}
				info, _ := ref.Info()
				refList = append(refList, map[string]interface{}{
					"name": ref.Name(),
					"size": info.Size(),
				})
			}
			skill["references"] = refList
		}

		skills = append(skills, skill)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skills)
}

// HandleLocalSkillContent returns the content of a local skill.
func (d *Dashboard) HandleLocalSkillContent(w http.ResponseWriter, r *http.Request) {
	// Parse path: /local-skills/{skill-name} or /local-skills/{skill-name}/reference/{file}
	path := strings.TrimPrefix(r.URL.Path, "/local-skills/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Skill name required", http.StatusBadRequest)
		return
	}

	skillName := parts[0]
	homeDir, _ := os.UserHomeDir()
	skillsDir := filepath.Join(homeDir, ".config", "shelley", "skills")

	// Security: ensure skill name doesn't contain path traversal
	if strings.Contains(skillName, "..") || strings.Contains(skillName, "/") {
		http.Error(w, "Invalid skill name", http.StatusBadRequest)
		return
	}

	var filePath string
	if len(parts) >= 3 && parts[1] == "reference" {
		// Reference file
		refName := parts[2]
		if strings.Contains(refName, "..") || strings.Contains(refName, "/") {
			http.Error(w, "Invalid reference name", http.StatusBadRequest)
			return
		}
		filePath = filepath.Join(skillsDir, skillName, "reference", refName)
	} else {
		// Main SKILL.md
		filePath = filepath.Join(skillsDir, skillName, "SKILL.md")
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		http.Error(w, "Skill not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

// HandleSettings handles GET/POST for coordinator settings.
func (d *Dashboard) HandleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		d.handleSettingsGet(w, r)
	} else if r.Method == "POST" {
		d.handleSettingsPost(w, r)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *Dashboard) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	// Get Tailscale IP if connected
	tailscaleIP := ""
	if out, err := exec.Command("tailscale", "ip", "-4").Output(); err == nil {
		tailscaleIP = strings.TrimSpace(string(out))
	}

	response := map[string]interface{}{
		"coord_host":         d.config.CoordHost,
		"tailscale_ip":       tailscaleIP,
		"tailscale_key_set":  d.config.TailscaleAuthKey != "",
		"github_token_set":   d.config.GitToken != "",
		"worker_prefix":      d.config.WorkerPrefix,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (d *Dashboard) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TailscaleKey string `json:"tailscale_key"`
		GitHubToken  string `json:"github_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	restartRequired := false

	// Save settings to file
	settings := d.loadSettingsFile()

	if req.TailscaleKey != "" {
		settings["tailscale_key"] = req.TailscaleKey
		d.config.TailscaleAuthKey = req.TailscaleKey
		restartRequired = true
	}

	if req.GitHubToken != "" {
		settings["github_token"] = req.GitHubToken
		d.config.GitToken = req.GitHubToken
		restartRequired = true
	}

	// Save to file
	if err := d.saveSettingsFile(settings); err != nil {
		http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"status":           "ok",
		"restart_required": restartRequired,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (d *Dashboard) getSettingsPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".config", "shelley", "coordinator-settings.json")
}

func (d *Dashboard) loadSettingsFile() map[string]string {
	settings := make(map[string]string)
	data, err := os.ReadFile(d.getSettingsPath())
	if err != nil {
		return settings
	}
	json.Unmarshal(data, &settings)
	return settings
}

func (d *Dashboard) saveSettingsFile(settings map[string]string) error {
	path := d.getSettingsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadSavedSettings loads settings from the settings file and applies them to config.
func (d *Dashboard) LoadSavedSettings() {
	settings := d.loadSettingsFile()
	if key, ok := settings["tailscale_key"]; ok && key != "" {
		d.config.TailscaleAuthKey = key
	}
	if token, ok := settings["github_token"]; ok && token != "" {
		d.config.GitToken = token
	}
}

// Quick Task Handlers

// HandleQuickTasks returns list of quick tasks or creates a new one.
// loadQuickTasksFromDB loads persisted quick tasks from the database on startup.
func (d *Dashboard) loadQuickTasksFromDB() {
	if d.db == nil {
		return
	}

	rows, err := d.db.Query(`
		SELECT id, prompt, working_dir, status, pid, output_file, error, created_at, finished_at, conversation_id, parent_task_id
		FROM quick_tasks
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		log.Printf("Failed to load quick tasks: %v", err)
		return
	}
	defer rows.Close()

	d.quickTasksMu.Lock()
	defer d.quickTasksMu.Unlock()

	for rows.Next() {
		var task QuickTask
		var errorStr, outputFile, workingDir, conversationID, parentTaskID sql.NullString
		var finishedAt sql.NullTime
		var pid sql.NullInt64

		err := rows.Scan(&task.ID, &task.Prompt, &workingDir, &task.Status,
			&pid, &outputFile, &errorStr, &task.CreatedAt, &finishedAt, &conversationID, &parentTaskID)
		if err != nil {
			log.Printf("Failed to scan quick task: %v", err)
			continue
		}

		if workingDir.Valid {
			task.WorkingDir = workingDir.String
		}
		if pid.Valid {
			task.PID = int(pid.Int64)
		}
		if outputFile.Valid {
			task.OutputFile = outputFile.String
		}
		if errorStr.Valid {
			task.Error = &errorStr.String
		}
		if finishedAt.Valid {
			task.FinishedAt = &finishedAt.Time
		}
		if conversationID.Valid {
			task.ConversationID = conversationID.String
		}
		if parentTaskID.Valid {
			task.ParentTaskID = parentTaskID.String
		}

		// Mark interrupted running tasks as failed
		if task.Status == "running" {
			task.Status = "failed"
			errMsg := "Task was interrupted by dashboard restart"
			task.Error = &errMsg
			now := time.Now()
			task.FinishedAt = &now
			d.db.Exec(`UPDATE quick_tasks SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
				task.Status, errMsg, now, task.ID)
		}

		// Check if task has artifacts
		if d.db != nil {
			var count int
			d.db.QueryRow(`SELECT COUNT(*) FROM quick_task_artifacts WHERE quick_task_id = ?`, task.ID).Scan(&count)
			task.HasArtifacts = count > 0
		}

		d.quickTasks[task.ID] = &task
	}

	log.Printf("Loaded %d quick tasks from database", len(d.quickTasks))
}

func (d *Dashboard) HandleQuickTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.listQuickTasks(w, r)
	case http.MethodPost:
		d.createQuickTask(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *Dashboard) listQuickTasks(w http.ResponseWriter, r *http.Request) {
	d.quickTasksMu.RLock()
	tasks := make([]*QuickTask, 0, len(d.quickTasks))
	for _, t := range d.quickTasks {
		// Update output preview for running tasks
		if t.Status == "running" {
			d.updateTaskOutput(t)
		}
		// Check for artifacts if we have a DB
		if d.db != nil && !t.HasArtifacts {
			var count int
			d.db.QueryRow(`SELECT COUNT(*) FROM quick_task_artifacts WHERE quick_task_id = ?`, t.ID).Scan(&count)
			t.HasArtifacts = count > 0
		}
		tasks = append(tasks, t)
	}
	d.quickTasksMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (d *Dashboard) createQuickTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt         string `json:"prompt"`
		WorkingDir     string `json:"working_dir"`
		ConversationID string `json:"conversation_id"` // for continuing a conversation
		ParentTaskID   string `json:"parent_task_id"`  // link to parent task
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	// Default working dir to home
	if req.WorkingDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			req.WorkingDir = home
		}
	}

	// Expand ~ in path
	if strings.HasPrefix(req.WorkingDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			req.WorkingDir = filepath.Join(home, req.WorkingDir[2:])
		}
	}

	taskID := fmt.Sprintf("quick-%d", time.Now().UnixNano())
	outputFile := filepath.Join(os.TempDir(), fmt.Sprintf("shelley-quick-%s.log", taskID))

	task := &QuickTask{
		ID:             taskID,
		Prompt:         req.Prompt,
		WorkingDir:     req.WorkingDir,
		Status:         "running",
		OutputFile:     outputFile,
		CreatedAt:      time.Now(),
		ConversationID: req.ConversationID,
		ParentTaskID:   req.ParentTaskID,
	}

	// Find shelley binary
	shelleyBin := "shelley"
	if path, err := exec.LookPath("shelley"); err == nil {
		shelleyBin = path
	} else {
		// Try common locations
		for _, p := range []string{
			"/home/exedev/shelley-cli/bin/shelley",
			"/usr/local/bin/shelley",
			filepath.Join(os.Getenv("HOME"), ".local/bin/shelley"),
		} {
			if _, err := os.Stat(p); err == nil {
				shelleyBin = p
				break
			}
		}
	}

	// Create output file
	outFile, err := os.Create(outputFile)
	if err != nil {
		http.Error(w, "Failed to create output file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build shelley command arguments
	// Note: We use -sync (default) to save conversations for the continue feature
	// Use the global shelley DB so conversations are available in the web client
	shelleyDBPath := d.config.ShelleyDB
	if shelleyDBPath == "" {
		shelleyDBPath = filepath.Join(os.Getenv("HOME"), ".config/shelley/shelley.db")
	}

	var cmd *exec.Cmd
	if req.ConversationID != "" {
		// Continue existing conversation
		cmd = exec.Command(shelleyBin, "-db", shelleyDBPath, "chat", "-yes", "-conversation", req.ConversationID, "-prompt", req.Prompt)
	} else {
		// Start new conversation (sync enabled so we can continue later)
		cmd = exec.Command(shelleyBin, "-db", shelleyDBPath, "chat", "-yes", "-prompt", req.Prompt)
	}
	cmd.Dir = req.WorkingDir
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	if err := cmd.Start(); err != nil {
		outFile.Close()
		errMsg := err.Error()
		task.Status = "failed"
		task.Error = &errMsg
		now := time.Now()
		task.FinishedAt = &now
	} else {
		task.PID = cmd.Process.Pid
		task.cmd = cmd

		// Monitor process in background
		go d.monitorQuickTask(task, outFile)
	}

	d.quickTasksMu.Lock()
	d.quickTasks[taskID] = task
	d.quickTasksMu.Unlock()

	// Persist to database
	if d.db != nil {
		d.db.Exec(`INSERT INTO quick_tasks (id, prompt, working_dir, status, pid, output_file, created_at, conversation_id, parent_task_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.ID, task.Prompt, task.WorkingDir, task.Status, task.PID, task.OutputFile, task.CreatedAt, task.ConversationID, task.ParentTaskID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (d *Dashboard) monitorQuickTask(task *QuickTask, outFile *os.File) {
	defer outFile.Close()

	err := task.cmd.Wait()

	d.quickTasksMu.Lock()
	now := time.Now()
	task.FinishedAt = &now
	if err != nil {
		task.Status = "failed"
		errMsg := err.Error()
		task.Error = &errMsg
	} else {
		task.Status = "completed"
	}
	d.updateTaskOutput(task)

	// Look up conversation ID from shelley database (if not already set from continuation)
	if task.ConversationID == "" {
		task.ConversationID = d.findConversationID(task)
	}

	// Persist status change to database
	if d.db != nil {
		var errStr interface{} = nil
		if task.Error != nil {
			errStr = *task.Error
		}
		d.db.Exec(`UPDATE quick_tasks SET status = ?, error = ?, finished_at = ?, conversation_id = ? WHERE id = ?`,
			task.Status, errStr, task.FinishedAt, task.ConversationID, task.ID)
	}

	// Collect artifacts after task completes (only for successful tasks)
	if task.Status == "completed" {
		d.collectQuickTaskArtifacts(task)
	}

	d.quickTasksMu.Unlock()
}

// findConversationID looks up the conversation ID from shelley's database.
// It finds the most recent conversation created in the task's working directory
// around the task's start time.
func (d *Dashboard) findConversationID(task *QuickTask) string {
	// Use the global shelley DB (same one quick tasks write to)
	shelleyDBPath := d.config.ShelleyDB
	if shelleyDBPath == "" {
		shelleyDBPath = filepath.Join(os.Getenv("HOME"), ".config/shelley/shelley.db")
	}

	shelleyDB, err := sql.Open("sqlite", shelleyDBPath)
	if err != nil {
		log.Printf("Failed to open shelley database at %s: %v", shelleyDBPath, err)
		return ""
	}
	defer shelleyDB.Close()

	// Find the most recent conversation that:
	// 1. Was created after the task started
	// 2. Has matching working directory (cwd)
	var conversationID string
	err = shelleyDB.QueryRow(`
		SELECT conversation_id FROM conversations
		WHERE cwd = ? AND created_at >= ?
		ORDER BY created_at DESC
		LIMIT 1
	`, task.WorkingDir, task.CreatedAt.Add(-time.Second)).Scan(&conversationID)

	if err != nil {
		// Try without cwd filter if that fails
		err = shelleyDB.QueryRow(`
			SELECT conversation_id FROM conversations
			WHERE created_at >= ?
			ORDER BY created_at DESC
			LIMIT 1
		`, task.CreatedAt.Add(-time.Second)).Scan(&conversationID)
		if err != nil {
			log.Printf("Failed to find conversation ID in %s: %v", shelleyDBPath, err)
			return ""
		}
	}

	log.Printf("Found conversation ID %s for quick task %s in %s", conversationID, task.ID, shelleyDBPath)
	return conversationID
}

func (d *Dashboard) updateTaskOutput(task *QuickTask) {
	// Read last 2KB of output file for preview
	if task.OutputFile == "" {
		return
	}
	f, err := os.Open(task.OutputFile)
	if err != nil {
		return
	}
	defer f.Close()

	// Seek to last 2KB
	info, _ := f.Stat()
	offset := int64(0)
	if info.Size() > 2048 {
		offset = info.Size() - 2048
	}
	f.Seek(offset, 0)

	buf := make([]byte, 2048)
	n, _ := f.Read(buf)
	if n > 0 {
		task.Output = string(buf[:n])
		// If we started mid-stream, skip to first newline
		if offset > 0 {
			if idx := strings.Index(task.Output, "\n"); idx >= 0 {
				task.Output = task.Output[idx+1:]
			}
		}
	}
}

// collectQuickTaskArtifacts scans the task's working directory for files created/modified
// during the task execution and copies eligible files to the dashboard's artifact storage.
func (d *Dashboard) collectQuickTaskArtifacts(task *QuickTask) {
	if d.db == nil || task.WorkingDir == "" {
		return
	}

	// Eligible file extensions and their content types
	validExtensions := map[string]string{
		".md":   "text/markdown",
		".html": "text/html",
		".htm":  "text/html",
		".jsx":  "text/javascript",
		".tsx":  "text/typescript",
		".json": "application/json",
		".txt":  "text/plain",
		".css":  "text/css",
		".svg":  "image/svg+xml",
		".js":   "text/javascript",
		".ts":   "text/typescript",
		".py":   "text/x-python",
		".go":   "text/x-go",
		".sh":   "text/x-shellscript",
		".yaml": "text/yaml",
		".yml":  "text/yaml",
	}

	// Directories to skip
	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"vendor":       true,
		".next":        true,
		"dist":         true,
		"build":        true,
		"__pycache__":  true,
		".venv":        true,
		"venv":         true,
	}

	// Create task-specific storage directory
	taskArtifactsDir := filepath.Join(d.artifactsDir, task.ID)
	os.MkdirAll(taskArtifactsDir, 0755)

	// Use task creation time as threshold (with 1 second buffer)
	threshold := task.CreatedAt.Add(-time.Second)

	var artifactsCollected int
	filepath.WalkDir(task.WorkingDir, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Skip excluded directories
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		contentType, ok := validExtensions[ext]
		if !ok {
			return nil
		}

		// Get file info
		fileInfo, err := info.Info()
		if err != nil {
			return nil
		}

		// Only collect files modified after the task started
		if fileInfo.ModTime().Before(threshold) {
			return nil
		}

		// Skip files larger than 10MB
		if fileInfo.Size() > 10*1024*1024 {
			return nil
		}

		// Skip the output file itself
		if path == task.OutputFile {
			return nil
		}

		// Copy file to storage
		relPath, _ := filepath.Rel(task.WorkingDir, path)
		destPath := filepath.Join(taskArtifactsDir, relPath)
		os.MkdirAll(filepath.Dir(destPath), 0755)

		if err := copyFile(path, destPath); err != nil {
			log.Printf("Failed to copy artifact %s: %v", path, err)
			return nil
		}

		// Record in database
		artifactID := fmt.Sprintf("qart-%d", time.Now().UnixNano())
		d.db.Exec(`INSERT INTO quick_task_artifacts
			(id, quick_task_id, filename, original_path, stored_path, size_bytes, content_type)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			artifactID, task.ID, filepath.Base(path), relPath, destPath, fileInfo.Size(), contentType)

		artifactsCollected++
		return nil
	})

	if artifactsCollected > 0 {
		task.HasArtifacts = true
		log.Printf("Collected %d artifacts for quick task %s", artifactsCollected, task.ID)
	}
}

// QuickTaskArtifact represents an artifact from a quick task.
type QuickTaskArtifact struct {
	ID           string    `json:"id"`
	QuickTaskID  string    `json:"quick_task_id"`
	Filename     string    `json:"filename"`
	OriginalPath string    `json:"original_path"`
	SizeBytes    *int64    `json:"size_bytes,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// HandleQuickTaskArtifacts lists artifacts for a quick task.
func (d *Dashboard) HandleQuickTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	if d.db == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]QuickTaskArtifact{})
		return
	}

	rows, err := d.db.Query(`
		SELECT id, quick_task_id, filename, original_path, size_bytes, content_type, created_at
		FROM quick_task_artifacts
		WHERE quick_task_id = ?
		ORDER BY created_at
	`, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var artifacts []QuickTaskArtifact
	for rows.Next() {
		var a QuickTaskArtifact
		var sizeBytes sql.NullInt64
		var contentType sql.NullString

		err := rows.Scan(&a.ID, &a.QuickTaskID, &a.Filename, &a.OriginalPath,
			&sizeBytes, &contentType, &a.CreatedAt)
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

	if artifacts == nil {
		artifacts = []QuickTaskArtifact{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(artifacts)
}

// HandleQuickTaskArtifactDownload serves an artifact file.
func (d *Dashboard) HandleQuickTaskArtifactDownload(w http.ResponseWriter, r *http.Request) {
	artifactID := r.URL.Query().Get("id")
	if artifactID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	if d.db == nil {
		http.Error(w, "Database not available", http.StatusServiceUnavailable)
		return
	}

	var storedPath, filename, contentType string
	err := d.db.QueryRow(`
		SELECT stored_path, filename, content_type
		FROM quick_task_artifacts
		WHERE id = ?
	`, artifactID).Scan(&storedPath, &filename, &contentType)

	if err == sql.ErrNoRows {
		http.Error(w, "Artifact not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Security: ensure path is within artifacts directory
	absPath, _ := filepath.Abs(storedPath)
	absArtifactsDir, _ := filepath.Abs(d.artifactsDir)
	if !strings.HasPrefix(absPath, absArtifactsDir) {
		http.Error(w, "Invalid artifact path", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(storedPath)
	if err != nil {
		http.Error(w, "Failed to read artifact", http.StatusInternalServerError)
		return
	}

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	// Support download parameter to force download instead of inline view
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	}
	w.Write(data)
}

// HandleQuickTask handles individual quick task operations (get, kill).
func (d *Dashboard) HandleQuickTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	d.quickTasksMu.RLock()
	task, exists := d.quickTasks[taskID]
	d.quickTasksMu.RUnlock()

	if !exists {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Return task with full output
		d.quickTasksMu.Lock()
		d.updateTaskOutput(task)
		d.quickTasksMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)

	case http.MethodDelete:
		// Kill task if running
		d.quickTasksMu.Lock()
		if task.Status == "running" && task.cmd != nil && task.cmd.Process != nil {
			task.cmd.Process.Kill()
			task.Status = "killed"
			now := time.Now()
			task.FinishedAt = &now
		}
		d.quickTasksMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(task)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleQuickTaskOutput streams the full output of a quick task.
func (d *Dashboard) HandleQuickTaskOutput(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	d.quickTasksMu.RLock()
	task, exists := d.quickTasks[taskID]
	d.quickTasksMu.RUnlock()

	if !exists {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain")

	if task.OutputFile == "" {
		return
	}

	data, err := os.ReadFile(task.OutputFile)
	if err != nil {
		http.Error(w, "Failed to read output", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

// HandleClearQuickTasks removes completed/failed/killed quick tasks.
func (d *Dashboard) HandleClearQuickTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	d.quickTasksMu.Lock()
	for id, task := range d.quickTasks {
		if task.Status != "running" {
			// Clean up output file
			if task.OutputFile != "" {
				os.Remove(task.OutputFile)
			}
			// Clean up artifacts directory
			taskArtifactsDir := filepath.Join(d.artifactsDir, task.ID)
			os.RemoveAll(taskArtifactsDir)
			// Delete from database
			if d.db != nil {
				d.db.Exec(`DELETE FROM quick_task_artifacts WHERE quick_task_id = ?`, id)
				d.db.Exec(`DELETE FROM quick_tasks WHERE id = ?`, id)
			}
			delete(d.quickTasks, id)
		}
	}
	d.quickTasksMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleListDirs returns directory listing for the directory picker.
func (d *Dashboard) HandleListDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	
	// Default to home directory
	if path == "" {
		var err error
		path, err = os.UserHomeDir()
		if err != nil {
			http.Error(w, "Failed to get home directory", http.StatusInternalServerError)
			return
		}
	}
	
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		}
	}
	
	// Clean and resolve the path
	path = filepath.Clean(path)
	
	// Check if path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, "Path not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if !info.IsDir() {
		http.Error(w, "Path is not a directory", http.StatusBadRequest)
		return
	}
	
	// Read directory entries
	entries, err := os.ReadDir(path)
	if err != nil {
		http.Error(w, "Failed to read directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	type DirEntry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
	}
	
	var dirs []DirEntry
	for _, entry := range entries {
		// Skip hidden files/dirs (starting with .)
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dirs = append(dirs, DirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
		})
	}
	
	// Sort: directories first, then files, alphabetically
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i].IsDir != dirs[j].IsDir {
			return dirs[i].IsDir // dirs first
		}
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	
	result := struct {
		Path    string     `json:"path"`
		Parent  string     `json:"parent"`
		Entries []DirEntry `json:"entries"`
	}{
		Path:    path,
		Parent:  filepath.Dir(path),
		Entries: dirs,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// SetupRoutes configures HTTP routes for the dashboard.
func (d *Dashboard) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", d.HandleDashboardIndex)
	mux.HandleFunc("/help", d.HandleHelp)
	mux.HandleFunc("/settings", d.HandleSettingsPage)
	mux.HandleFunc("/conversation/", d.HandleConversation)
	mux.HandleFunc("/dashboard/start", d.HandleDashboardStart)
	mux.HandleFunc("/dashboard/stop", d.HandleDashboardStop)
	mux.HandleFunc("/dashboard/status", d.HandleDashboardStatus)
	mux.HandleFunc("/api/settings", d.HandleSettings)
	mux.HandleFunc("/api/quick-tasks", d.HandleQuickTasks)
	mux.HandleFunc("/api/quick-task", d.HandleQuickTask)
	mux.HandleFunc("/api/quick-task/output", d.HandleQuickTaskOutput)
	mux.HandleFunc("/api/quick-task/artifacts", d.HandleQuickTaskArtifacts)
	mux.HandleFunc("/api/quick-task/artifact", d.HandleQuickTaskArtifactDownload)
	mux.HandleFunc("/api/quick-tasks/clear", d.HandleClearQuickTasks)
	mux.HandleFunc("/api/list-dirs", d.HandleListDirs)
	mux.HandleFunc("/api/", d.HandleAPIProxy)
	mux.HandleFunc("/shelley-bin", d.HandleAPIProxy)
	mux.HandleFunc("/local-skills", d.HandleLocalSkills)
	mux.HandleFunc("/local-skills/", d.HandleLocalSkillContent)
}

// Run starts the dashboard HTTP server.
func (d *Dashboard) Run() error {
	// Pre-flight checks
	if err := CheckPortAvailable(d.config.Port); err != nil {
		if portErr, ok := err.(*PortInUseError); ok {
			return fmt.Errorf("%s", FormatPreflightError(portErr))
		}
		return fmt.Errorf("port %d is not available: %w", d.config.Port, err)
	}

	// Load persisted quick tasks from database
	d.loadQuickTasksFromDB()

	mux := http.NewServeMux()
	d.SetupRoutes(mux)

	addr := fmt.Sprintf(":%d", d.config.Port)
	log.Printf("Dashboard starting on port %d", d.config.Port)
	log.Printf("Coordinator will run on port %d", d.config.CoordPort)
	log.Printf("API Token: %s", d.apiToken)

	// Set up signal handler to stop coordinator when dashboard is killed
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("Dashboard received shutdown signal, stopping coordinator...")
		d.Stop()
		os.Exit(0)
	}()

	return http.ListenAndServe(addr, mux)
}
