package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"shelley.exe.dev/version"
)

type ServiceStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Port    int    `json:"port,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Details string `json:"details,omitempty"`
}

type CoordinatorStatus struct {
	Running bool   `json:"running"`
	Port    int    `json:"port,omitempty"`
	Token   string `json:"token,omitempty"`
	Workers int    `json:"workers,omitempty"`
	Tasks   struct {
		Queued    int `json:"queued"`
		Running   int `json:"running"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"tasks,omitempty"`
}

type StatusOutput struct {
	Version     version.Info       `json:"version"`
	Hostname    string             `json:"hostname"`
	Services    []ServiceStatus    `json:"services"`
	Coordinator *CoordinatorStatus `json:"coordinator,omitempty"`
}

func runStatus(args []string) {
	output := StatusOutput{}

	// Version info
	output.Version = version.GetInfo()

	// Hostname
	hostname, _ := os.Hostname()
	output.Hostname = hostname

	// Check systemd services
	// Services to check - for services with multiple ports, we'll show the running one
	services := []struct {
		name  string
		ports []int // Check these ports in order, show first running or first if none running
	}{
		{"coordinator", []int{8080, 8081}}, // Dashboard on 8080, coord on 8081
		{"igor", []int{8099}},
	}

	for _, svc := range services {
		var bestStatus *ServiceStatus
		for _, port := range svc.ports {
			status := checkSystemdService(svc.name, port)
			if status.Status == "running" {
				bestStatus = &status
				break // Found a running instance, use it
			}
			if bestStatus == nil {
				bestStatus = &status // Keep first checked as fallback
			}
		}
		if bestStatus != nil {
			output.Services = append(output.Services, *bestStatus)
		}
	}

	// Check coordinator API if running
	for _, svc := range output.Services {
		if svc.Name == "coordinator" && svc.Status == "running" {
			coordStatus := checkCoordinatorAPI()
			output.Coordinator = coordStatus
			break
		}
	}

	// Output as JSON or pretty print
	jsonOutput := false
	for _, arg := range args {
		if arg == "-json" || arg == "--json" {
			jsonOutput = true
		}
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(output)
	} else {
		prettyPrintStatus(output)
	}
}

func checkSystemdService(name string, port int) ServiceStatus {
	status := ServiceStatus{
		Name: name,
		Port: port,
	}

	// First check if systemctl exists and service is running
	cmd := exec.Command("systemctl", "is-active", name)
	out, err := cmd.Output()
	if err == nil {
		state := strings.TrimSpace(string(out))
		if state == "active" {
			status.Status = "running"

			// Get PID
			cmd = exec.Command("systemctl", "show", name, "--property=MainPID", "--value")
			out, err = cmd.Output()
			if err == nil {
				fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &status.PID)
			}
			return status
		}
	}

	// If not running as systemd service, check if something is listening on the port
	// This handles the case where coordinator is run manually (e.g., nohup shelley coord ...)
	cmd = exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-t")
	out, err = cmd.Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		pids := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(pids) > 0 {
			fmt.Sscanf(pids[0], "%d", &status.PID)
			status.Status = "running"
			return status
		}
	}

	status.Status = "stopped"
	return status
}

func checkCoordinatorAPI() *CoordinatorStatus {
	status := &CoordinatorStatus{
		Running: true,
		Port:    8080,
	}

	// Try to get token from journalctl
	token := getCoordinatorToken()
	status.Token = token

	client := &http.Client{Timeout: 2 * time.Second}

	// Try with token if we have one
	url := "http://localhost:8080/api/stats"
	if token != "" {
		url += "?token=" + token
	}

	resp, err := client.Get(url)
	if err != nil {
		status.Running = false
		return status
	}
	defer resp.Body.Close()

	// If we got stats, parse them
	if resp.StatusCode == http.StatusOK {
		var stats struct {
			Tasks struct {
				Queued    int `json:"queued"`
				Running   int `json:"running"`
				Completed int `json:"completed"`
				Failed    int `json:"failed"`
			} `json:"tasks"`
			Workers struct {
				Total int `json:"total"`
			} `json:"workers"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&stats); err == nil {
			status.Tasks = stats.Tasks
			status.Workers = stats.Workers.Total
		}
	}

	return status
}

// isHexString checks if a string contains only hexadecimal characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func getCoordinatorToken() string {
	// Method 0: Read from coordinator DB (persistent token)
	dbPaths := []string{
		os.ExpandEnv("$HOME/.config/shelley/coordinator.db"),
		"coordinator.db",
	}
	for _, dbPath := range dbPaths {
		if _, err := os.Stat(dbPath); err == nil {
			db, err := sql.Open("sqlite", dbPath)
			if err == nil {
				var token string
				if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'api_token'`).Scan(&token); err == nil && token != "" {
					db.Close()
					return token
				}
				db.Close()
			}
		}
	}

	// Method 1: Try to get token from process list (if -token was specified)
	cmd := exec.Command("ps", "aux")
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "shelley coord") && strings.Contains(line, "-token") {
				// Find -token and extract the value
				parts := strings.Split(line, "-token ")
				if len(parts) >= 2 {
					tokenPart := strings.Fields(parts[1])
					if len(tokenPart) > 0 {
						return tokenPart[0]
					}
				}
			}
		}
	}

	// Method 2: Check common log file locations for coordinator output
	logFiles := []string{
		"/tmp/coord.log",
		os.ExpandEnv("$HOME/coord.log"),
	}
	for _, logFile := range logFiles {
		if data, err := os.ReadFile(logFile); err == nil {
			lines := strings.Split(string(data), "\n")
			inTokenSection := false
			for _, line := range lines {
				line = strings.TrimSpace(line)
				// Look for API TOKEN section marker
				if strings.Contains(line, "=== API TOKEN ===") {
					inTokenSection = true
					continue
				}
				if inTokenSection {
					// End of token section
					if strings.HasPrefix(line, "===") {
						break
					}
					// This should be the token (32 hex chars)
					if len(line) == 32 && isHexString(line) {
						return line
					}
				}
			}
		}
	}

	// Method 3: Try journalctl (may not work via exe.dev ssh proxy)
	cmd = exec.Command("journalctl", "-u", "coordinator", "-n", "50", "--no-pager")
	out, err = cmd.Output()
	if err != nil {
		return ""
	}

	// Look for "API Token: <token>" in output
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if idx := strings.Index(lines[i], "API Token: "); idx != -1 {
			return strings.TrimSpace(lines[i][idx+11:])
		}
	}
	return ""
}

func prettyPrintStatus(output StatusOutput) {
	fmt.Printf("🐡 Shelley Status\n")
	fmt.Printf("================\n\n")

	fmt.Printf("Commit:   %s\n", output.Version.Commit)
	fmt.Printf("Built:    %s\n", output.Version.CommitTime)
	fmt.Printf("Modified: %v\n", output.Version.Modified)
	fmt.Printf("Hostname: %s\n", output.Hostname)

	fmt.Printf("\n📦 Services:\n")
	for _, svc := range output.Services {
		icon := "❌"
		if svc.Status == "running" {
			icon = "✅"
		}
		fmt.Printf("  %s %s (port %d)", icon, svc.Name, svc.Port)
		if svc.PID > 0 {
			fmt.Printf(" [PID %d]", svc.PID)
		}
		fmt.Printf("\n")
	}

	if output.Coordinator != nil && output.Coordinator.Running {
		fmt.Printf("\n🎛️  Coordinator:\n")
		fmt.Printf("  Workers: %d\n", output.Coordinator.Workers)
		fmt.Printf("  Tasks:   %d queued, %d running, %d completed, %d failed\n",
			output.Coordinator.Tasks.Queued,
			output.Coordinator.Tasks.Running,
			output.Coordinator.Tasks.Completed,
			output.Coordinator.Tasks.Failed)
		if output.Coordinator.Token != "" {
			fmt.Printf("  Token:   %s\n", output.Coordinator.Token)
		}
	}

	fmt.Printf("\n🔗 URLs:\n")
	fmt.Printf("  Dashboard:    https://%s.exe.xyz:8080/\n", output.Hostname)
	fmt.Printf("  Igor:         https://%s.exe.xyz:8099/\n", output.Hostname)
}
