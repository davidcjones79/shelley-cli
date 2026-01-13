package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	processStatus string // Current processing status (e.g., "Sending request...", "Receiving...")
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

	// Available models cache (for number selection)
	availableModels []string

	// Last viewport content (to avoid unnecessary updates)
	lastViewportContent string

	// Mouse mode enabled (for scrolling, but prevents text selection)
	mouseEnabled bool

	// Database conversation (when DB is configured)
	conversationID string
	lastGitState   *gitstate.GitState

	// Confirmation state for destructive operations
	pendingConfirm string // e.g., "delete" when waiting for confirmation

	// Channels for async communication
	responseChan chan responseMsg
	streamChan   chan llm.StreamEvent

	// Streaming state
	streamingText   strings.Builder // accumulates streaming text
	streamingActive bool            // true if we received streaming events for current message
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

	errMsg            struct{ err error }
	streamMsg         struct {
		event llm.StreamEvent
	}
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
		streamChan:     make(chan llm.StreamEvent, 100),
		verbose:        cfg.Verbose,
		conversationID: cfg.ConversationID,
		mouseEnabled:   true,
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
		m.waitForStream(),
		tea.EnableMouseCellMotion,
	)
}

// waitForResponse returns a command that waits for responses from the loop
func (m *Model) waitForResponse() tea.Cmd {
	return func() tea.Msg {
		resp := <-m.responseChan
		return resp
	}
}

// waitForStream returns a command that waits for streaming events
func (m *Model) waitForStream() tea.Cmd {
	return func() tea.Msg {
		event := <-m.streamChan
		return streamMsg{event: event}
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

		case tea.KeyPgUp, tea.KeyCtrlU:
			// Page/half-page up in viewport
			if m.viewportReady {
				m.viewport.HalfViewUp()
			}

		case tea.KeyPgDown, tea.KeyCtrlD:
			// Page/half-page down in viewport
			if m.viewportReady {
				m.viewport.HalfViewDown()
			}
		}

	case tea.MouseMsg:
		// Handle mouse wheel scrolling explicitly for Terminal.app compatibility
		if m.viewportReady {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.viewport.LineUp(3)
			case tea.MouseButtonWheelDown:
				m.viewport.LineDown(3)
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
			// If streaming was active, the text was already displayed
			// Only render non-text content (tool uses, etc.) and update usage
			if m.streamingActive {
				m.streamingActive = false
				// Finalize any remaining streaming text
				m.finalizeStreamingText()
				// Only render tool content, not text (already shown via streaming)
				for _, content := range msg.message.Content {
					if content.Type == llm.ContentTypeToolUse || content.Type == llm.ContentTypeToolResult {
						rendered := m.renderer.renderContent(msg.message.Role, content, m.verbose)
						if rendered != "" {
							m.messages = append(m.messages, renderedMessage{
								role:    msg.message.Role,
								content: rendered,
							})
						}
					}
				}
			} else {
				// Non-streaming path: render the full message
				rendered := m.renderer.RenderMessage(msg.message, m.verbose)
				if rendered != "" {
					m.messages = append(m.messages, renderedMessage{
						role:    msg.message.Role,
						content: rendered,
					})
				}
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
			// Process any pending messages
			if len(m.pendingMessages) > 0 {
				nextMsg := m.pendingMessages[0]
				m.pendingMessages = m.pendingMessages[1:]
				m.textarea.SetValue(nextMsg)
				return m, m.sendMessage()
			}
		}

		// Continue waiting for responses
		cmds = append(cmds, m.waitForResponse())

	case streamMsg:
		switch msg.event.Type {
		case llm.StreamEventRequestStart:
			// Request is being sent to the API
			m.processStatus = "Sending request..."

		case llm.StreamEventTextDelta:
			// Accumulate text and update display
			m.streamingActive = true
			m.processStatus = "Receiving..."
			m.streamingText.WriteString(msg.event.Text)
			m.updateStreamingDisplay()

		case llm.StreamEventThinkingDelta:
			// Could show thinking indicator if verbose
			if m.verbose {
				// For now, just accumulate - could add a thinking indicator
			}

		case llm.StreamEventToolUseStart:
			// Finalize any streaming text first
			m.finalizeStreamingText()
			// Update status to show which tool is running
			m.processStatus = fmt.Sprintf("Running %s...", msg.event.ToolName)
			// Always show tool starting indicator for progress feedback
			toolMsg := m.styles.ToolName.Render(msg.event.ToolName) + " " + m.styles.ToolRunning.Render("running...")
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ToolBoxStyle(m.width-4, false).Render(toolMsg),
			})
			m.updateViewportContent()

		case llm.StreamEventContentBlockStop:
			// Content block finished - finalize streaming text if any
			m.finalizeStreamingText()

		case llm.StreamEventMessageComplete:
			// Message complete - make sure streaming text is finalized
			m.finalizeStreamingText()
			// Usage will be updated via responseMsg
		}
		// Continue waiting for stream events
		cmds = append(cmds, m.waitForStream())

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
	m.processStatus = "Preparing request..."
	m.err = nil
	m.streamingActive = false
	m.streamingText.Reset()

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

	// Create conversation in database on first message
	if m.config.DB != nil && m.conversationID == "" {
		if err := m.createConversation(context.Background()); err != nil {
			m.config.Logger.Error("Failed to create conversation", "error", err)
		}
	}

	// Initialize loop if needed
	if m.loop == nil {
		if err := m.initLoop(); err != nil {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.renderer.RenderError(err),
			})
			m.updateViewportContent()
			m.processing = false
			return nil
		}
	}

	// Queue the message
	m.loop.QueueUserMessage(userMsg)

	// Process the turn in a goroutine so streaming can update the UI
	go func() {
		if err := m.loop.ProcessOneTurn(m.loopCtx); err != nil {
			m.responseChan <- responseMsg{err: err}
		}
		// Signal that processing is done
		m.responseChan <- responseMsg{message: llm.Message{EndOfTurn: true}}
	}()

	return nil
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
		OnStream: func(event llm.StreamEvent) {
			// Send stream events to the CLI's stream channel
			select {
			case m.streamChan <- event:
			default:
				// Channel full, skip (shouldn't happen with large buffer)
			}
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

	// Add streaming text if any
	if m.streamingText.Len() > 0 {
		if len(m.messages) > 0 {
			content.WriteString("\n")
		}
		content.WriteString(m.streamingText.String())
		content.WriteString("\n")
	}

	newContent := content.String()
	
	// Only update and scroll if content actually changed
	if newContent != m.lastViewportContent {
		// Check if user was at bottom before update
		wasAtBottom := m.viewport.AtBottom()
		
		m.lastViewportContent = newContent
		m.viewport.SetContent(newContent)
		
		// Only auto-scroll if user was already at bottom
		if wasAtBottom {
			m.viewport.GotoBottom()
		}
	}
}

// updateStreamingDisplay updates the viewport with the current streaming text
func (m *Model) updateStreamingDisplay() {
	m.updateViewportContent()
}

// finalizeStreamingText moves accumulated streaming text to a permanent message
func (m *Model) finalizeStreamingText() {
	if m.streamingText.Len() == 0 {
		return
	}

	text := m.streamingText.String()
	m.streamingText.Reset()

	// Render the text through the markdown renderer
	rendered, err := m.renderer.renderer.Render(text)
	if err != nil {
		rendered = text
	}
	rendered = strings.TrimSpace(rendered)

	if rendered != "" {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: rendered,
		})
	}
	m.updateViewportContent()
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
	title := m.styles.HeaderTitle.Render("Shelley CLI (unofficial)")
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
		status := m.processStatus
		if status == "" {
			status = "Agent working..."
		}
		statusLine := m.spinner.View() + " " + m.styles.Thinking.Render(status)
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

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
