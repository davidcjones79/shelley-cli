package cli

import (
	"context"
	"encoding/base64"
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
	"shelley.exe.dev/db"
	"shelley.exe.dev/db/generated"
	"shelley.exe.dev/gitstate"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/loop"
	"shelley.exe.dev/models"
)

// bashCodeBlockRe matches ```bash or ```sh code blocks
var bashCodeBlockRe = regexp.MustCompile("(?s)```(?:bash|sh|shell|zsh)\\s*\\n(.*?)```")

// slashCommandRe matches slash commands (e.g., /run, /help) but not file paths (e.g., /Users/foo)
// A slash command is / followed by lowercase letters only, then end-of-string or whitespace
var slashCommandRe = regexp.MustCompile(`^/[a-z]+(?:\s|$)`)

// imageAttachment holds a pending image to attach to the next message
type imageAttachment struct {
	path      string
	mediaType string
	data      string // base64 encoded
}

// supportedImageTypes maps file extensions to MIME types
var supportedImageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

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
	Model         string
	WorkingDir    string
	LLMService    llm.Service
	Logger        *slog.Logger
	System        []llm.SystemContent
	Verbose       bool // Show tool execution details
	EnableBrowser bool // Enable browser tools

	// Model manager for switching models (optional)
	ModelManager *models.Manager

	// Database integration (optional - enables conversation sync with web UI)
	DB             *db.DB
	ConversationID string // Resume specific conversation
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

	// Tab completion
	completions      []string
	completionIndex  int
	completionPrefix string // Original text before completion started

	// Message queue for messages sent while processing
	pendingMessages []string

	// Pending image attachments for next message
	pendingAttachments []imageAttachment

	// Shell integration - store suggested commands
	suggestedCmds []string

	// Verbosity - show tool details
	verbose bool

	// Session management (legacy JSON files)
	sessionName string

	// Database conversation (when DB is configured)
	conversationID string
	lastGitState   *gitstate.GitState

	// Confirmation state for destructive operations
	pendingConfirm string // e.g., "delete" when waiting for confirmation

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
		config:         cfg,
		textarea:       ta,
		spinner:        sp,
		viewport:       vp,
		renderer:       renderer,
		styles:         DefaultStyles(),
		messages:       []renderedMessage{},
		promptHistory:  []string{},
		historyIndex:   -1,
		responseChan:   make(chan responseMsg, 10),
		verbose:        cfg.Verbose,
		conversationID: cfg.ConversationID,
	}

	// If resuming a conversation, load and display history
	if cfg.DB != nil && cfg.ConversationID != "" {
		history, err := m.loadHistoryFromDB()
		if err != nil {
			cfg.Logger.Warn("Failed to load conversation history", "error", err)
		} else {
			for _, msg := range history {
				rendered := m.renderer.RenderMessage(msg, m.verbose)
				if rendered != "" {
					m.messages = append(m.messages, renderedMessage{
						role:    msg.Role,
						content: rendered,
					})
				}
			}
		}
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

		case tea.KeyTab:
			// Tab completion
			if !m.processing {
				return m, m.handleTabCompletion()
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
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

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

	// Update viewport for scrolling
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	// Update textarea - always allow typing, even while processing
	var cmd tea.Cmd
	oldValue := m.textarea.Value()
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	// Clear completions if text changed (except from tab completion itself)
	if m.textarea.Value() != oldValue && !strings.HasPrefix(m.textarea.Value(), m.completionPrefix) {
		m.clearCompletions()
	}

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

	// Handle slash commands (but not file paths like /Users/foo/bar.png)
	if slashCommandRe.MatchString(text) {
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

	// Auto-extract image paths from text (e.g., from drag-drop which pastes file path)
	cleanedText, imagePaths := extractImagePathsFromText(text, m.config.WorkingDir)
	for _, imgPath := range imagePaths {
		if att, err := loadImageAsAttachment(imgPath); err == nil {
			m.pendingAttachments = append(m.pendingAttachments, *att)
		}
	}
	if cleanedText != "" {
		text = cleanedText
	}

	// Build message content with text and any pending attachments
	var content []llm.Content

	// Add pending image attachments first
	for _, att := range m.pendingAttachments {
		content = append(content, llm.Content{
			Type:      llm.ContentTypeText,
			MediaType: att.mediaType,
			Data:      att.data,
		})
	}
	m.pendingAttachments = nil // Clear after use

	// Add text content
	content = append(content, llm.Content{Type: llm.ContentTypeText, Text: text})

	// Add user message to display
	userMsg := llm.Message{
		Role:    llm.MessageRoleUser,
		Content: content,
	}
	rendered := m.renderer.RenderMessage(userMsg, true) // Always show user messages
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleUser,
		content: rendered,
	})

	// Update viewport with new message
	m.updateViewportContent()

	return func() tea.Msg {
		// Create conversation in database on first message
		if m.config.DB != nil && m.conversationID == "" {
			if err := m.createConversation(context.Background()); err != nil {
				m.config.Logger.Error("Failed to create conversation", "error", err)
			}
		}

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

	// Get initial git state for change detection
	m.lastGitState = gitstate.GetGitState(m.config.WorkingDir)

	// Create toolset with browser support if enabled
	toolSetConfig := claudetool.ToolSetConfig{
		WorkingDir:    m.config.WorkingDir,
		ModelID:       m.config.Model,
		EnableBrowser: m.config.EnableBrowser,
	}
	m.toolSet = claudetool.NewToolSet(m.loopCtx, toolSetConfig)

	// Create the loop with message recording callback
	recordMessage := func(ctx context.Context, message llm.Message, usage llm.Usage) error {
		// Record to database if configured
		if m.config.DB != nil && m.conversationID != "" {
			if err := m.recordMessageToDB(ctx, message, usage); err != nil {
				m.config.Logger.Error("Failed to record message to DB", "error", err)
			}
		}

		// Send to response channel for UI update
		select {
		case m.responseChan <- responseMsg{message: message, usage: usage}:
		default:
			// Channel full, skip
		}
		return nil
	}

	// Load history from database if resuming a conversation
	var history []llm.Message
	if m.config.DB != nil && m.conversationID != "" {
		var err error
		history, err = m.loadHistoryFromDB()
		if err != nil {
			m.config.Logger.Warn("Failed to load history from DB", "error", err)
		}
	}

	m.loop = loop.NewLoop(loop.Config{
		LLM:           m.config.LLMService,
		History:       history,
		Tools:         m.toolSet.Tools(),
		RecordMessage: recordMessage,
		Logger:        m.config.Logger,
		System:        m.config.System,
		WorkingDir:    m.config.WorkingDir,
		GetWorkingDir: m.toolSet.WorkingDir().Get,
		OnGitStateChange: func(ctx context.Context, state *gitstate.GitState) {
			m.handleGitStateChange(ctx, state)
		},
	})

	return nil
}

// updateViewportContent rebuilds the viewport content from messages
func (m *Model) updateViewportContent() {
	var content strings.Builder

	for i, msg := range m.messages {
		if i > 0 {
			content.WriteString("\n") // Extra blank line between messages
		}
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

	// Token counts with context percentage
	if m.totalUsage.InputTokens > 0 || m.totalUsage.OutputTokens > 0 {
		tokenStr := fmt.Sprintf("↑%s ↓%s", formatTokens(m.totalUsage.InputTokens), formatTokens(m.totalUsage.OutputTokens))
		// Add context percentage if we can calculate it
		if m.config.LLMService != nil {
			maxContext := m.config.LLMService.TokenContextWindow()
			if maxContext > 0 && m.totalUsage.InputTokens > 0 {
				percent := float64(m.totalUsage.InputTokens) / float64(maxContext) * 100
				tokenStr += fmt.Sprintf(" (%.0f%%)", percent)
			}
		}
		parts = append(parts, m.styles.TokenCount.Render(tokenStr))
	}

	// Working directory - aggressively truncate
	if m.config.WorkingDir != "" {
		wd := m.config.WorkingDir
		// Replace home dir with ~
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(wd, home) {
			wd = "~" + wd[len(home):]
		}
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

	// Database conversation commands
	case "/conversations", "/convos":
		return m.listConversations()
	case "/switch":
		if len(parts) < 2 {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /switch <conversation-id>"),
			})
			m.updateViewportContent()
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
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /unarchive <conversation-id>"),
			})
			m.updateViewportContent()
			return nil
		}
		return m.unarchiveConversation(parts[1])
	case "/rename":
		if len(parts) < 2 {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /rename <new-slug>"),
			})
			m.updateViewportContent()
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
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Nothing to confirm"),
		})
		m.updateViewportContent()
		return nil
	case "/no", "/n":
		if m.pendingConfirm != "" {
			m.pendingConfirm = ""
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.SystemMessage.Render("Cancelled"),
			})
			m.updateViewportContent()
			return nil
		}
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Nothing to cancel"),
		})
		m.updateViewportContent()
		return nil
	case "/search":
		if len(parts) < 2 {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /search <query>"),
			})
			m.updateViewportContent()
			return nil
		}
		return m.searchConversations(strings.Join(parts[1:], " "))

	// Model commands
	case "/models":
		return m.listModels()
	case "/model":
		if len(parts) < 2 {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /model <model-id>"),
			})
			m.updateViewportContent()
			return nil
		}
		return m.switchModel(parts[1])
	case "/fast":
		return m.switchModel("claude-haiku-4.5")
	case "/smart":
		return m.switchModel("claude-sonnet-4.5")
	case "/think", "/opus":
		return m.switchModel("claude-opus-4.5")
	case "/context":
		return m.showContext()
	case "/theme":
		if len(parts) < 2 {
			// Toggle theme
			return m.toggleTheme()
		}
		return m.setTheme(parts[1])
	case "/cwd", "/cd":
		if len(parts) < 2 {
			// Show current working directory
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.SystemMessage.Render("Working directory: " + m.config.WorkingDir),
			})
			m.updateViewportContent()
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
				m.messages = append(m.messages, renderedMessage{
					role:    llm.MessageRoleAssistant,
					content: m.styles.ErrorMessage.Render("Usage: /git show <commit>"),
				})
				m.updateViewportContent()
				return nil
			}
			return m.gitShowCommit(parts[2])
		case "diff":
			if len(parts) < 3 {
				m.messages = append(m.messages, renderedMessage{
					role:    llm.MessageRoleAssistant,
					content: m.styles.ErrorMessage.Render("Usage: /git diff <file>"),
				})
				m.updateViewportContent()
				return nil
			}
			return m.gitDiffFile(strings.Join(parts[2:], " "))
		default:
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Unknown git subcommand: " + parts[1] + " (try: show, diff)"),
			})
			m.updateViewportContent()
			return nil
		}

	// Image attachment
	case "/attach", "/image":
		if len(parts) < 2 {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Usage: /attach <path-to-image>"),
			})
			m.updateViewportContent()
			return nil
		}
		return m.attachImage(strings.Join(parts[1:], " "))

	case "/attachments":
		return m.listAttachments()

	case "/help":
		helpText := m.buildHelpText()
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

// buildHelpText generates help text based on available features
func (m *Model) buildHelpText() string {
	var sb strings.Builder
	sb.WriteString("Commands:\n")
	sb.WriteString("  /run [n]       - Run suggested command (n=index, default=last)\n")
	sb.WriteString("  /clear         - Clear conversation display\n")
	sb.WriteString("  /verbose       - Toggle tool detail visibility\n")
	sb.WriteString("  /stop          - Cancel current operation (or press Escape)\n")

	sb.WriteString("\nImage Attachments:\n")
	sb.WriteString("  /attach <path> - Attach image to next message (.png, .jpg, .gif, .webp)\n")
	sb.WriteString("  /attachments   - List pending attachments\n")

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
	sb.WriteString("  PgUp/PgDown    - Scroll message history")
	return sb.String()
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

// recordMessageToDB records a message to the database
func (m *Model) recordMessageToDB(ctx context.Context, message llm.Message, usage llm.Usage) error {
	if m.config.DB == nil || m.conversationID == "" {
		return nil
	}

	// Determine message type
	var msgType db.MessageType
	switch message.Role {
	case llm.MessageRoleUser:
		msgType = db.MessageTypeUser
	case llm.MessageRoleAssistant:
		msgType = db.MessageTypeAgent
	default:
		msgType = db.MessageTypeAgent
	}

	// Check for tool content
	for _, content := range message.Content {
		if content.Type == llm.ContentTypeToolUse || content.Type == llm.ContentTypeToolResult {
			msgType = db.MessageTypeTool
			break
		}
	}

	_, err := m.config.DB.CreateMessage(ctx, db.CreateMessageParams{
		ConversationID: m.conversationID,
		Type:           msgType,
		LLMData:        message,
		UsageData:      usage,
	})
	return err
}

// loadHistoryFromDB loads conversation history from the database
func (m *Model) loadHistoryFromDB() ([]llm.Message, error) {
	if m.config.DB == nil || m.conversationID == "" {
		return nil, nil
	}

	var messages []generated.Message
	err := m.config.DB.Queries(context.Background(), func(q *generated.Queries) error {
		var err error
		messages, err = q.ListMessages(context.Background(), m.conversationID)
		return err
	})
	if err != nil {
		return nil, err
	}

	var history []llm.Message
	for _, msg := range messages {
		// Skip system and gitinfo messages
		if msg.Type == string(db.MessageTypeSystem) || msg.Type == string(db.MessageTypeGitInfo) {
			continue
		}
		if msg.LlmData == nil {
			continue
		}
		var llmMsg llm.Message
		if err := json.Unmarshal([]byte(*msg.LlmData), &llmMsg); err != nil {
			continue
		}
		history = append(history, llmMsg)
	}
	return history, nil
}

// createConversation creates a new conversation in the database
func (m *Model) createConversation(ctx context.Context) error {
	if m.config.DB == nil {
		return nil
	}

	cwd := m.config.WorkingDir
	conv, err := m.config.DB.CreateConversation(ctx, nil, true, &cwd)
	if err != nil {
		return err
	}
	m.conversationID = conv.ConversationID
	return nil
}

// handleGitStateChange handles git state changes for display
func (m *Model) handleGitStateChange(ctx context.Context, state *gitstate.GitState) {
	if state == nil || !state.IsRepo {
		return
	}

	// Check if state actually changed
	if m.lastGitState != nil && state.Equal(m.lastGitState) {
		return
	}
	m.lastGitState = state

	// Display git info message
	gitMsg := fmt.Sprintf("📦 %s", state.String())
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(gitMsg),
	})
	m.updateViewportContent()

	// Record to database if configured
	if m.config.DB != nil && m.conversationID != "" {
		message := llm.Message{
			Role:    llm.MessageRoleAssistant,
			Content: []llm.Content{{Type: llm.ContentTypeText, Text: state.String()}},
		}
		m.config.DB.CreateMessage(ctx, db.CreateMessageParams{
			ConversationID: m.conversationID,
			Type:           db.MessageTypeGitInfo,
			LLMData:        message,
		})
	}
}

// listConversations lists recent conversations from the database
// listModels lists available models
func (m *Model) listModels() tea.Cmd {
	if m.config.ModelManager == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Model manager not configured"),
		})
		m.updateViewportContent()
		return nil
	}

	available := m.config.ModelManager.GetAvailableModels()
	if len(available) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No models available"),
		})
		m.updateViewportContent()
		return nil
	}

	allModels := models.All()
	modelMap := make(map[string]models.Model)
	for _, model := range allModels {
		modelMap[model.ID] = model
	}

	var sb strings.Builder
	sb.WriteString("Available models:\n")
	for _, id := range available {
		marker := "  "
		if id == m.config.Model {
			marker = "→ "
		}
		line := fmt.Sprintf("%s%s", marker, id)
		if model, ok := modelMap[id]; ok {
			line += fmt.Sprintf(" (%s)", model.Provider)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /model <id> to switch models")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// switchModel switches to a different model
func (m *Model) switchModel(modelID string) tea.Cmd {
	if m.config.ModelManager == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Model manager not configured"),
		})
		m.updateViewportContent()
		return nil
	}

	// Check if model is available
	if !m.config.ModelManager.HasModel(modelID) {
		available := m.config.ModelManager.GetAvailableModels()
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render(fmt.Sprintf("Model %q not available. Available: %s", modelID, strings.Join(available, ", "))),
		})
		m.updateViewportContent()
		return nil
	}

	// Get the new service
	newService, err := m.config.ModelManager.GetService(modelID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get model: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Update the config
	oldModel := m.config.Model
	m.config.Model = modelID
	m.config.LLMService = newService

	// If we have an active loop, we need to restart it with the new model
	// The next message will create a new loop with the updated service
	if m.loop != nil {
		if m.loopCancel != nil {
			m.loopCancel()
		}
		m.loop = nil
		m.loopCtx = nil
		m.loopCancel = nil
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render(fmt.Sprintf("Switched model: %s → %s", oldModel, modelID)),
	})
	m.updateViewportContent()
	return nil
}

// showContext shows context window usage information
func (m *Model) showContext() tea.Cmd {
	maxContext := 0
	if m.config.LLMService != nil {
		maxContext = m.config.LLMService.TokenContextWindow()
	}

	// Calculate approximate context usage from total usage
	// Input tokens represent what we've sent to the model
	usedTokens := m.totalUsage.InputTokens

	var sb strings.Builder
	sb.WriteString("Context Window:\n")
	sb.WriteString(fmt.Sprintf("  Model: %s\n", m.config.Model))
	sb.WriteString(fmt.Sprintf("  Max tokens: %s\n", formatTokensInt(maxContext)))
	sb.WriteString(fmt.Sprintf("  Input tokens used: %s\n", formatTokens(usedTokens)))
	sb.WriteString(fmt.Sprintf("  Output tokens used: %s\n", formatTokens(m.totalUsage.OutputTokens)))

	if maxContext > 0 {
		percent := float64(usedTokens) / float64(maxContext) * 100
		sb.WriteString(fmt.Sprintf("  Usage: %.1f%%\n", percent))

		// Visual bar
		barWidth := 40
		filled := int(percent / 100 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		sb.WriteString(fmt.Sprintf("  [%s]", bar))
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// toggleTheme switches between dark and light themes
func (m *Model) toggleTheme() tea.Cmd {
	if m.styles.Theme() == ThemeDark {
		return m.setTheme("light")
	}
	return m.setTheme("dark")
}

// setTheme changes to a specific theme
func (m *Model) setTheme(themeName string) tea.Cmd {
	var newStyles *Styles
	switch themeName {
	case "dark":
		newStyles = DarkStyles()
	case "light":
		newStyles = LightStyles()
	default:
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Unknown theme: " + themeName + " (available: dark, light)"),
		})
		m.updateViewportContent()
		return nil
	}

	m.styles = newStyles
	m.renderer.styles = newStyles

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Theme set to: " + themeName),
	})
	m.updateViewportContent()
	return nil
}

// changeWorkingDir changes the working directory
func (m *Model) changeWorkingDir(path string) tea.Cmd {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		}
	}

	// Make path absolute if relative
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.config.WorkingDir, path)
	}

	// Clean the path
	path = filepath.Clean(path)

	// Check if path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Directory not found: " + path),
		})
		m.updateViewportContent()
		return nil
	}
	if !info.IsDir() {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Not a directory: " + path),
		})
		m.updateViewportContent()
		return nil
	}

	oldDir := m.config.WorkingDir
	m.config.WorkingDir = path

	// Update the toolset's working directory if it exists
	if m.toolSet != nil {
		m.toolSet.WorkingDir().Set(path)
	}

	// Format paths for display
	oldDisplay := oldDir
	newDisplay := path
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(oldDisplay, home) {
			oldDisplay = "~" + oldDisplay[len(home):]
		}
		if strings.HasPrefix(newDisplay, home) {
			newDisplay = "~" + newDisplay[len(home):]
		}
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render(fmt.Sprintf("Changed directory: %s → %s", oldDisplay, newDisplay)),
	})
	m.updateViewportContent()
	return nil
}

// showStatus shows current session status
func (m *Model) showStatus() tea.Cmd {
	var sb strings.Builder
	sb.WriteString("Session Status:\n")

	// Model
	sb.WriteString(fmt.Sprintf("  Model: %s\n", m.styles.ModelName.Render(m.config.Model)))

	// Working directory
	wd := m.config.WorkingDir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(wd, home) {
		wd = "~" + wd[len(home):]
	}
	sb.WriteString(fmt.Sprintf("  Working Dir: %s\n", m.styles.WorkingDir.Render(wd)))

	// Conversation
	if m.conversationID != "" {
		sb.WriteString(fmt.Sprintf("  Conversation: %s\n", m.conversationID))
	} else {
		sb.WriteString("  Conversation: (none)\n")
	}

	// Context usage
	if m.config.LLMService != nil {
		maxContext := m.config.LLMService.TokenContextWindow()
		if maxContext > 0 && m.totalUsage.InputTokens > 0 {
			percent := float64(m.totalUsage.InputTokens) / float64(maxContext) * 100
			sb.WriteString(fmt.Sprintf("  Context: %s / %s (%.1f%%)\n",
				formatTokens(m.totalUsage.InputTokens),
				formatTokensInt(maxContext),
				percent))
		} else {
			sb.WriteString(fmt.Sprintf("  Context: %s used\n", formatTokens(m.totalUsage.InputTokens)))
		}
	}

	// Token totals
	sb.WriteString(fmt.Sprintf("  Tokens: ↑%s ↓%s\n",
		formatTokens(m.totalUsage.InputTokens),
		formatTokens(m.totalUsage.OutputTokens)))

	// Theme
	sb.WriteString(fmt.Sprintf("  Theme: %s\n", m.styles.Theme()))

	// Processing state
	if m.processing {
		sb.WriteString("  State: " + m.styles.ToolRunning.Render("processing...") + "\n")
	} else {
		sb.WriteString("  State: ready\n")
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// exportConversation exports the conversation to a markdown file
func (m *Model) attachImage(path string) tea.Cmd {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	// Make path absolute if relative
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.config.WorkingDir, path)
	}

	// Check file exists
	info, err := os.Stat(path)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("File not found: " + path),
		})
		m.updateViewportContent()
		return nil
	}

	if info.IsDir() {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Path is a directory, not a file"),
		})
		m.updateViewportContent()
		return nil
	}

	// Check file size (limit to 10MB)
	if info.Size() > 10*1024*1024 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("File too large (max 10MB)"),
		})
		m.updateViewportContent()
		return nil
	}

	// Check file extension
	ext := strings.ToLower(filepath.Ext(path))
	mediaType, ok := supportedImageTypes[ext]
	if !ok {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Unsupported image type: " + ext + " (supported: .png, .jpg, .jpeg, .gif, .webp)"),
		})
		m.updateViewportContent()
		return nil
	}

	// Read and encode the file
	data, err := os.ReadFile(path)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to read file: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Base64 encode
	encoded := base64.StdEncoding.EncodeToString(data)

	// Add to pending attachments
	m.pendingAttachments = append(m.pendingAttachments, imageAttachment{
		path:      path,
		mediaType: mediaType,
		data:      encoded,
	})

	// Show confirmation
	filename := filepath.Base(path)
	sizeKB := float64(info.Size()) / 1024
	msg := fmt.Sprintf("🖼️  Attached: %s (%.1fKB, %s)", filename, sizeKB, mediaType)
	if len(m.pendingAttachments) > 1 {
		msg += fmt.Sprintf(" [%d attachments pending]", len(m.pendingAttachments))
	}
	msg += "\nType your message and press Enter to send with attachment(s)"

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render(msg),
	})
	m.updateViewportContent()
	return nil
}

// listAttachments shows pending attachments
func (m *Model) listAttachments() tea.Cmd {
	if len(m.pendingAttachments) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No pending attachments"),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Pending attachments:\n")
	for i, att := range m.pendingAttachments {
		sb.WriteString(fmt.Sprintf("  %d. %s (%s)\n", i+1, filepath.Base(att.path), att.mediaType))
	}
	sb.WriteString("\nThese will be sent with your next message")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// handleTabCompletion handles tab key for completion
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

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}
