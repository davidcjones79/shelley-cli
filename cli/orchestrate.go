package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CoordinatorStats holds statistics from the coordinator API
type CoordinatorStats struct {
	WorkersTotal     int `json:"workers_total"`
	WorkersBusy      int `json:"workers_busy"`
	WorkersIdle      int `json:"workers_idle"`
	TasksQueued      int `json:"tasks_queued"`
	TasksRunning     int `json:"tasks_running"`
	TasksCompleted   int `json:"tasks_completed"`
	TasksFailed      int `json:"tasks_failed"`
}

// isCoordinatorRunning checks if the coordinator is accessible
func isCoordinatorRunning() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:8080/api/stats")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// getCoordinatorStats fetches stats from the coordinator
func getCoordinatorStats() (*CoordinatorStats, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:8080/api/stats")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var stats CoordinatorStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// getHostname returns the current hostname
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return hostname
}

// startCoordinator starts the coordinator dashboard in the background
func (m *Model) startCoordinator() string {
	if isCoordinatorRunning() {
		return "🟢 Coordinator already running at http://localhost:8080/"
	}

	shelleyBin, err := os.Executable()
	if err != nil {
		return fmt.Sprintf("Error finding shelley binary: %v", err)
	}

	cmd := exec.Command(shelleyBin, "dashboard", "-auto-start", "-port", "8080")
	cmd.Dir = m.config.WorkingDir
	
	// Start in background, redirect output to file
	logFile, err := os.Create("/tmp/shelley-coordinator.log")
	if err != nil {
		return fmt.Sprintf("Error creating log file: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Sprintf("Error starting coordinator: %v", err)
	}

	// Wait a bit for it to start
	time.Sleep(2 * time.Second)

	if isCoordinatorRunning() {
		hostname := getHostname()
		return fmt.Sprintf("🚀 Started coordinator dashboard\n   Dashboard: https://%s.exe.xyz:8080/\n   Log: /tmp/shelley-coordinator.log", hostname)
	}

	return "Coordinator started but not responding yet. Check /tmp/shelley-coordinator.log"
}

// stopCoordinator stops the coordinator
func (m *Model) stopCoordinator() string {
	cmd := exec.Command("pkill", "-f", "shelley dashboard")
	cmd.Run()
	return "🛑 Coordinator stopped"
}

// coordStatus returns coordinator status
func (m *Model) coordStatus() string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start to start it."
	}

	stats, err := getCoordinatorStats()
	if err != nil {
		return fmt.Sprintf("Error getting coordinator stats: %v", err)
	}

	hostname := getHostname()
	var sb strings.Builder
	sb.WriteString("📡 Coordinator Status:\n\n")
	sb.WriteString(fmt.Sprintf("Workers: %d total (%d busy, %d idle)\n",
		stats.WorkersTotal, stats.WorkersBusy, stats.WorkersIdle))
	sb.WriteString(fmt.Sprintf("Tasks: %d queued, %d running, %d completed, %d failed\n",
		stats.TasksQueued, stats.TasksRunning, stats.TasksCompleted, stats.TasksFailed))
	sb.WriteString(fmt.Sprintf("\nDashboard: https://%s.exe.xyz:8080/", hostname))

	return sb.String()
}

// coordScale scales the number of workers
func (m *Model) coordScale(countStr string) string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start first."
	}

	count, err := strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return "Invalid worker count. Use a positive integer."
	}

	token := getCoordinatorToken()
	url := fmt.Sprintf("http://localhost:8080/api/scale?workers=%d", count)
	req, _ := http.NewRequest("POST", url, nil)
	if token != "" {
		req.Header.Set("X-Coordinator-Token", token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error scaling workers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("Error scaling workers: %s", string(body))
	}

	return fmt.Sprintf("✅ Scaling to %d workers", count)
}

// coordDrain drains all workers
func (m *Model) coordDrain() string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running."
	}

	token := getCoordinatorToken()
	req, _ := http.NewRequest("POST", "http://localhost:8080/api/drain", nil)
	if token != "" {
		req.Header.Set("X-Coordinator-Token", token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error draining workers: %v", err)
	}
	defer resp.Body.Close()

	return "💧 Draining all workers"
}

// coordAddTask adds a task to the coordinator queue
func (m *Model) coordAddTask(prompt string) string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start first."
	}

	token := getCoordinatorToken()
	
	taskData := map[string]interface{}{
		"prompt": prompt,
	}
	jsonData, _ := json.Marshal(taskData)

	req, _ := http.NewRequest("POST", "http://localhost:8080/api/enqueue", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Coordinator-Token", token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error adding task: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("Error adding task: %s", string(body))
	}

	return fmt.Sprintf("✅ Added task to queue: %s", truncateString(prompt, 50))
}

// coordWorkers lists coordinator workers
func (m *Model) coordWorkers() string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start first."
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:8080/api/workers")
	if err != nil {
		return fmt.Sprintf("Error getting workers: %v", err)
	}
	defer resp.Body.Close()

	var workers []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return fmt.Sprintf("Error parsing workers: %v", err)
	}

	if len(workers) == 0 {
		return "No workers registered. Use /coord scale <n> to add workers."
	}

	var sb strings.Builder
	sb.WriteString("👷 Workers:\n\n")

	for _, w := range workers {
		id := w["id"]
		status := w["status"]
		icon := "🟢"
		if status == "busy" {
			icon = "🟡"
		} else if status == "offline" {
			icon = "🔴"
		}
		sb.WriteString(fmt.Sprintf("%s %v (%v)\n", icon, id, status))
	}

	return sb.String()
}

// coordTasks lists coordinator tasks
func (m *Model) coordTasks() string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start first."
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:8080/api/tasks")
	if err != nil {
		return fmt.Sprintf("Error getting tasks: %v", err)
	}
	defer resp.Body.Close()

	var tasks []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return fmt.Sprintf("Error parsing tasks: %v", err)
	}

	if len(tasks) == 0 {
		return "No tasks in queue. Use /coord add <prompt> to add tasks."
	}

	var sb strings.Builder
	sb.WriteString("📝 Tasks:\n\n")

	// Show most recent first, limit to 20
	shown := 0
	for i := len(tasks) - 1; i >= 0 && shown < 20; i-- {
		t := tasks[i]
		id := t["id"]
		status := t["status"]
		prompt := ""
		if p, ok := t["prompt"].(string); ok {
			prompt = truncateString(p, 40)
		}

		icon := "⏳" // queued
		switch status {
		case "running":
			icon = "🔄"
		case "completed":
			icon = "✅"
		case "failed":
			icon = "❌"
		}

		// Get short ID
		idStr := fmt.Sprintf("%v", id)
		if len(idStr) > 8 {
			idStr = idStr[:8]
		}

		sb.WriteString(fmt.Sprintf("%s %s (%v) %s\n", icon, idStr, status, prompt))
		shown++
	}

	if len(tasks) > 20 {
		sb.WriteString(fmt.Sprintf("\n... and %d more tasks", len(tasks)-20))
	}

	return sb.String()
}

// getCoordinatorToken tries to get the coordinator token
func getCoordinatorToken() string {
	// Try reading from common locations
	locations := []string{
		"/tmp/coordinator-token",
		"/tmp/shelley-coordinator.token",
		os.ExpandEnv("$HOME/.config/shelley/coordinator.token"),
	}

	for _, loc := range locations {
		data, err := os.ReadFile(loc)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	return ""
}

// orchestrate parses a plan into tasks and executes them via coordinator
func (m *Model) orchestrate(plan string) string {
	// Parse the plan into tasks
	tasks := parsePlanIntoTasks(plan)
	if len(tasks) == 0 {
		return "Could not parse plan into discrete tasks. Please provide a clearer breakdown (numbered list, bullet points, or pipe-separated)."
	}

	// Start coordinator if not running
	if !isCoordinatorRunning() {
		startResult := m.startCoordinator()
		if !isCoordinatorRunning() {
			return startResult + "\n\nCoordinator failed to start. Cannot orchestrate."
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🎯 Orchestrating %d tasks:\n\n", len(tasks)))

	for i, task := range tasks {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, truncateString(task, 60)))
	}

	// Scale workers to match task count (up to max of 5)
	workerCount := len(tasks)
	if workerCount > 5 {
		workerCount = 5
	}

	m.coordScale(strconv.Itoa(workerCount))

	// Add tasks to coordinator
	for _, task := range tasks {
		m.coordAddTask(task)
	}

	hostname := getHostname()
	sb.WriteString(fmt.Sprintf("\n✅ Added %d tasks to coordinator\n", len(tasks)))
	sb.WriteString(fmt.Sprintf("📊 Scaling to %d workers\n", workerCount))
	sb.WriteString("\nMonitor progress:\n")
	sb.WriteString(fmt.Sprintf("  • Dashboard: https://%s.exe.xyz:8080/\n", hostname))
	sb.WriteString("  • CLI: shelley watch\n")
	sb.WriteString("  • Status: /coord status\n")

	return sb.String()
}

// parsePlanIntoTasks parses various plan formats into discrete tasks
func parsePlanIntoTasks(plan string) []string {
	var tasks []string

	// First try pipe-separated format
	if strings.Contains(plan, "|") {
		parts := strings.Split(plan, "|")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			part = strings.Trim(part, `"'`)
			if len(part) > 10 {
				tasks = append(tasks, part)
			}
		}
		if len(tasks) > 0 {
			return tasks
		}
	}

	// Try line-by-line parsing
	lines := strings.Split(plan, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and headers
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Remove list prefixes
		for _, prefix := range []string{"- ", "* ", "• ", "- [ ] ", "- [x] "} {
			line = strings.TrimPrefix(line, prefix)
		}

		// Remove numbered prefixes like "1. ", "1) ", "01. "
		if len(line) > 2 {
			// Check for patterns like "1.", "1)", "01.", "01)"
			for i := 1; i <= 3 && i < len(line); i++ {
				prefix := line[:i]
				if _, err := strconv.Atoi(prefix); err == nil {
					if i < len(line) && (line[i] == '.' || line[i] == ')') {
						line = strings.TrimSpace(line[i+1:])
						break
					}
				}
			}
		}

		if len(line) > 10 { // Minimum task length
			tasks = append(tasks, line)
		}
	}

	return tasks
}

// coordCreateGroup creates a task group with a repo URL
func (m *Model) coordCreateGroup(repoURL, promptsStr string) string {
	if !isCoordinatorRunning() {
		// Try to start it
		startResult := m.startCoordinator()
		if !isCoordinatorRunning() {
			return startResult + "\n\nCoordinator failed to start."
		}
	}

	// Parse prompts (pipe-separated or line-separated)
	prompts := parsePlanIntoTasks(promptsStr)
	if len(prompts) == 0 {
		return "Could not parse prompts. Use pipe-separated format:\n  \"task 1\" | \"task 2\" | \"task 3\""
	}

	token := getCoordinatorToken()
	
	groupData := map[string]interface{}{
		"name":        fmt.Sprintf("Group %d", time.Now().Unix()%10000),
		"repo_url":    repoURL,
		"base_branch": "main",
		"prompts":     prompts,
	}
	jsonData, _ := json.Marshal(groupData)

	req, _ := http.NewRequest("POST", "http://localhost:8081/api/group/create", bytes.NewReader(jsonData))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Coordinator-Token", token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error creating group: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("Error creating group: %s", string(body))
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	hostname := getHostname()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ Created task group with %d tasks\n\n", len(prompts)))
	sb.WriteString(fmt.Sprintf("Repository: %s\n", repoURL))
	sb.WriteString("Tasks:\n")
	for i, p := range prompts {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, truncateString(p, 50)))
	}
	sb.WriteString(fmt.Sprintf("\n📊 Use /coord scale %d to start workers\n", min(len(prompts), 5)))
	sb.WriteString(fmt.Sprintf("🌐 Dashboard: https://%s.exe.xyz:8080/", hostname))

	return sb.String()
}

// coordListRepos lists shared repositories
func (m *Model) coordListRepos() string {
	if !isCoordinatorRunning() {
		return "Coordinator is not running. Use /coord start first."
	}

	token := getCoordinatorToken()
	req, _ := http.NewRequest("GET", "http://localhost:8081/api/repos", nil)
	if token != "" {
		req.Header.Set("X-Coordinator-Token", token)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error getting repos: %v", err)
	}
	defer resp.Body.Close()

	var repos []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return fmt.Sprintf("Error parsing repos: %v", err)
	}

	if len(repos) == 0 {
		return "No shared repositories. Create a task group with a repo URL to add one."
	}

	var sb strings.Builder
	sb.WriteString("📦 Shared Repositories:\n\n")

	for _, r := range repos {
		id := r["id"]
		url := r["url"]
		path := r["path"]
		sb.WriteString(fmt.Sprintf("• %v\n", id))
		sb.WriteString(fmt.Sprintf("  URL: %v\n", url))
		sb.WriteString(fmt.Sprintf("  Path: %v\n\n", path))
	}

	return sb.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
