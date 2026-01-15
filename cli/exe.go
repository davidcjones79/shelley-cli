package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// runExeCommand runs an exe.dev shell command via the exe.dev CLI
func (m *Model) runExeCommand(cmdStr string) tea.Cmd {
	// Check if we're on exe.dev
	if _, err := os.Stat("/exe.dev"); os.IsNotExist(err) {
		m.showError("Not running on exe.dev - /exe commands only work on exe.dev VMs")
		return nil
	}

	// The exe.dev shell is accessed via SSH to exe.dev
	// We need to run the command through the exe.dev control plane
	// Format: ssh exe.dev <command>
	args := []string{"exe.dev", cmdStr}
	cmd := exec.Command("ssh", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Show output even on error (might contain useful info)
		if len(output) > 0 {
			m.showError(fmt.Sprintf("exe.dev command failed: %s\n%s", err, string(output)))
		} else {
			m.showError(fmt.Sprintf("exe.dev command failed: %s", err))
		}
		return nil
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		result = "Command completed successfully"
	}
	m.showSystemMessage(fmt.Sprintf("exe.dev: %s\n%s", cmdStr, result))
	return nil
}
