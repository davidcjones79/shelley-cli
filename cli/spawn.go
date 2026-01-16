package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SubAgent represents a spawned Shelley sub-agent
type SubAgent struct {
	ID         string
	Prompt     string
	StartTime  time.Time
	Cmd        *exec.Cmd
	OutputFile string
	Status     string // "running", "completed", "failed"
	outFile    *os.File
}

var (
	subAgents   = make(map[string]*SubAgent)
	subAgentsMu sync.Mutex
	subAgentID  int
)

// spawnSubAgent spawns a new Shelley sub-agent with the given prompt
func (m *Model) spawnSubAgent(prompt string) string {
	subAgentsMu.Lock()
	defer subAgentsMu.Unlock()

	subAgentID++
	id := fmt.Sprintf("agent-%d", subAgentID)
	outputFile := filepath.Join(os.TempDir(), fmt.Sprintf("shelley-%s.log", id))

	// Find shelley binary
	shelleyBin, err := os.Executable()
	if err != nil {
		return fmt.Sprintf("Error finding shelley binary: %v", err)
	}

	// Spawn sub-agent with flags for unattended execution
	cmd := exec.Command(shelleyBin, "chat", "-yes", "-no-sync", "-no-browser", "-prompt", prompt)
	cmd.Dir = m.config.WorkingDir

	outFile, err := os.Create(outputFile)
	if err != nil {
		return fmt.Sprintf("Error creating output file: %v", err)
	}
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	if err := cmd.Start(); err != nil {
		outFile.Close()
		os.Remove(outputFile)
		return fmt.Sprintf("Error starting sub-agent: %v", err)
	}

	agent := &SubAgent{
		ID:         id,
		Prompt:     prompt,
		StartTime:  time.Now(),
		Cmd:        cmd,
		OutputFile: outputFile,
		Status:     "running",
		outFile:    outFile,
	}
	subAgents[id] = agent

	// Monitor completion in background
	go func() {
		cmd.Wait()
		outFile.Close()
		subAgentsMu.Lock()
		if cmd.ProcessState != nil && cmd.ProcessState.Success() {
			agent.Status = "completed"
		} else {
			agent.Status = "failed"
		}
		subAgentsMu.Unlock()
	}()

	return fmt.Sprintf("🚀 Spawned sub-agent %s\n   Prompt: %s\n   Output: %s\n\nUse /spawns to check status, /spawn-output %s to view output",
		id, truncateString(prompt, 50), outputFile, id)
}

// listSubAgents returns a formatted list of all sub-agents
func (m *Model) listSubAgents() string {
	subAgentsMu.Lock()
	defer subAgentsMu.Unlock()

	if len(subAgents) == 0 {
		return "No sub-agents spawned. Use /spawn <prompt> to start one."
	}

	var sb strings.Builder
	sb.WriteString("🤖 Sub-Agents:\n\n")

	for _, agent := range subAgents {
		icon := "🔄"
		switch agent.Status {
		case "completed":
			icon = "✅"
		case "failed":
			icon = "❌"
		}
		elapsed := time.Since(agent.StartTime).Round(time.Second)
		sb.WriteString(fmt.Sprintf("%s %s (%s) - %s\n   %s\n\n",
			icon, agent.ID, agent.Status, elapsed, truncateString(agent.Prompt, 60)))
	}

	return sb.String()
}

// getSubAgentOutput returns the output from a specific sub-agent
func (m *Model) getSubAgentOutput(id string) string {
	subAgentsMu.Lock()
	agent, ok := subAgents[id]
	subAgentsMu.Unlock()

	if !ok {
		return fmt.Sprintf("Sub-agent %s not found. Use /spawns to list agents.", id)
	}

	content, err := os.ReadFile(agent.OutputFile)
	if err != nil {
		return fmt.Sprintf("Error reading output: %v", err)
	}

	// Truncate if too long
	output := string(content)
	if len(output) > 10000 {
		output = output[:5000] + "\n\n... (truncated) ...\n\n" + output[len(output)-5000:]
	}

	return fmt.Sprintf("📄 Output from %s (%s):\n\n%s", id, agent.Status, output)
}

// waitForSubAgents waits for all running sub-agents to complete
func (m *Model) waitForSubAgents() string {
	subAgentsMu.Lock()
	running := []*SubAgent{}
	for _, agent := range subAgents {
		if agent.Status == "running" {
			running = append(running, agent)
		}
	}
	subAgentsMu.Unlock()

	if len(running) == 0 {
		return "No running sub-agents to wait for."
	}

	// Wait for all running agents
	for _, agent := range running {
		if agent.Cmd.Process != nil {
			agent.Cmd.Wait()
		}
	}

	return fmt.Sprintf("✅ All %d sub-agents have completed. Use /spawns to see results.", len(running))
}

// spawnParallel spawns multiple sub-agents from a pipe-separated list
func (m *Model) spawnParallel(input string) string {
	tasks := strings.Split(input, "|")

	var results []string
	spawned := 0
	for _, task := range tasks {
		task = strings.TrimSpace(task)
		task = strings.Trim(task, `"'`)
		if task == "" {
			continue
		}
		result := m.spawnSubAgent(task)
		results = append(results, result)
		spawned++
	}

	if spawned == 0 {
		return "No valid tasks found. Use: /parallel \"task1\" | \"task2\" | \"task3\""
	}

	return fmt.Sprintf("🚀 Spawned %d sub-agents:\n\n%s\n\nUse /spawns to monitor progress.",
		spawned, strings.Join(results, "\n\n"))
}

// clearSubAgents removes completed/failed sub-agents from tracking
func (m *Model) clearSubAgents() string {
	subAgentsMu.Lock()
	defer subAgentsMu.Unlock()

	cleared := 0
	for id, agent := range subAgents {
		if agent.Status != "running" {
			// Clean up output file
			os.Remove(agent.OutputFile)
			delete(subAgents, id)
			cleared++
		}
	}

	if cleared == 0 {
		return "No completed sub-agents to clear."
	}
	return fmt.Sprintf("🧹 Cleared %d completed sub-agents.", cleared)
}

// truncateString truncates a string to maxLen with ellipsis
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
