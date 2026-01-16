package main

import (
	"encoding/json"
	"fmt"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type WatchStats struct {
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

type WatchWorker struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	TasksCompleted int    `json:"tasks_completed"`
	CurrentTask    string `json:"current_task,omitempty"`
}

type WatchTask struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Prompt   string `json:"prompt"`
	WorkerID string `json:"worker_id,omitempty"`
}

func runCoordWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	token := fs.String("token", "", "Coordinator API token (auto-detected if not provided)")
	port := fs.Int("port", 8080, "Coordinator port")
	interval := fs.Duration("interval", 2*time.Second, "Refresh interval")
	
	fs.Parse(args)

	// Auto-detect token if not provided
	if *token == "" {
		*token = getCoordinatorToken()
		if *token == "" {
			fmt.Fprintf(os.Stderr, "Error: Could not auto-detect token. Use -token flag.\n")
			os.Exit(1)
		}
	}

	baseURL := fmt.Sprintf("http://localhost:%d", *port)
	client := &http.Client{Timeout: 5 * time.Second}

	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Clear screen and hide cursor
	fmt.Print("\033[2J\033[?25l")
	defer fmt.Print("\033[?25h") // Show cursor on exit

	// Initial render
	renderDashboard(client, baseURL, *token)

	for {
		select {
		case <-ticker.C:
			renderDashboard(client, baseURL, *token)
		case <-sigChan:
			fmt.Print("\033[?25h") // Show cursor
			fmt.Println("\n\nExiting...")
			return
		}
	}
}

func renderDashboard(client *http.Client, baseURL, token string) {
	// Move cursor to top
	fmt.Print("\033[H")

	// Get stats
	stats := fetchStats(client, baseURL, token)
	workers := fetchWorkers(client, baseURL, token)
	tasks := fetchTasks(client, baseURL, token)

	// Header
	fmt.Println("\033[1;36m╔══════════════════════════════════════════════════════════════════╗\033[0m")
	fmt.Println("\033[1;36m║\033[0m  🐡 \033[1mShelley Coordinator Dashboard\033[0m                                 \033[1;36m║\033[0m")
	fmt.Println("\033[1;36m╚══════════════════════════════════════════════════════════════════╝\033[0m")
	fmt.Println()

	// Stats bar
	fmt.Printf("  \033[1mWorkers:\033[0m %d total │ \033[32m%d idle\033[0m │ \033[33m%d busy\033[0m\n",
		stats.Workers.Total, stats.Workers.Idle, stats.Workers.Busy)
	fmt.Printf("  \033[1mTasks:\033[0m   \033[34m%d queued\033[0m │ \033[33m%d running\033[0m │ \033[32m%d completed\033[0m │ \033[31m%d failed\033[0m\n",
		stats.Tasks.Queued, stats.Tasks.Running, stats.Tasks.Completed, stats.Tasks.Failed)
	fmt.Println()

	// Workers section
	fmt.Println("\033[1;33m┌─ Workers ─────────────────────────────────────────────────────────┐\033[0m")
	if len(workers) == 0 {
		fmt.Println("\033[90m│ No workers                                                        │\033[0m")
	} else {
		for i, w := range workers {
			if i >= 6 {
				fmt.Printf("\033[90m│ ... and %d more                                                   │\033[0m\n", len(workers)-6)
				break
			}
			icon, color := getStatusDisplay(w.Status)
			line := fmt.Sprintf("%s %s\033[0m %s", icon, color+w.ID, w.Status)
			if w.CurrentTask != "" {
				line += fmt.Sprintf(" → %s", truncate(w.CurrentTask, 20))
			}
			fmt.Printf("│ %-66s│\n", line)
		}
	}
	fmt.Println("\033[1;33m└───────────────────────────────────────────────────────────────────┘\033[0m")
	fmt.Println()

	// Tasks section
	fmt.Println("\033[1;34m┌─ Tasks ────────────────────────────────────────────────────────────┐\033[0m")
	
	// Show running tasks first, then queued, then recent completed
	shown := 0
	maxTasks := 10
	
	for _, t := range tasks {
		if shown >= maxTasks {
			break
		}
		if t.Status == "running" {
			icon, color := getStatusDisplay(t.Status)
			prompt := truncate(t.Prompt, 50)
			fmt.Printf("│ %s %s%-8s\033[0m %s │\n", icon, color, t.ID[:8], prompt)
			shown++
		}
	}
	
	for _, t := range tasks {
		if shown >= maxTasks {
			break
		}
		if t.Status == "queued" {
			icon, color := getStatusDisplay(t.Status)
			prompt := truncate(t.Prompt, 50)
			fmt.Printf("│ %s %s%-8s\033[0m %s │\n", icon, color, t.ID[:8], prompt)
			shown++
		}
	}

	// Show recent completed/failed
	for i := len(tasks) - 1; i >= 0 && shown < maxTasks; i-- {
		t := tasks[i]
		if t.Status == "completed" || t.Status == "failed" {
			icon, color := getStatusDisplay(t.Status)
			prompt := truncate(t.Prompt, 50)
			fmt.Printf("│ %s %s%-8s\033[0m %s │\n", icon, color, t.ID[:8], prompt)
			shown++
		}
	}

	if shown == 0 {
		fmt.Println("\033[90m│ No tasks                                                           │\033[0m")
	}
	
	remaining := len(tasks) - shown
	if remaining > 0 {
		fmt.Printf("\033[90m│ ... and %d more tasks                                              │\033[0m\n", remaining)
	}
	
	fmt.Println("\033[1;34m└────────────────────────────────────────────────────────────────────┘\033[0m")
	fmt.Println()

	// Footer
	now := time.Now().Format("15:04:05")
	fmt.Printf("\033[90m  Updated: %s │ Press Ctrl+C to exit\033[0m\n", now)
	
	// Clear rest of screen
	fmt.Print("\033[J")
}

func getStatusDisplay(status string) (icon, color string) {
	switch status {
	case "idle", "ready":
		return "●", "\033[32m" // green
	case "busy", "running":
		return "◉", "\033[33m" // yellow
	case "completed":
		return "✓", "\033[32m" // green
	case "failed":
		return "✗", "\033[31m" // red
	case "queued":
		return "○", "\033[34m" // blue
	case "spawning":
		return "◐", "\033[36m" // cyan
	default:
		return "?", "\033[90m" // gray
	}
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	// Pad to fixed width
	return fmt.Sprintf("%-*s", max, s)
}

func fetchStats(client *http.Client, baseURL, token string) WatchStats {
	var stats WatchStats
	resp, err := client.Get(baseURL + "/api/stats?token=" + token)
	if err != nil {
		return stats
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&stats)
	return stats
}

func fetchWorkers(client *http.Client, baseURL, token string) []WatchWorker {
	var workers []WatchWorker
	resp, err := client.Get(baseURL + "/api/workers?token=" + token)
	if err != nil {
		return workers
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&workers)
	return workers
}

func fetchTasks(client *http.Client, baseURL, token string) []WatchTask {
	var tasks []WatchTask
	resp, err := client.Get(baseURL + "/api/tasks?token=" + token)
	if err != nil {
		return tasks
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(&tasks)
	return tasks
}
