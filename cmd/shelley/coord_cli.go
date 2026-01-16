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
	port := fs.Int("port", 8080, "Coordinator port")
	
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: shelley coord-cli [flags] <command>\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  clear-tasks    Clear all tasks from the queue\n")
		fmt.Fprintf(os.Stderr, "  clear-workers  Remove all workers\n")
		fmt.Fprintf(os.Stderr, "  clear-all      Clear tasks and workers, reset DB\n")
		fmt.Fprintf(os.Stderr, "  stats          Show coordinator stats\n")
		fmt.Fprintf(os.Stderr, "  workers        List workers\n")
		fmt.Fprintf(os.Stderr, "  tasks          List tasks\n")
		fmt.Fprintf(os.Stderr, "  scale <n>      Scale to n workers\n")
		fmt.Fprintf(os.Stderr, "  drain          Drain all workers\n")
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
	case "clear-tasks":
		clearTasks(client, baseURL, *token)
	case "clear-workers":
		clearWorkers(client, baseURL, *token)
	case "clear-all":
		clearAll(*port)
	case "stats":
		showStats(client, baseURL, *token)
	case "workers":
		listWorkers(client, baseURL, *token)
	case "tasks":
		listTasks(client, baseURL, *token)
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
	
	fmt.Printf("👷 Workers (%d):\n", len(workers))
	for _, w := range workers {
		icon := "⏳"
		switch w.Status {
		case "ready", "idle":
			icon = "✅"
		case "busy":
			icon = "🔨"
		case "failed":
			icon = "❌"
		}
		fmt.Printf("   %s %s (%s)\n", icon, w.ID, w.Status)
	}
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
		fmt.Fprintf(os.Stderr, "URL: %s\n", url)
		fmt.Fprintf(os.Stderr, "Token length: %d\n", len(token))
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
