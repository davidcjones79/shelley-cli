package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCoordCLI(args []string) {
	fs := flag.NewFlagSet("coord-cli", flag.ExitOnError)
	token := fs.String("token", "", "Coordinator API token (auto-detected if not provided)")
	port := fs.Int("port", 8081, "Coordinator port (8081 for coord, 8080 for dashboard)")
	
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli [flags] <command>\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  add-task <prompt>   Add a task to the queue\n")
		fmt.Fprintf(os.Stderr, "  tasks               List tasks\n")
		fmt.Fprintf(os.Stderr, "  task <id>           Show task details\n")
		fmt.Fprintf(os.Stderr, "  workers             List workers\n")
		fmt.Fprintf(os.Stderr, "  scale <n>           Scale to n workers\n")
		fmt.Fprintf(os.Stderr, "  drain               Drain all workers\n")
		fmt.Fprintf(os.Stderr, "  kill-worker <id>    Force remove a worker and delete its VM\n")
		fmt.Fprintf(os.Stderr, "  reset-task <id>     Reset a stuck task to queued status\n")
		fmt.Fprintf(os.Stderr, "  stuck               Show stuck/orphaned tasks\n")
		fmt.Fprintf(os.Stderr, "  reset-stuck         Reset all stuck tasks to queued\n")
		fmt.Fprintf(os.Stderr, "  stats               Show coordinator stats\n")
		fmt.Fprintf(os.Stderr, "  clear-tasks         Clear all tasks from the queue\n")
		fmt.Fprintf(os.Stderr, "  clear-workers       Remove all workers\n")
		fmt.Fprintf(os.Stderr, "  clear-all           Clear tasks and workers, reset DB\n")
		fmt.Fprintf(os.Stderr, "  api-help            Show all API endpoints\n")
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		fs.PrintDefaults()
	}

	fs.Parse(args)
	cmdArgs := fs.Args()

	if len(cmdArgs) == 0 {
		fs.Usage()
		os.Exit(1)
	}

	// Auto-detect token if not provided
	if *token == "" {
		// Check environment variable first
		*token = os.Getenv("COORD_TOKEN")
	}
	if *token == "" {
		*token = getCoordinatorToken()
	}
	if *token == "" {
		fmt.Fprintf(os.Stderr, "Error: Could not auto-detect token. Use -token flag or COORD_TOKEN env var.\n")
		os.Exit(1)
	}

	baseURL := fmt.Sprintf("http://localhost:%d", *port)
	client := &http.Client{Timeout: 30 * time.Second}

	cmd := cmdArgs[0]
	switch cmd {
	case "add-task":
		if len(cmdArgs) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli add-task <prompt>\n")
			os.Exit(1)
		}
		prompt := strings.Join(cmdArgs[1:], " ")
		addTask(client, baseURL, *token, prompt)
	case "task":
		if len(cmdArgs) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli task <id>\n")
			os.Exit(1)
		}
		showTask(client, baseURL, *token, cmdArgs[1])
	case "tasks":
		listTasks(client, baseURL, *token)
	case "workers":
		listWorkers(client, baseURL, *token)
	case "scale":
		if len(cmdArgs) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli scale <n>\n")
			os.Exit(1)
		}
		var n int
		fmt.Sscanf(cmdArgs[1], "%d", &n)
		scaleWorkers(client, baseURL, *token, n)
	case "drain":
		drainWorkers(client, baseURL, *token)
	case "kill-worker":
		if len(cmdArgs) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli kill-worker <worker-id>\n")
			os.Exit(1)
		}
		killWorker(client, baseURL, *token, cmdArgs[1])
	case "reset-task":
		if len(cmdArgs) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli reset-task <task-id>\n")
			os.Exit(1)
		}
		resetTask(client, baseURL, *token, cmdArgs[1])
	case "stuck":
		showStuckTasks(client, baseURL, *token)
	case "reset-stuck":
		resetStuckTasks(client, baseURL, *token)
	case "stats":
		showStats(client, baseURL, *token)
	case "clear-tasks":
		clearTasks(client, baseURL, *token)
	case "clear-workers":
		clearWorkers(client, baseURL, *token)
	case "clear-all":
		clearAll(*port)
	case "api-help":
		showAPIHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		fs.Usage()
		os.Exit(1)
	}
}

func apiRequest(client *http.Client, method, url, token string) ([]byte, error) {
	// Add token to URL - use & if URL already has query params, otherwise ?
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	req, err := http.NewRequest(method, url+sep+"token="+token, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func clearTasks(client *http.Client, baseURL, token string) {
	_, err := apiRequest(client, "POST", baseURL+"/api/clear-tasks", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Tasks cleared")
}

func clearWorkers(client *http.Client, baseURL, token string) {
	_, err := apiRequest(client, "POST", baseURL+"/api/drain", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Workers draining")
}

func clearAll(port int) {
	fmt.Println("Stopping coordinator...")
	exec.Command("sudo", "systemctl", "stop", "coordinator").Run()
	
	fmt.Println("Removing database...")
	home := os.Getenv("HOME")
	os.Remove(home + "/coordinator.db")
	os.Remove(home + "/coordinator.db-shm")
	os.Remove(home + "/coordinator.db-wal")
	
	fmt.Println("Starting coordinator...")
	exec.Command("sudo", "systemctl", "start", "coordinator").Run()
	
	// Wait for it to start
	time.Sleep(2 * time.Second)
	
	// Get new token
	newToken := getCoordinatorToken()
	fmt.Println("✅ Coordinator reset")
	fmt.Printf("   New token: %s\n", newToken)
}

func showStats(client *http.Client, baseURL, token string) {
	body, err := apiRequest(client, "GET", baseURL+"/api/stats", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	
	var stats struct {
		Tasks struct {
			Queued    int `json:"queued"`
			Running   int `json:"running"`
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"tasks"`
		Workers struct {
			Total int `json:"total"`
			Busy  int `json:"busy"`
			Idle  int `json:"idle"`
		} `json:"workers"`
	}
	json.Unmarshal(body, &stats)
	
	fmt.Printf("📊 Coordinator Stats\n")
	fmt.Printf("   Workers: %d total (%d busy, %d idle)\n", 
		stats.Workers.Total, stats.Workers.Busy, stats.Workers.Idle)
	fmt.Printf("   Tasks:   %d queued, %d running, %d completed, %d failed\n",
		stats.Tasks.Queued, stats.Tasks.Running, stats.Tasks.Completed, stats.Tasks.Failed)
}

func listWorkers(client *http.Client, baseURL, token string) {
	body, err := apiRequest(client, "GET", baseURL+"/api/workers", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	
	var workers []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(body, &workers)
	
	if len(workers) == 0 {
		fmt.Println("No workers")
		return
	}
	
	// Get list of actual VMs from exe.dev to verify workers exist
	existingVMs := getExeDevVMs()
	
	// Count actual vs stale workers
	actualCount := 0
	for _, w := range workers {
		if existingVMs[w.ID] {
			actualCount++
		}
	}
	
	fmt.Printf("👷 Workers (%d", len(workers))
	if actualCount < len(workers) {
		fmt.Printf(", %d actual", actualCount)
	}
	fmt.Println("):")
	
	for _, w := range workers {
		exists := existingVMs[w.ID]
		icon := "⏳"
		statusStr := w.Status
		
		if !exists {
			// VM doesn't exist on exe.dev
			icon = "👻"
			statusStr = "missing"
		} else {
			switch w.Status {
			case "ready", "idle":
				icon = "✅"
			case "busy":
				icon = "🔨"
			case "failed":
				icon = "❌"
			}
		}
		fmt.Printf("   %s %s (%s)\n", icon, w.ID, statusStr)
	}
}

// getExeDevVMs returns a map of VM names that exist on exe.dev
func getExeDevVMs() map[string]bool {
	vms := make(map[string]bool)
	cmd := exec.Command("ssh", "exe.dev", "ls")
	out, err := cmd.Output()
	if err != nil {
		return vms
	}
	
	// Parse output like "  • vmname.exe.xyz - running (image)"
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "•") {
			continue
		}
		// Extract VM name
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			vmName := strings.TrimPrefix(parts[1], "•")
			vmName = strings.TrimSpace(vmName)
			// Remove .exe.xyz suffix to get worker ID
			workerID := strings.TrimSuffix(vmName, ".exe.xyz")
			vms[workerID] = true
		}
	}
	return vms
}

func listTasks(client *http.Client, baseURL, token string) {
	body, err := apiRequest(client, "GET", baseURL+"/api/tasks", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	
	var tasks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Prompt string `json:"prompt"`
	}
	json.Unmarshal(body, &tasks)
	
	if len(tasks) == 0 {
		fmt.Println("No tasks")
		return
	}
	
	fmt.Printf("📋 Tasks (%d):\n", len(tasks))
	for _, t := range tasks {
		icon := "⏳"
		switch t.Status {
		case "completed":
			icon = "✅"
		case "running":
			icon = "🔨"
		case "failed":
			icon = "❌"
		case "queued":
			icon = "📥"
		}
		prompt := t.Prompt
		if len(prompt) > 60 {
			prompt = prompt[:60] + "..."
		}
		fmt.Printf("   %s %s: %s\n", icon, t.ID[:8], prompt)
	}
}

func scaleWorkers(client *http.Client, baseURL, token string, n int) {
	url := fmt.Sprintf("%s/api/scale?workers=%d", baseURL, n)
	_, err := apiRequest(client, "POST", url, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Scaling to %d workers\n", n)
}

func drainWorkers(client *http.Client, baseURL, token string) {
	_, err := apiRequest(client, "POST", baseURL+"/api/drain", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Draining workers")
}

func addTask(client *http.Client, baseURL, token, prompt string) {
	reqBody := fmt.Sprintf(`{"prompt": %q}`, prompt)
	req, err := http.NewRequest("POST", baseURL+"/api/enqueue?token="+token, strings.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error %d: %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}
	
	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &result)
	
	fmt.Printf("✅ Task added: %s\n", result.ID)
	if len(prompt) > 60 {
		fmt.Printf("   Prompt: %s...\n", prompt[:60])
	} else {
		fmt.Printf("   Prompt: %s\n", prompt)
	}
}

func showTask(client *http.Client, baseURL, token, taskID string) {
	body, err := apiRequest(client, "GET", baseURL+"/api/task?id="+taskID, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	
	var task struct {
		ID         string  `json:"id"`
		Status     string  `json:"status"`
		Prompt     string  `json:"prompt"`
		WorkerID   *string `json:"worker_id"`
		Result     *string `json:"result"`
		Error      *string `json:"error"`
		CreatedAt  string  `json:"created_at"`
		StartedAt  *string `json:"started_at"`
		CompletedAt *string `json:"completed_at"`
	}
	json.Unmarshal(body, &task)
	
	icon := "⏳"
	switch task.Status {
	case "completed":
		icon = "✅"
	case "running":
		icon = "🔨"
	case "failed":
		icon = "❌"
	case "queued":
		icon = "📥"
	}
	
	fmt.Printf("%s Task %s\n", icon, task.ID)
	fmt.Printf("   Status:  %s\n", task.Status)
	fmt.Printf("   Prompt:  %s\n", task.Prompt)
	if task.WorkerID != nil {
		fmt.Printf("   Worker:  %s\n", *task.WorkerID)
	}
	fmt.Printf("   Created: %s\n", task.CreatedAt)
	if task.StartedAt != nil {
		fmt.Printf("   Started: %s\n", *task.StartedAt)
	}
	if task.CompletedAt != nil {
		fmt.Printf("   Completed: %s\n", *task.CompletedAt)
	}
	if task.Result != nil && *task.Result != "" {
		fmt.Printf("   Result:  %s\n", *task.Result)
	}
	if task.Error != nil && *task.Error != "" {
		fmt.Printf("   Error:   %s\n", *task.Error)
	}
}

func killWorker(client *http.Client, baseURL, token, workerID string) {
	fmt.Printf("Killing worker %s...\n", workerID)
	
	// First, try to remove from coordinator DB via API
	// Note: This requires the coordinator to have a delete-worker endpoint
	// For now, we'll delete the VM and the coordinator will clean up the DB entry
	
	// Delete the VM from exe.dev
	fmt.Printf("Deleting VM %s...\n", workerID)
	cmd := exec.Command("ssh", "exe.dev", "rm", workerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// VM might not exist, that's OK
		fmt.Printf("   Note: %s\n", strings.TrimSpace(string(output)))
	} else {
		fmt.Printf("   VM deleted\n")
	}
	
	// Call drain to trigger cleanup of stale workers
	// The coordinator will detect the VM is gone and clean up
	fmt.Println("Triggering coordinator cleanup...")
	apiRequest(client, "POST", baseURL+"/api/cleanup-workers", token)
	
	fmt.Printf("✅ Worker %s killed\n", workerID)
}

func resetTask(client *http.Client, baseURL, token, taskID string) {
	_, err := apiRequest(client, "POST", baseURL+"/api/reset-task?id="+taskID, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Task %s reset to queued\n", taskID)
}

func showStuckTasks(client *http.Client, baseURL, token string) {
	body, err := apiRequest(client, "GET", baseURL+"/api/stuck-tasks", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var tasks []struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		Prompt    string  `json:"prompt"`
		WorkerID  *string `json:"worker_id"`
		Reason    string  `json:"reason"`
		Retries   int     `json:"retry_count"`
	}
	json.Unmarshal(body, &tasks)

	if len(tasks) == 0 {
		fmt.Println("No stuck tasks")
		return
	}

	fmt.Printf("⚠️  Stuck Tasks (%d):\n", len(tasks))
	for _, t := range tasks {
		prompt := t.Prompt
		if len(prompt) > 50 {
			prompt = prompt[:50] + "..."
		}
		worker := "<none>"
		if t.WorkerID != nil {
			worker = *t.WorkerID
		}
		fmt.Printf("   %s: %s\n", t.ID[:8], prompt)
		fmt.Printf("      Worker: %s | Reason: %s | Retries: %d\n", worker, t.Reason, t.Retries)
	}
}

func resetStuckTasks(client *http.Client, baseURL, token string) {
	_, err := apiRequest(client, "POST", baseURL+"/api/reset-stuck-tasks", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ All stuck tasks reset to queued")
}

func showAPIHelp() {
	fmt.Println(`Coordinator API Reference
=========================

All endpoints require authentication via:
  - Header: X-Coordinator-Token: <token>
  - Query param: ?token=<token>

Tasks
-----
POST /api/enqueue
  Add a task to the queue
  Body: {"prompt": "...", "repo_url": "...", "base_branch": "..."}
  Returns: {"id": "task-id"}

GET /api/tasks
  List all tasks
  Returns: [{"id": "...", "status": "...", "prompt": "...", ...}]

GET /api/task?id=<task-id>
  Get details for a specific task
  Returns: {"id": "...", "status": "...", "prompt": "...", "worker_id": "...", ...}

POST /api/clear-tasks
  Clear all tasks from the queue

POST /api/reset-task?id=<task-id>
  Reset a stuck/orphaned task to queued status

Workers
-------
GET /api/workers
  List all workers
  Returns: [{"id": "...", "status": "...", ...}]

POST /api/scale?workers=<n>
  Scale to n workers

POST /api/drain
  Gracefully drain all workers (finish current tasks, then shut down)

POST /api/cleanup-workers
  Remove stale worker entries from DB (VMs that no longer exist)

Stats
-----
GET /api/stats
  Get coordinator statistics
  Returns: {"tasks": {"queued": N, "running": N, ...}, "workers": {"total": N, ...}}

Groups
------
POST /api/group/create
  Create a task group with multiple prompts
  Body: {"name": "...", "prompts": ["...", "..."], "repo_url": "...", "base_branch": "..."}

GET /api/groups
  List all task groups

GET /api/group?id=<group-id>
  Get details for a specific group

GET /api/group/tasks?id=<group-id>
  List tasks in a group

Other
-----
GET /api/shelley-bin
  Download the shelley binary (used by workers during install)
`)
}
