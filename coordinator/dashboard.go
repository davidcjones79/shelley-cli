// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
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
	MaxWorkers    int
	WorkerPrefix  string
	CoordHost     string
	APIToken      string
	GitToken      string
	GitUser       string
	ShelleyDB     string // Path to main shelley DB for syncing conversations
	InstallScript string // URL to install script for workers
}

// Dashboard manages the coordinator subprocess and serves the web UI.
type Dashboard struct {
	config    DashboardConfig
	mu        sync.RWMutex
	cmd       *exec.Cmd
	running   bool
	logs      []string
	maxLogs   int
	proxy     *httputil.ReverseProxy
	apiToken  string
}

// NewDashboard creates a new dashboard instance.
func NewDashboard(cfg DashboardConfig) *Dashboard {
	// Set up reverse proxy to coordinator
	coordURL, _ := url.Parse(fmt.Sprintf("http://localhost:%d", cfg.CoordPort))
	proxy := httputil.NewSingleHostReverseProxy(coordURL)
	
	// Custom error handler for when coordinator is down
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Coordinator not running",
		})
	}

	return &Dashboard{
		config:   cfg,
		maxLogs:  1000,
		proxy:    proxy,
		apiToken: cfg.APIToken,
	}
}

// Start begins the coordinator subprocess.
func (d *Dashboard) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return fmt.Errorf("coordinator already running")
	}

	// Build command
	args := []string{
		"coord",
		"-port", fmt.Sprintf("%d", d.config.CoordPort),
		"-db", d.config.CoordDBPath,
		"-max-workers", fmt.Sprintf("%d", d.config.MaxWorkers),
		"-prefix", d.config.WorkerPrefix,
	}
	if d.config.CoordHost != "" {
		args = append(args, "-host", d.config.CoordHost)
	}
	if d.apiToken != "" {
		args = append(args, "-token", d.apiToken)
	}
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running": d.IsRunning(),
		"logs":    d.GetLogs(),
	})
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

// SetupRoutes configures HTTP routes for the dashboard.
func (d *Dashboard) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", d.HandleDashboardIndex)
	mux.HandleFunc("/conversation/", d.HandleConversation)
	mux.HandleFunc("/dashboard/start", d.HandleDashboardStart)
	mux.HandleFunc("/dashboard/stop", d.HandleDashboardStop)
	mux.HandleFunc("/dashboard/status", d.HandleDashboardStatus)
	mux.HandleFunc("/api/", d.HandleAPIProxy)
	mux.HandleFunc("/shelley-bin", d.HandleAPIProxy)
}

// Run starts the dashboard HTTP server.
func (d *Dashboard) Run() error {
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
