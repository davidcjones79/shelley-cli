package cli

import (
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

// handleSlashCommand handles special /commands
func (m *Model) handleSlashCommand(text string) tea.Cmd {
	m.textarea.Reset()

	parts := strings.Fields(text)
	cmd := parts[0]

	switch cmd {
	case "/run":
		return m.runSuggestedCommand(parts[1:])
	case "/clear":
		m.messages = nil
		m.suggestedCmds = nil
		m.sessionName = ""
		m.updateViewportContent()
		return nil
	case "/save":
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}
		return m.saveSession(name)
	case "/load":
		if len(parts) < 2 {
			return m.listSessions()
		}
		return m.loadSession(parts[1])
	case "/sessions":
		return m.listSessions()
	case "/stop", "/cancel":
		if m.processing && m.loopCancel != nil {
			m.loopCancel()
			m.processing = false
			m.pendingMessages = nil
			m.loop = nil
			m.loopCtx, m.loopCancel = nil, nil
			m.showSystemMessage("Cancelled")
		} else {
			m.showSystemMessage("Nothing to cancel")
		}
		return nil
	case "/verbose":
		m.verbose = !m.verbose
		status := "off"
		if m.verbose {
			status = "on"
		}
		m.showSystemMessage("Verbose mode: " + status)
		return nil

	case "/mouse":
		m.mouseEnabled = !m.mouseEnabled
		status := "off (text selection enabled, use Ctrl+U/D to scroll)"
		var cmd tea.Cmd
		if m.mouseEnabled {
			status = "on (mouse scrolling enabled, text selection disabled)"
			cmd = tea.EnableMouseCellMotion
		} else {
			cmd = tea.DisableMouse
		}
		m.showSystemMessage("Mouse mode: " + status)
		return cmd

	// Database conversation commands
	case "/conversations", "/convos":
		return m.listConversations()
	case "/switch":
		if len(parts) < 2 {
			m.showError("Usage: /switch <conversation-id>")
			return nil
		}
		return m.switchConversation(parts[1])
	case "/new":
		return m.newConversation()
	case "/archive":
		return m.archiveConversation()
	case "/archived":
		return m.listArchivedConversations()
	case "/unarchive":
		if len(parts) < 2 {
			m.showError("Usage: /unarchive <conversation-id>")
			return nil
		}
		return m.unarchiveConversation(parts[1])
	case "/rename":
		if len(parts) < 2 {
			m.showError("Usage: /rename <new-slug>")
			return nil
		}
		return m.renameConversation(strings.Join(parts[1:], "-"))
	case "/delete":
		return m.confirmDeleteConversation()
	case "/yes", "/y":
		if m.pendingConfirm == "delete" {
			m.pendingConfirm = ""
			return m.deleteConversation()
		}
		m.showError("Nothing to confirm")
		return nil
	case "/no", "/n":
		if m.pendingConfirm != "" {
			m.pendingConfirm = ""
			m.showSystemMessage("Cancelled")
			return nil
		}
		m.showError("Nothing to cancel")
		return nil
	case "/search":
		if len(parts) < 2 {
			m.showError("Usage: /search <query>")
			return nil
		}
		return m.searchConversations(strings.Join(parts[1:], " "))

	// Model commands
	case "/models":
		return m.listModels()
	case "/model":
		if len(parts) < 2 {
			m.showError("Usage: /model <model-id>")
			return nil
		}
		return m.switchModel(parts[1])
	case "/fast":
		return m.switchModel(ModelHaiku)
	case "/smart":
		return m.switchModel(ModelSonnet)
	case "/think", "/opus":
		return m.switchModel(ModelOpus)
	case "/context":
		return m.showContext()
	case "/usage", "/cost":
		return m.showUsage()
	case "/theme":
		if len(parts) < 2 {
			// Toggle theme
			return m.toggleTheme()
		}
		return m.setTheme(parts[1])
	case "/cwd", "/cd":
		if len(parts) < 2 {
			// Show current working directory
			m.showSystemMessage("Working directory: " + m.config.WorkingDir)
			return nil
		}
		return m.changeWorkingDir(strings.Join(parts[1:], " "))
	case "/status":
		return m.showStatus()
	case "/export":
		filename := ""
		if len(parts) > 1 {
			filename = strings.Join(parts[1:], " ")
		}
		return m.exportConversation(filename)

	// Git commands
	case "/git":
		if len(parts) < 2 {
			return m.gitListCommits()
		}
		switch parts[1] {
		case "show":
			if len(parts) < 3 {
				m.showError("Usage: /git show <commit>")
				return nil
			}
			return m.gitShowCommit(parts[2])
		case "diff":
			if len(parts) < 3 {
				m.showError("Usage: /git diff <file>")
				return nil
			}
			return m.gitDiffFile(strings.Join(parts[2:], " "))
		default:
			m.showError("Unknown git subcommand: " + parts[1] + " (try: show, diff)")
			return nil
		}

	// Image attachment
	case "/attach", "/image":
		if len(parts) < 2 {
			m.showError("Usage: /attach <path-to-image>")
			return nil
		}
		return m.attachImage(strings.Join(parts[1:], " "))

	case "/attachments":
		return m.listAttachments()

	case "/describe":
		if len(parts) < 2 {
			m.showError("Usage: /describe <path-to-image> [prompt]")
			return nil
		}
		return m.describeImage(strings.Join(parts[1:], " "))

	case "/imgresult":
		if len(parts) < 2 {
			return m.injectImageResult(0) // 0 means most recent
		}
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			m.showError("Usage: /imgresult [n] - n is the result number from /imglist")
			return nil
		}
		return m.injectImageResult(idx)

	case "/imglist":
		return m.listImageResults()

	case "/help":
		m.showSystemMessage(m.buildHelpText())
		return nil
	case "/quit", "/exit", "/q":
		m.quitting = true
		if m.loopCancel != nil {
			m.loopCancel()
		}
		return tea.Quit
	default:
		m.showError("Unknown command: " + cmd + " (try /help)")
		return nil
	}
}

// buildHelpText generates help text based on available features
func (m *Model) buildHelpText() string {
	var sb strings.Builder
	sb.WriteString("Commands:\n")
	sb.WriteString("  /run [n]       - Run suggested command (n=index, default=last)\n")
	sb.WriteString("  /clear         - Clear conversation display\n")
	sb.WriteString("  /verbose       - Toggle tool detail visibility\n")
	sb.WriteString("  /stop          - Cancel current operation (or press Escape)\n")
	sb.WriteString("  /quit          - Exit the CLI\n")

	sb.WriteString("\nImages:\n")
	sb.WriteString("  /attach <path> - Attach image to next message (.png, .jpg, .gif, .webp)\n")
	sb.WriteString("  /attachments   - List pending attachments\n")
	sb.WriteString("  /describe <path> [prompt] - Analyze image with vision model\n")
	sb.WriteString("  /imglist       - List available image results\n")
	sb.WriteString("  /imgresult [n] - Inject image result as context (default: most recent)\n")

	if m.config.DB != nil {
		sb.WriteString("\nConversation Management (database enabled):\n")
		sb.WriteString("  /conversations - List recent conversations\n")
		sb.WriteString("  /search <q>    - Search conversations by content\n")
		sb.WriteString("  /switch <id>   - Switch to conversation by ID or slug\n")
		sb.WriteString("  /new           - Start a new conversation\n")
		sb.WriteString("  /rename <slug> - Rename current conversation\n")
		sb.WriteString("  /archive       - Archive current conversation\n")
		sb.WriteString("  /archived      - List archived conversations\n")
		sb.WriteString("  /unarchive <id>- Restore archived conversation\n")
		sb.WriteString("  /delete        - Delete current conversation\n")
	} else {
		sb.WriteString("\nSession Management (legacy JSON files):\n")
		sb.WriteString("  /save [name]   - Save session\n")
		sb.WriteString("  /load <name>   - Load a saved session\n")
		sb.WriteString("  /sessions      - List saved sessions\n")
	}

	if m.config.ModelManager != nil {
		sb.WriteString("\nModel & Context:\n")
		sb.WriteString("  /models        - List available models\n")
		sb.WriteString("  /model <id>    - Switch to a different model\n")
		sb.WriteString("  /fast          - Switch to Haiku (cheap & fast)\n")
		sb.WriteString("  /smart         - Switch to Sonnet (balanced)\n")
		sb.WriteString("  /think         - Switch to Opus (complex reasoning)\n")
		sb.WriteString("  /context       - Show context window usage\n")
	}

	sb.WriteString("\nDisplay & Navigation:\n")
	sb.WriteString("  /theme [name]  - Toggle or set theme (dark/light)\n")
	sb.WriteString("  /mouse         - Toggle mouse mode (off=select text, on=scroll)\n")
	sb.WriteString("  /cwd [path]    - Show or change working directory\n")
	sb.WriteString("  /status        - Show session status\n")
	sb.WriteString("  /export [file] - Export conversation to markdown\n")

	sb.WriteString("\nGit:\n")
	sb.WriteString("  /git           - List recent commits\n")
	sb.WriteString("  /git show <id> - Show files in commit\n")
	sb.WriteString("  /git diff <f>  - Show diff for file\n")

	sb.WriteString("\n  /help          - Show this help")
	sb.WriteString("\n\nTips:\n")
	sb.WriteString("  Tab            - Complete file paths and commands\n")
	sb.WriteString("  Up/Down        - Cycle through prompt history\n")
	sb.WriteString("  Ctrl+U/D       - Scroll message history (or PgUp/PgDown)")
	return sb.String()
}

// runSuggestedCommand executes a suggested shell command
func (m *Model) runSuggestedCommand(args []string) tea.Cmd {
	if len(m.suggestedCmds) == 0 {
		m.showError("No suggested commands to run")
		return nil
	}

	// Determine which command to run
	idx := len(m.suggestedCmds) - 1 // Default to last
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 && n <= len(m.suggestedCmds) {
			idx = n - 1
		}
	}

	cmdStr := m.suggestedCmds[idx]

	// Show what we're running
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleUser,
		content: m.styles.ToolName.Render("Running: ") + m.styles.ToolInput.Render(cmdStr),
	})
	m.updateViewportContent()

	// Execute the command
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = m.config.WorkingDir
	output, err := cmd.CombinedOutput()

	var resultContent string
	if err != nil {
		resultContent = m.styles.ToolError.Render(string(output) + "\n" + err.Error())
	} else {
		outStr := strings.TrimSpace(string(output))
		if outStr == "" {
			outStr = "(no output)"
		}
		resultContent = m.styles.ToolOutput.Render(outStr)
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: resultContent,
	})
	m.updateViewportContent()
	return nil
}
