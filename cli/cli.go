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
		sb.WriteString("  /context       - Show context window usage\n")
	}

	sb.WriteString("\nDisplay & Navigation:\n")
	sb.WriteString("  /theme [name]  - Toggle or set theme (dark/light)\n")
	sb.WriteString("  /cwd [path]    - Show or change working directory\n")
	sb.WriteString("  /status        - Show session status\n")

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
func (m *Model) listConversations() tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -db flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	conversations, err := m.config.DB.ListConversations(context.Background(), 20, 0)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to list conversations: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if len(conversations) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No conversations found"),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Recent conversations:\n")
	for _, conv := range conversations {
		marker := "  "
		if conv.ConversationID == m.conversationID {
			marker = "→ "
		}
		name := conv.ConversationID
		if conv.Slug != nil && *conv.Slug != "" {
			name = *conv.Slug
		}
		// Format: marker name (time) [cwd]
		line := fmt.Sprintf("%s%s (%s)", marker, name, conv.UpdatedAt.Format("Jan 2 15:04"))
		if conv.Cwd != nil && *conv.Cwd != "" {
			cwd := *conv.Cwd
			// Shorten home directory to ~
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(cwd, home) {
				cwd = "~" + cwd[len(home):]
			}
			line += fmt.Sprintf(" [%s]", cwd)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /switch <id> to switch, /search <query> to search")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// switchConversation switches to a different conversation
func (m *Model) switchConversation(conversationID string) tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -db flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	// Verify conversation exists
	_, err := m.config.DB.GetConversationByID(context.Background(), conversationID)
	if err != nil {
		// Try by slug
		conv, err2 := m.config.DB.GetConversationBySlug(context.Background(), conversationID)
		if err2 != nil {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Conversation not found: " + conversationID),
			})
			m.updateViewportContent()
			return nil
		}
		conversationID = conv.ConversationID
	}

	// Stop current loop
	if m.loopCancel != nil {
		m.loopCancel()
	}
	if m.toolSet != nil {
		m.toolSet.Cleanup()
	}
	m.loop = nil
	m.loopCtx = nil
	m.loopCancel = nil
	m.toolSet = nil

	// Switch conversation
	m.conversationID = conversationID
	m.messages = nil
	m.totalUsage = llm.Usage{}
	m.suggestedCmds = nil

	// Load and display history from database
	history, err := m.loadHistoryFromDB()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to load history: " + err.Error()),
		})
	} else {
		// Render loaded messages
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

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Switched to conversation: " + conversationID),
	})
	m.updateViewportContent()
	return nil
}

// newConversation starts a new conversation
func (m *Model) newConversation() tea.Cmd {
	// Stop current loop
	if m.loopCancel != nil {
		m.loopCancel()
	}
	if m.toolSet != nil {
		m.toolSet.Cleanup()
	}
	m.loop = nil
	m.loopCtx = nil
	m.loopCancel = nil
	m.toolSet = nil

	// Clear state
	m.conversationID = ""
	m.messages = nil
	m.totalUsage = llm.Usage{}
	m.suggestedCmds = nil
	m.sessionName = ""

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Started new conversation"),
	})
	m.updateViewportContent()
	return nil
}

// archiveConversation archives the current conversation
func (m *Model) archiveConversation() tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -db flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	if m.conversationID == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("No active conversation to archive"),
		})
		m.updateViewportContent()
		return nil
	}

	_, err := m.config.DB.ArchiveConversation(context.Background(), m.conversationID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to archive: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	archivedID := m.conversationID

	// Start fresh
	m.newConversation()

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Archived conversation: " + archivedID),
	})
	m.updateViewportContent()
	return nil
}

// listArchivedConversations lists archived conversations from the database
func (m *Model) listArchivedConversations() tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	conversations, err := m.config.DB.ListArchivedConversations(context.Background(), 20, 0)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to list archived conversations: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if len(conversations) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No archived conversations"),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString("Archived conversations:\n")
	for _, conv := range conversations {
		name := conv.ConversationID
		if conv.Slug != nil && *conv.Slug != "" {
			name = *conv.Slug
		}
		sb.WriteString(fmt.Sprintf("  %s (%s)\n", name, conv.UpdatedAt.Format("Jan 2 15:04")))
	}
	sb.WriteString("\nUse /unarchive <id> to restore")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// unarchiveConversation restores an archived conversation
func (m *Model) unarchiveConversation(conversationID string) tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	// Try to find by ID first, then by slug
	conv, err := m.config.DB.GetConversationByID(context.Background(), conversationID)
	if err != nil {
		conv, err = m.config.DB.GetConversationBySlug(context.Background(), conversationID)
		if err != nil {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render("Conversation not found: " + conversationID),
			})
			m.updateViewportContent()
			return nil
		}
	}

	_, err = m.config.DB.UnarchiveConversation(context.Background(), conv.ConversationID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to unarchive: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	name := conv.ConversationID
	if conv.Slug != nil && *conv.Slug != "" {
		name = *conv.Slug
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Unarchived conversation: " + name),
	})
	m.updateViewportContent()
	return nil
}

// renameConversation renames the current conversation
func (m *Model) renameConversation(newSlug string) tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	if m.conversationID == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("No active conversation to rename"),
		})
		m.updateViewportContent()
		return nil
	}

	// Sanitize the slug: lowercase, alphanumeric and hyphens only
	newSlug = sanitizeSlug(newSlug)
	if newSlug == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Invalid slug (must contain alphanumeric characters)"),
		})
		m.updateViewportContent()
		return nil
	}

	_, err := m.config.DB.UpdateConversationSlug(context.Background(), m.conversationID, newSlug)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to rename: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Renamed conversation to: " + newSlug),
	})
	m.updateViewportContent()
	return nil
}

// sanitizeSlug cleans a slug: lowercase, alphanumeric and hyphens only, max 60 chars
func sanitizeSlug(input string) string {
	// Convert to lowercase
	result := strings.ToLower(input)
	// Replace spaces and underscores with hyphens
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")
	// Remove any character that's not alphanumeric or hyphen
	var sb strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result = sb.String()
	// Collapse multiple hyphens
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	// Trim leading/trailing hyphens
	result = strings.Trim(result, "-")
	// Limit length
	if len(result) > 60 {
		result = result[:60]
		result = strings.TrimSuffix(result, "-")
	}
	return result
}

// deleteConversation deletes the current conversation
// confirmDeleteConversation asks for confirmation before deleting
func (m *Model) confirmDeleteConversation() tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	if m.conversationID == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("No active conversation to delete"),
		})
		m.updateViewportContent()
		return nil
	}

	m.pendingConfirm = "delete"
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ErrorMessage.Render(fmt.Sprintf("Delete conversation %s? Type /yes to confirm or /no to cancel", m.conversationID)),
	})
	m.updateViewportContent()
	return nil
}

func (m *Model) deleteConversation() tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	if m.conversationID == "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("No active conversation to delete"),
		})
		m.updateViewportContent()
		return nil
	}

	deletedID := m.conversationID

	err := m.config.DB.DeleteConversation(context.Background(), m.conversationID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to delete: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Start fresh
	m.newConversation()

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Deleted conversation: " + deletedID),
	})
	m.updateViewportContent()
	return nil
}

// searchConversations searches conversations by content
func (m *Model) searchConversations(query string) tea.Cmd {
	if m.config.DB == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Database not configured. Use -sync flag to enable conversation sync."),
		})
		m.updateViewportContent()
		return nil
	}

	conversations, err := m.config.DB.SearchConversationsWithMessages(context.Background(), query, 20, 0)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to search: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	if len(conversations) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No conversations found matching: " + query),
		})
		m.updateViewportContent()
		return nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for \"%s\":\n", query))
	for _, conv := range conversations {
		marker := "  "
		if conv.ConversationID == m.conversationID {
			marker = "→ "
		}
		name := conv.ConversationID
		if conv.Slug != nil && *conv.Slug != "" {
			name = *conv.Slug
		}
		sb.WriteString(fmt.Sprintf("%s%s (%s)\n", marker, name, conv.UpdatedAt.Format("Jan 2 15:04")))
	}
	sb.WriteString("\nUse /switch <id> to switch conversations")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

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

// Git helper types and functions

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

// getGitRoot returns the git repository root for the given directory
func getGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// getWorkingChangesStats returns stats for working directory changes
func getWorkingChangesStats(gitRoot string) gitStats {
	cmd := exec.Command("git", "diff", "HEAD", "--numstat")
	cmd.Dir = gitRoot
	output, _ := cmd.Output()
	return parseNumstat(string(output))
}

// parseNumstat parses git diff --numstat output
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

// getRecentCommits returns recent commits with stats
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

		// Get stats for this commit
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

// getCommitFiles returns files changed in a commit
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

		// Get stats for this file
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

// getFileDiff returns the diff for a file (working changes)
func getFileDiff(gitRoot, filePath string) (string, error) {
	cmd := exec.Command("git", "diff", "HEAD", "--", filePath)
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err != nil {
		// Try without HEAD (for untracked or staged files)
		cmd = exec.Command("git", "diff", "--", filePath)
		cmd.Dir = gitRoot
		output, err = cmd.Output()
		if err != nil {
			return "", err
		}
	}
	return string(output), nil
}

// colorizeDiff applies colors to diff output
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

// formatTokens formats token counts with K/M suffixes
func formatTokens(tokens uint64) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// formatTokensInt formats int token counts with K/M suffixes (for max context which is int)
func formatTokensInt(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// attachImage loads an image file and queues it for the next message
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
func (m *Model) handleTabCompletion() tea.Cmd {
	text := m.textarea.Value()

	// For single-line input, complete on the full text
	textToCursor := text

	// If we're already cycling through completions, advance to next
	if len(m.completions) > 0 && strings.HasPrefix(text, m.completionPrefix) {
		m.completionIndex = (m.completionIndex + 1) % len(m.completions)
		m.textarea.SetValue(m.completions[m.completionIndex])
		m.textarea.CursorEnd()
		return nil
	}

	// Start new completion
	m.completionPrefix = text
	m.completions = nil
	m.completionIndex = 0

	// Check if completing a slash command
	if strings.HasPrefix(textToCursor, "/") && !strings.Contains(textToCursor, " ") {
		m.completions = m.completeSlashCommand(textToCursor)
	} else {
		// File path completion - find the word being typed
		m.completions = m.completeFilePath(textToCursor)
	}

	if len(m.completions) == 0 {
		return nil
	}

	if len(m.completions) == 1 {
		// Single match - just complete it
		m.textarea.SetValue(m.completions[0])
		m.textarea.CursorEnd()
		m.completions = nil // Clear so next tab starts fresh
	} else {
		// Multiple matches - complete common prefix and start cycling
		common := longestCommonPrefix(m.completions)
		if common != text {
			m.textarea.SetValue(common)
			m.textarea.CursorEnd()
			m.completionPrefix = common
		} else {
			// Already at common prefix, show first completion
			m.textarea.SetValue(m.completions[0])
			m.textarea.CursorEnd()
		}
	}

	return nil
}

// completeSlashCommand returns completions for slash commands
func (m *Model) completeSlashCommand(prefix string) []string {
	commands := []string{
		"/help",
		"/clear",
		"/run",
		"/stop",
		"/cancel",
		"/verbose",
		"/attach",
		"/image",
		"/attachments",
		"/theme",
		"/cwd",
		"/cd",
		"/status",
	}

	// Add DB-specific commands if database is configured
	if m.config.DB != nil {
		commands = append(commands,
			"/conversations",
			"/convos",
			"/search",
			"/switch",
			"/new",
			"/rename",
			"/archive",
			"/archived",
			"/unarchive",
			"/delete",
		)
	} else {
		commands = append(commands,
			"/save",
			"/load",
			"/sessions",
		)
	}

	// Add model commands if manager is configured
	if m.config.ModelManager != nil {
		commands = append(commands,
			"/models",
			"/model",
			"/context",
		)
	}

	// Git commands always available
	commands = append(commands,
		"/git",
	)

	var matches []string
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// completeFilePath returns completions for file paths
func (m *Model) completeFilePath(text string) []string {
	// Find the last "word" that looks like a path
	// Words are separated by spaces (but not escaped spaces)
	words := splitRespectingQuotes(text)
	if len(words) == 0 {
		return nil
	}

	lastWord := words[len(words)-1]
	prefix := strings.Join(words[:len(words)-1], " ")
	if prefix != "" {
		prefix += " "
	}

	// Expand ~ to home directory
	pathToComplete := lastWord
	homeExpanded := false
	if strings.HasPrefix(pathToComplete, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			pathToComplete = filepath.Join(home, pathToComplete[2:])
			homeExpanded = true
		}
	}

	// Make path absolute if relative
	if !filepath.IsAbs(pathToComplete) {
		pathToComplete = filepath.Join(m.config.WorkingDir, pathToComplete)
	}

	// Get directory and prefix for matching
	dir := filepath.Dir(pathToComplete)
	base := filepath.Base(pathToComplete)

	// If the path ends with /, list directory contents
	if strings.HasSuffix(lastWord, "/") || strings.HasSuffix(lastWord, string(filepath.Separator)) {
		dir = pathToComplete
		base = ""
	}

	// Check if directory exists
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden files unless explicitly looking for them
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}

		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			// Build the completed path
			var completedPath string
			if homeExpanded {
				// Restore ~/
				home, _ := os.UserHomeDir()
				relDir := strings.TrimPrefix(dir, home)
				completedPath = "~" + filepath.Join(relDir, name)
			} else if strings.HasPrefix(lastWord, "/") {
				// Absolute path
				completedPath = filepath.Join(dir, name)
			} else {
				// Relative path - reconstruct from original
				origDir := filepath.Dir(lastWord)
				if origDir == "." && !strings.HasPrefix(lastWord, "./") {
					completedPath = name
				} else {
					completedPath = filepath.Join(origDir, name)
				}
			}

			// Add trailing slash for directories
			if entry.IsDir() {
				completedPath += "/"
			}

			matches = append(matches, prefix+completedPath)
		}
	}

	return matches
}

// splitRespectingQuotes splits a string by spaces, respecting quoted sections
func splitRespectingQuotes(s string) []string {
	var result []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for i, r := range s {
		if (r == '"' || r == '\'') && (i == 0 || s[i-1] != '\\') {
			if !inQuote {
				inQuote = true
				quoteChar = r
			} else if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
		} else if r == ' ' && !inQuote {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	// If trailing space, add empty string to signal "complete in current dir"
	if len(s) > 0 && s[len(s)-1] == ' ' && !inQuote {
		result = append(result, "")
	}

	return result
}

// longestCommonPrefix finds the longest common prefix of a slice of strings
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	prefix := strs[0]
	for _, s := range strs[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// clearCompletions resets completion state (call when input changes)
func (m *Model) clearCompletions() {
	m.completions = nil
	m.completionIndex = 0
	m.completionPrefix = ""
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

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}
