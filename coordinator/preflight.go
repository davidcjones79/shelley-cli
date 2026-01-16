// Package coordinator implements a task queue and worker pool for distributed shelley execution.
package coordinator

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// PortInUseError provides detailed information about a port conflict.
type PortInUseError struct {
	Port    int
	PID     int
	Process string
	Command string
}

func (e *PortInUseError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("port %d is already in use by %s (PID %d)", e.Port, e.Process, e.PID)
	}
	return fmt.Sprintf("port %d is already in use", e.Port)
}

// CheckPortAvailable checks if a port is available for binding.
// Returns nil if available, or a PortInUseError with details if not.
func CheckPortAvailable(port int) error {
	// Try to bind to the port
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// Port is in use, try to find what's using it
		pid, process, cmd := getProcessOnPort(port)
		return &PortInUseError{
			Port:    port,
			PID:     pid,
			Process: process,
			Command: cmd,
		}
	}
	listener.Close()
	return nil
}

// getProcessOnPort tries to find what process is using a port.
// Returns PID, process name, and full command.
func getProcessOnPort(port int) (pid int, process, command string) {
	// Try lsof first
	cmd := exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-t")
	if output, err := cmd.Output(); err == nil {
		pidStr := strings.TrimSpace(string(output))
		if lines := strings.Split(pidStr, "\n"); len(lines) > 0 {
			if p, err := strconv.Atoi(lines[0]); err == nil {
				pid = p
			}
		}
	}

	if pid > 0 {
		// Get process name
		cmd = exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
		if output, err := cmd.Output(); err == nil {
			process = strings.TrimSpace(string(output))
		}
		// Get full command
		cmd = exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=")
		if output, err := cmd.Output(); err == nil {
			command = strings.TrimSpace(string(output))
		}
	}

	return pid, process, command
}

// PreflightResult contains the results of pre-flight checks.
type PreflightResult struct {
	Success   bool
	Errors    []error
	Warnings  []string
}

// AddError adds an error to the result and marks it as failed.
func (r *PreflightResult) AddError(err error) {
	r.Success = false
	r.Errors = append(r.Errors, err)
}

// AddWarning adds a warning to the result.
func (r *PreflightResult) AddWarning(msg string) {
	r.Warnings = append(r.Warnings, msg)
}

// Preflight performs pre-flight checks before starting coordinator.
func Preflight(port int) *PreflightResult {
	result := &PreflightResult{Success: true}

	// Check port availability
	if err := CheckPortAvailable(port); err != nil {
		result.AddError(err)
	}

	return result
}

// FormatPreflightError formats a PortInUseError with helpful suggestions.
func FormatPreflightError(err *PortInUseError) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n❌ Port %d is already in use\n\n", err.Port))

	if err.PID > 0 {
		sb.WriteString(fmt.Sprintf("Process: %s (PID %d)\n", err.Process, err.PID))
		if err.Command != "" {
			sb.WriteString(fmt.Sprintf("Command: %s\n", err.Command))
		}
	}

	sb.WriteString("\nSuggested actions:\n")
	sb.WriteString(fmt.Sprintf("  1. Use a different port: shelley dashboard -port %d\n", err.Port+1))
	if err.PID > 0 {
		sb.WriteString(fmt.Sprintf("  2. Stop the existing process: kill %d\n", err.PID))
	}
	sb.WriteString(fmt.Sprintf("  3. Find what's using the port: lsof -i :%d\n", err.Port))

	return sb.String()
}
