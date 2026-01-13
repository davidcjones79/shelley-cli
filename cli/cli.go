package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"shelley.exe.dev/claudetool"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/loop"
)

// bashCodeBlockRe matches ```bash or ```sh code blocks
var bashCodeBlockRe = regexp.MustCompile("(?s)```(?:bash|sh|shell|zsh)\\s*\\n(.*?)```")

// Session represents a saved chat session
type Session struct {
	Name       string             `json:"name"`
	Model      string             `json:"model"`
	WorkingDir string             `json:"working_dir"`
	Messages   []SessionMessage   `json:"messages"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// SessionMessage is a simplified message for storage
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Config holds configuration for the CLI
type Config struct {
	Model      string
	WorkingDir string
	LLMService llm.Service
	Logger     *slog.Logger
	System     []llm.SystemContent
	Verbose    bool // Show tool execution details
}

// Model is the Bubble Tea model for the CLI
type Model struct {
	config     Config
	loop       *loop.Loop
	toolSet    *claudetool.ToolSet
	loopCtx    context.Context
	loopCancel context.CancelFunc

	// UI state
	textarea      textarea.Model
	spinner       spinner.Model
	viewport      viewport.Model
	renderer      *MessageRenderer
	styles        *Styles
	messages      []renderedMessage
	width, height int
	ready         bool
	viewportReady bool
	processing    bool
	err           error
	totalUsage    llm.Usage
	quitting      bool

	// Prompt history
	promptHistory []string
	historyIndex  int
	currentInput  string // Saves current input when cycling through history

	// Message queue for messages sent while processing
	pendingMessages []string

	// Shell integration - store suggested commands
	suggestedCmds []string

	// Verbosity - show tool details
	verbose bool

	// Session management
	sessionName string

	// Channels for async communication
	responseChan chan responseMsg
}

// getSessionsDir returns the directory for storing sessions
func getSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".shelley", "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// renderedMessage holds a pre-rendered message for display
type renderedMessage struct {
	role    llm.MessageRole
	content string
}

// Message types for Bubble Tea
type (
	responseMsg struct {
		message llm.Message
		usage   llm.Usage
		err     error
	}
	processingDoneMsg struct{}
	errMsg            struct{ err error }
)

// New creates a new CLI model
func New(cfg Config) (*Model, error) {
	if cfg.LLMService == nil {
		return nil, fmt.Errorf("LLM service is required")
	}

	if cfg.WorkingDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			cfg.WorkingDir = "/"
		} else {
			cfg.WorkingDir = wd
		}
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Create textarea for input
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Enter to send, Ctrl+C to quit)"
	ta.Focus()
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	// Create spinner
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	// Create renderer (will be recreated with proper width on WindowSizeMsg)
	renderer, err := NewMessageRenderer(80)
	if err != nil {
		return nil, err
	}

	// Create viewport (will be properly sized on WindowSizeMsg)
	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	m := &Model{
		config:        cfg,
		textarea:      ta,
		spinner:       sp,
		viewport:      vp,
		renderer:      renderer,
		styles:        DefaultStyles(),
		messages:      []renderedMessage{},
		promptHistory: []string{},
		historyIndex:  -1,
		responseChan:  make(chan responseMsg, 10),
		verbose:       cfg.Verbose,
	}

	return m, nil
}

// Init implements tea.Model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForResponse(),
	)
}

// waitForResponse returns a command that waits for responses from the loop
func (m *Model) waitForResponse() tea.Cmd {
	return func() tea.Msg {
		resp := <-m.responseChan
		return resp
	}
}

// Update implements tea.Model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			if m.loopCancel != nil {
				m.loopCancel()
			}
			return m, tea.Quit

		case tea.KeyEscape:
			// Cancel current operation
			if m.processing && m.loopCancel != nil {
				m.loopCancel()
				m.processing = false
				m.pendingMessages = nil
				// Reset loop so next message starts fresh
				m.loop = nil
				m.loopCtx, m.loopCancel = nil, nil
				m.messages = append(m.messages, renderedMessage{
					role:    llm.MessageRoleAssistant,
					content: m.styles.SystemMessage.Render("Cancelled"),
				})
				m.updateViewportContent()
			}

		case tea.KeyEnter:
			// Send message on Enter
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" {
				if m.processing {
					// Queue message for later processing
					m.pendingMessages = append(m.pendingMessages, text)
					m.textarea.Reset()
					// Show queued feedback
					m.messages = append(m.messages, renderedMessage{
						role:    llm.MessageRoleUser,
						content: m.styles.SystemMessage.Render(fmt.Sprintf("Message queued (%d pending)", len(m.pendingMessages))),
					})
					m.updateViewportContent()
				} else {
					return m, m.sendMessage()
				}
			}

		case tea.KeyUp:
			// Cycle to previous prompt in history
			if !m.processing && len(m.promptHistory) > 0 {
				if m.historyIndex == -1 {
					// Save current input before cycling
					m.currentInput = m.textarea.Value()
					m.historyIndex = len(m.promptHistory) - 1
				} else if m.historyIndex > 0 {
					m.historyIndex--
				}
				m.textarea.SetValue(m.promptHistory[m.historyIndex])
				m.textarea.CursorEnd()
			}

		case tea.KeyDown:
			// Cycle to next prompt in history
			if !m.processing && m.historyIndex != -1 {
				if m.historyIndex < len(m.promptHistory)-1 {
					m.historyIndex++
					m.textarea.SetValue(m.promptHistory[m.historyIndex])
				} else {
					// Return to current input
					m.historyIndex = -1
					m.textarea.SetValue(m.currentInput)
				}
				m.textarea.CursorEnd()
			}

		case tea.KeyPgUp:
			// Page up in viewport
			if m.viewportReady {
				m.viewport.ViewUp()
			}

		case tea.KeyPgDown:
			// Page down in viewport
			if m.viewportReady {
				m.viewport.ViewDown()
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Calculate heights for each section:
		// Header: 1 line
		// Footer: 4 lines (status bar + divider + prompt line + textarea line)
		headerHeight := 1
		footerHeight := 4

		viewportHeight := msg.Height - headerHeight - footerHeight
		if viewportHeight < 3 {
			viewportHeight = 3
		}

		// Resize viewport
		m.viewport.Width = msg.Width
		m.viewport.Height = viewportHeight

		// Update textarea width
		m.textarea.SetWidth(msg.Width - 4)

		// Recreate renderer with new width
		renderer, err := NewMessageRenderer(msg.Width - 4)
		if err == nil {
			m.renderer = renderer
		}

		// Update viewport content
		m.updateViewportContent()

		if !m.ready {
			m.ready = true
			m.viewportReady = true
		}

	case spinner.TickMsg:
		if m.processing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case responseMsg:
		if msg.err != nil {
			m.err = msg.err
			rendered := m.renderer.RenderError(msg.err)
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: rendered,
			})
		} else {
			// Render and add the message
			rendered := m.renderer.RenderMessage(msg.message, m.verbose)
			if rendered != "" {
				m.messages = append(m.messages, renderedMessage{
					role:    msg.message.Role,
					content: rendered,
				})
			}
			m.totalUsage.Add(msg.usage)

			// Extract bash commands from assistant text responses
			if msg.message.Role == llm.MessageRoleAssistant {
				for _, content := range msg.message.Content {
					if content.Type == llm.ContentTypeText {
						cmds := extractBashCommands(content.Text)
						m.suggestedCmds = append(m.suggestedCmds, cmds...)
					}
				}
			}
		}

		// Update viewport content and scroll to bottom
		m.updateViewportContent()

		// Check if we should stop processing
		if msg.message.EndOfTurn || msg.err != nil {
			m.processing = false
		}

		// Continue waiting for responses
		cmds = append(cmds, m.waitForResponse())

	case processingDoneMsg:
		m.processing = false
		// Process any pending messages
		if len(m.pendingMessages) > 0 {
			nextMsg := m.pendingMessages[0]
			m.pendingMessages = m.pendingMessages[1:]
			m.textarea.SetValue(nextMsg)
			return m, m.sendMessage()
		}

	case errMsg:
		m.err = msg.err
		m.processing = false
		// Clear pending messages on error
		m.pendingMessages = nil
	}

	// Update textarea - always allow typing, even while processing
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	// Update placeholder based on state
	if m.processing {
		m.textarea.Placeholder = "Type next message (will be queued)..."
	} else {
		m.textarea.Placeholder = "Type your message... (Enter to send, Ctrl+C to quit)"
	}

	return m, tea.Batch(cmds...)
}

// sendMessage sends the current textarea content as a message
func (m *Model) sendMessage() tea.Cmd {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return nil
	}

	// Handle slash commands
	if strings.HasPrefix(text, "/") {
		return m.handleSlashCommand(text)
	}

	// Save to prompt history
	m.promptHistory = append(m.promptHistory, text)
	m.historyIndex = -1
	m.currentInput = ""

	m.textarea.Reset()
	m.processing = true
	m.err = nil

	// Clear suggested commands for new conversation turn
	m.suggestedCmds = nil

	// Add user message to display
	userMsg := llm.Message{
		Role:    llm.MessageRoleUser,
		Content: []llm.Content{{Type: llm.ContentTypeText, Text: text}},
	}
	rendered := m.renderer.RenderMessage(userMsg, true) // Always show user messages
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleUser,
		content: rendered,
	})

	// Update viewport with new message
	m.updateViewportContent()

	return func() tea.Msg {
		// Initialize loop if needed
		if m.loop == nil {
			if err := m.initLoop(); err != nil {
				return errMsg{err}
			}
		}

		// Queue the message
		m.loop.QueueUserMessage(userMsg)

		// Process the turn
		if err := m.loop.ProcessOneTurn(m.loopCtx); err != nil {
			return errMsg{err}
		}

		return processingDoneMsg{}
	}
}

// initLoop initializes the agent loop
func (m *Model) initLoop() error {
	m.loopCtx, m.loopCancel = context.WithCancel(context.Background())

	// Create toolset
	toolSetConfig := claudetool.ToolSetConfig{
		WorkingDir: m.config.WorkingDir,
		ModelID:    m.config.Model,
	}
	m.toolSet = claudetool.NewToolSet(m.loopCtx, toolSetConfig)

	// Create the loop with message recording callback
	recordMessage := func(ctx context.Context, message llm.Message, usage llm.Usage) error {
		// Send to response channel for UI update
		select {
		case m.responseChan <- responseMsg{message: message, usage: usage}:
		default:
			// Channel full, skip
		}
		return nil
	}

	m.loop = loop.NewLoop(loop.Config{
		LLM:           m.config.LLMService,
		History:       []llm.Message{},
		Tools:         m.toolSet.Tools(),
		RecordMessage: recordMessage,
		Logger:        m.config.Logger,
		System:        m.config.System,
		WorkingDir:    m.config.WorkingDir,
		GetWorkingDir: m.toolSet.WorkingDir().Get,
	})

	return nil
}

// updateViewportContent rebuilds the viewport content from messages
func (m *Model) updateViewportContent() {
	var content strings.Builder

	for _, msg := range m.messages {
		content.WriteString(msg.content)
		content.WriteString("\n")
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// renderStatusBar creates the status bar with model, tokens, and cwd
func (m *Model) renderStatusBar() string {
	// Simple single-line status without background styling
	var parts []string

	// Model name (shortened)
	if m.config.Model != "" {
		model := m.config.Model
		if len(model) > 18 {
			model = model[:18]
		}
		parts = append(parts, m.styles.ModelName.Render(model))
	}

	// Token counts
	if m.totalUsage.InputTokens > 0 || m.totalUsage.OutputTokens > 0 {
		tokenStr := fmt.Sprintf("↑%d ↓%d", m.totalUsage.InputTokens, m.totalUsage.OutputTokens)
		parts = append(parts, m.styles.TokenCount.Render(tokenStr))
	}

	// Working directory - aggressively truncate
	if m.config.WorkingDir != "" {
		wd := m.config.WorkingDir
		maxLen := 30
		if len(wd) > maxLen {
			wd = "..." + wd[len(wd)-maxLen+3:]
		}
		parts = append(parts, m.styles.WorkingDir.Render(wd))
	}

	return strings.Join(parts, " | ")
}

// View implements tea.Model
func (m *Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	if !m.ready {
		return "Initializing...\n"
	}

	// ========== HEADER (always visible at top) ==========
	title := m.styles.HeaderTitle.Render("Shelley CLI")
	model := m.styles.ModelName.Render(m.config.Model)

	// Calculate padding to right-align the model name
	titleWidth := lipgloss.Width(title)
	modelWidth := lipgloss.Width(model)
	padding := m.width - titleWidth - modelWidth - 4 // -4 for padding on sides
	if padding < 1 {
		padding = 1
	}

	headerContent := title + strings.Repeat(" ", padding) + model
	headerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("15")).
		Padding(0, 1)
	header := headerStyle.Render(headerContent)

	// ========== MESSAGES VIEWPORT ==========
	viewport := m.viewport.View()

	// ========== FOOTER (status + input) ==========
	// Horizontal divider line above input
	divider := m.styles.Divider.Render(strings.Repeat("─", m.width))

	var footer string
	if m.processing {
		statusLine := m.spinner.View() + " " + m.styles.Thinking.Render("Agent working...")
		if len(m.pendingMessages) > 0 {
			statusLine += m.styles.SystemMessage.Render(fmt.Sprintf(" (%d queued)", len(m.pendingMessages)))
		}
		// Show input even while processing so user can queue messages
		footer = statusLine + "\n" + divider + "\n" + m.renderer.RenderPrompt() + m.textarea.View()
	} else {
		footer = m.renderStatusBar() + "\n" + divider + "\n" + m.renderer.RenderPrompt() + m.textarea.View()
	}

	// Join all sections vertically
	return lipgloss.JoinVertical(lipgloss.Left, header, viewport, footer)
}

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
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.SystemMessage.Render("Cancelled"),
			})
			m.updateViewportContent()
		} else {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.SystemMessage.Render("Nothing to cancel"),
			})
			m.updateViewportContent()
		}
		return nil
	case "/verbose":
		m.verbose = !m.verbose
		status := "off"
		if m.verbose {
			status = "on"
		}
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("Verbose mode: " + status),
		})
		m.updateViewportContent()
		return nil
	case "/help":
		helpText := `/run [n]   - Run suggested command (n=index, default=last)
/save [name] - Save session (auto-names if omitted)
/load <name> - Load a saved session
/sessions    - List saved sessions
/clear       - Clear conversation
/verbose     - Toggle tool detail visibility
/stop        - Cancel current operation (or press Escape)
/help        - Show this help`
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render(helpText),
		})
		m.updateViewportContent()
		return nil
	default:
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Unknown command: " + cmd + " (try /help)"),
		})
		m.updateViewportContent()
		return nil
	}
}

// runSuggestedCommand executes a suggested shell command
func (m *Model) runSuggestedCommand(args []string) tea.Cmd {
	if len(m.suggestedCmds) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("No suggested commands to run"),
		})
		m.updateViewportContent()
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

// extractBashCommands extracts bash code blocks from text
func extractBashCommands(text string) []string {
	matches := bashCodeBlockRe.FindAllStringSubmatch(text, -1)
	var cmds []string
	for _, match := range matches {
		if len(match) > 1 {
			cmd := strings.TrimSpace(match[1])
			if cmd != "" {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// saveSession saves the current session to a file
func (m *Model) saveSession(name string) tea.Cmd {
	if len(m.messages) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Nothing to save"),
		})
		m.updateViewportContent()
		return nil
	}

	// Generate name if not provided
	if name == "" {
		if m.sessionName != "" {
			name = m.sessionName
		} else {
			name = time.Now().Format("2006-01-02_15-04-05")
		}
	}
	m.sessionName = name

	// Build session
	session := Session{
		Name:       name,
		Model:      m.config.Model,
		WorkingDir: m.config.WorkingDir,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	for _, msg := range m.messages {
		session.Messages = append(session.Messages, SessionMessage{
			Role:    msg.role.String(),
			Content: msg.content,
		})
	}

	// Save to file
	dir, err := getSessionsDir()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get sessions dir: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	filePath := filepath.Join(dir, name+".json")
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to marshal session: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to save session: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Session saved: " + name),
	})
	m.updateViewportContent()
	return nil
}

// loadSession loads a session from a file
func (m *Model) loadSession(name string) tea.Cmd {
	dir, err := getSessionsDir()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get sessions dir: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	filePath := filepath.Join(dir, name+".json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to load session: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to parse session: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Restore messages
	m.messages = nil
	for _, msg := range session.Messages {
		var role llm.MessageRole
		switch msg.Role {
		case "user":
			role = llm.MessageRoleUser
		case "assistant":
			role = llm.MessageRoleAssistant
		default:
			role = llm.MessageRoleUser
		}
		m.messages = append(m.messages, renderedMessage{
			role:    role,
			content: msg.Content,
		})
	}
	m.sessionName = session.Name
	m.suggestedCmds = nil

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Session loaded: " + name),
	})
	m.updateViewportContent()
	return nil
}

// listSessions lists all saved sessions
func (m *Model) listSessions() tea.Cmd {
	dir, err := getSessionsDir()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get sessions dir: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to read sessions: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	var sessions []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			name := strings.TrimSuffix(entry.Name(), ".json")
			sessions = append(sessions, name)
		}
	}

	if len(sessions) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No saved sessions"),
		})
	} else {
		list := "Saved sessions:\n"
		for _, s := range sessions {
			list += "  • " + s + "\n"
		}
		list += "\nUse /load <name> to load"
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render(list),
		})
	}
	m.updateViewportContent()
	return nil
}

// Cleanup releases resources
func (m *Model) Cleanup() {
	if m.loopCancel != nil {
		m.loopCancel()
	}
	if m.toolSet != nil {
		m.toolSet.Cleanup()
	}
}

// Run starts the CLI application
func Run(cfg Config) error {
	model, err := New(cfg)
	if err != nil {
		return err
	}
	defer model.Cleanup()

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
