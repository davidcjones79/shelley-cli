// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"sync"
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

	d.cmd = exec.Command(d.config.ShelleyBin, args...)
	
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

	if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
		// If interrupt fails, kill
		d.cmd.Process.Kill()
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

	// Require exe.dev authentication
	userID := r.Header.Get("X-Exedev-Userid")
	if userID == "" {
		http.Redirect(w, r, "/__exe.dev/login?redirect=/", http.StatusFound)
		return
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

	return http.ListenAndServe(addr, mux)
}
