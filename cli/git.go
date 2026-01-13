package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

// gitListCommits lists recent git commits
func (m *Model) gitListCommits() tea.Cmd {
	gitRoot, err := getGitRoot(m.config.WorkingDir)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Not a git repository"),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Git History:\n")

	// Working changes
	workingStats := getWorkingChangesStats(gitRoot)
	if workingStats.FilesCount > 0 {
		sb.WriteString(fmt.Sprintf("  %s %s (%d files, +%d -%d)\n",
			m.styles.ToolRunning.Render("working"),
			"Working Changes",
			workingStats.FilesCount,
			workingStats.Additions,
			workingStats.Deletions))
	}

	// Recent commits
	commits, err := getRecentCommits(gitRoot, 10)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get commits: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	for _, c := range commits {
		shortID := c.ID
		if len(shortID) > 7 {
			shortID = shortID[:7]
		}
		msg := c.Message
		if len(msg) > 50 {
			msg = msg[:50] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %s %s (%d files, +%d -%d)\n",
			m.styles.ToolName.Render(shortID),
			msg,
			c.FilesCount,
			c.Additions,
			c.Deletions))
	}

	sb.WriteString("\nUse /git show <commit> to see files, /git diff <file> for diff")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// gitShowCommit shows files changed in a commit
func (m *Model) gitShowCommit(commitID string) tea.Cmd {
	gitRoot, err := getGitRoot(m.config.WorkingDir)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Not a git repository"),
		})
		m.updateViewportContent()
		return nil
	}

	files, err := getCommitFiles(gitRoot, commitID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get commit files: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if len(files) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No files changed in " + commitID),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files in %s:\n", commitID))

	for _, f := range files {
		statusStyle := m.styles.ToolOutput
		statusIcon := "M"
		switch f.Status {
		case "added":
			statusStyle = m.styles.ToolSuccess
			statusIcon = "A"
		case "deleted":
			statusStyle = m.styles.ToolError
			statusIcon = "D"
		}
		sb.WriteString(fmt.Sprintf("  %s %s (+%d -%d)\n",
			statusStyle.Render(statusIcon),
			f.Path,
			f.Additions,
			f.Deletions))
	}

	sb.WriteString("\nUse /git diff <file> to see content")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// gitDiffFile shows the diff for a file
func (m *Model) gitDiffFile(filePath string) tea.Cmd {
	gitRoot, err := getGitRoot(m.config.WorkingDir)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Not a git repository"),
		})
		m.updateViewportContent()
		return nil
	}

	diff, err := getFileDiff(gitRoot, filePath)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get diff: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if diff == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No changes in " + filePath),
		})
		m.updateViewportContent()
		return nil
	}

	// Colorize the diff output
	colorized := colorizeDiff(diff, m.styles)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Diff for %s:\n", filePath))
	sb.WriteString(colorized)

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: sb.String(),
	})
	m.updateViewportContent()
	return nil
}

// Git helper types

type gitCommitInfo struct {
	ID         string
	Message    string
	Author     string
	FilesCount int
	Additions  int
	Deletions  int
}

type gitFileInfo struct {
	Path      string
	Status    string
	Additions int
	Deletions int
}

type gitStats struct {
	FilesCount int
	Additions  int
	Deletions  int
}

// Git helper functions

func getGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getWorkingChangesStats(gitRoot string) gitStats {
	cmd := exec.Command("git", "diff", "HEAD", "--numstat")
	cmd.Dir = gitRoot
	output, _ := cmd.Output()
	return parseNumstat(string(output))
}

func parseNumstat(output string) gitStats {
	var stats gitStats
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			if parts[0] != "-" {
				add, _ := strconv.Atoi(parts[0])
				stats.Additions += add
			}
			if parts[1] != "-" {
				del, _ := strconv.Atoi(parts[1])
				stats.Deletions += del
			}
			stats.FilesCount++
		}
	}
	return stats
}

func getRecentCommits(gitRoot string, limit int) ([]gitCommitInfo, error) {
	cmd := exec.Command("git", "log", "--oneline", fmt.Sprintf("-%d", limit),
		"--pretty=format:%H\x00%s\x00%an")
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var commits []gitCommitInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) < 3 {
			continue
		}

		statCmd := exec.Command("git", "diff", parts[0]+"^", parts[0], "--numstat")
		statCmd.Dir = gitRoot
		statOutput, _ := statCmd.Output()
		stats := parseNumstat(string(statOutput))

		commits = append(commits, gitCommitInfo{
			ID:         parts[0],
			Message:    parts[1],
			Author:     parts[2],
			FilesCount: stats.FilesCount,
			Additions:  stats.Additions,
			Deletions:  stats.Deletions,
		})
	}
	return commits, nil
}

func getCommitFiles(gitRoot, commitID string) ([]gitFileInfo, error) {
	var cmd *exec.Cmd
	var statBase string

	if commitID == "working" {
		cmd = exec.Command("git", "diff", "--name-status", "HEAD")
		statBase = "HEAD"
	} else {
		cmd = exec.Command("git", "diff", "--name-status", commitID+"^", commitID)
		statBase = commitID + "^"
	}
	cmd.Dir = gitRoot

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []gitFileInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		status := "modified"
		switch parts[0] {
		case "A":
			status = "added"
		case "D":
			status = "deleted"
		}

		var statCmd *exec.Cmd
		if commitID == "working" {
			statCmd = exec.Command("git", "diff", statBase, "--numstat", "--", parts[1])
		} else {
			statCmd = exec.Command("git", "diff", statBase, commitID, "--numstat", "--", parts[1])
		}
		statCmd.Dir = gitRoot
		statOutput, _ := statCmd.Output()
		statParts := strings.Fields(string(statOutput))

		additions, deletions := 0, 0
		if len(statParts) >= 2 {
			additions, _ = strconv.Atoi(statParts[0])
			deletions, _ = strconv.Atoi(statParts[1])
		}

		files = append(files, gitFileInfo{
			Path:      parts[1],
			Status:    status,
			Additions: additions,
			Deletions: deletions,
		})
	}
	return files, nil
}

func getFileDiff(gitRoot, filePath string) (string, error) {
	cmd := exec.Command("git", "diff", "HEAD", "--", filePath)
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("git", "diff", "--", filePath)
		cmd.Dir = gitRoot
		output, err = cmd.Output()
		if err != nil {
			return "", err
		}
	}
	return string(output), nil
}

func colorizeDiff(diff string, styles *Styles) string {
	var sb strings.Builder
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			sb.WriteString(styles.ToolSuccess.Render(line))
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			sb.WriteString(styles.ToolError.Render(line))
		} else if strings.HasPrefix(line, "@@") {
			sb.WriteString(styles.ToolName.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
