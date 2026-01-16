package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

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
	services := []struct {
		name string
		port int
	}{
		{"coordinator", 8080},
		{"igor", 8099},
	}

	for _, svc := range services {
		status := checkSystemdService(svc.name, svc.port)
		output.Services = append(output.Services, status)
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

	// Check if systemctl exists and service is running
	cmd := exec.Command("systemctl", "is-active", name)
	out, err := cmd.Output()
	if err != nil {
		status.Status = "stopped"
		return status
	}

	state := strings.TrimSpace(string(out))
	if state == "active" {
		status.Status = "running"

		// Get PID
		cmd = exec.Command("systemctl", "show", name, "--property=MainPID", "--value")
		out, err = cmd.Output()
		if err == nil {
			fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &status.PID)
		}
	} else {
		status.Status = state
	}

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

func getCoordinatorToken() string {
	// Try to get token from journalctl
	cmd := exec.Command("journalctl", "-u", "coordinator", "-n", "50", "--no-pager")
	out, err := cmd.Output()
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
