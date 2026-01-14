package cli

// TODO: Refactor Update() (~380 lines) - extract message handlers:
//   - handleKeyMsg()
//   - handleStreamMsg()
//   - handleResponseMsg()
//
// TODO: Consolidate tool rendering duplication between streaming and non-streaming paths

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	"shelley.exe.dev/slug"
)

// frankensteinStatus returns a random Frankenstein-themed status message
// Mary Shelley wrote Frankenstein, so we honor the namesake
var frankensteinStatuses = []string{
	// Lab/Creation themed
	"Harnessing lightning...",
	"Stitching together...",
	"Animating the creature...",
	"Charging the apparatus...",
	"Galvanizing...",
	"Assembling parts...",
	"Reanimating...",
	"Connecting the electrodes...",
	"Calibrating the voltaic pile...",
	"Preparing the galvanic bath...",
	"Raising the platform...",
	"Opening the skylight...",
	"The machinery hums...",
	"Bubbling beakers...",
	"Adjusting the dials...",
	"Checking the sutures...",
	"Reading the instruments...",
	// Gothic/Atmospheric
	"Brooding in the laboratory...",
	"Consulting ancient texts...",
	"By candlelight...",
	"The creature stirs...",
	"Awaiting the storm...",
	"In the shadows...",
	"A dark and stormy night...",
	"The wind howls outside...",
	"Thunder rumbles...",
	"In Castle Frankenstein...",
	"Midnight approaches...",
	"Cobwebs tremble...",
	"Candles flicker...",
	"Something stirs below...",
	"The tower shakes...",
	"Rain lashes the windows...",
	"Fog rolls in...",
	"The clock strikes twelve...",
	// Literary references (Mary Shelley's novel)
	"Prometheus stirs...",
	"The Modern Prometheus awakens...",
	"From the workshop of filthy creation...",
	"Pursuing nature to her hiding places...",
	"Infusing a spark of being...",
	"The secrets of heaven and earth...",
	"A new species would bless me...",
	"The beauty of the dream vanished...",
	"I beheld the wretch...",
	// Classic film references
	"Igor, the switches!",
	"Throw the switch!",
	"It's alive... almost...",
	"The monster awakens...",
	"Lightning crackles...",
	"Sparks fly...",
	"More power!",
	"The body twitches...",
	"Vital signs detected...",
	"The hand moves...",
	"Eyes flicker open...",
	"Give my creature life!",
	"The moment of truth...",
	"Destiny awaits...",
	"History in the making...",
	"Science prevails...",
	"The experiment continues...",
	"Behold!",
	"Stand back!",
	"Now we wait...",
	"Patience, Igor...",
	"Almost there...",
	"The stars align...",
	"Fate intervenes...",
	"Genius at work...",
}

func randomFrankensteinStatus() string {
	return frankensteinStatuses[rand.Intn(len(frankensteinStatuses))]
}

// getWorkingStatus returns a status message, either Frankenstein-themed or standard
func (m *Model) getWorkingStatus() string {
	if m.frankenstein {
		return randomFrankensteinStatus()
	}
	return "Working..."
}

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
	Frankenstein  bool // Enable Frankenstein-themed status messages (easter egg)

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
	textarea       textarea.Model
	textareaHeight int // current textarea height (for auto-resize)
	spinner        spinner.Model
	viewport       viewport.Model
	renderer       *MessageRenderer
	styles         *Styles
	messages       []renderedMessage
	width, height  int
	ready         bool
	viewportReady bool
	// State machine for async message processing:
	//
	//   IDLE (processing=false)
	//     |
	//     | User submits message (Enter key)
	//     v
	//   PROCESSING (processing=true, streamingActive=false)
	//     |
	//     | First streamMsg received
	//     v
	//   STREAMING (processing=true, streamingActive=true)
	//     |                                      |
	//     | streamMsg events continue            | Tool use detected (ContentStart with tool)
	//     | (text accumulates in streamingText)  v
	//     |                                    TOOL_RUNNING (currentToolName set)
	//     |                                      |
	//     | <------ Tool completes (ContentStop) |
	//     |                                      |
	//     | responseMsg{EndOfTurn: true}
	//     v
	//   IDLE (processing=false)
	//
	// Cancellation: Escape or Ctrl+C calls loopCancel(), sets processing=false
	// Message queueing: User can type while processing; messages go to pendingMessages
	//
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

	// Recent conversations cache (for numbered selection)
	recentConversations []string

	// Verbosity - show tool details
	verbose bool

	// Session management (legacy JSON files)
	sessionName string

	// Available models cache (for number selection)
	availableModels []string

	// Last viewport content (to avoid unnecessary updates)
	lastViewportContent string

	// User manually scrolled up - don't auto-scroll until they return to bottom
	userScrolledUp bool

	// Mouse mode enabled (for scrolling, but prevents text selection)
	mouseEnabled bool

	// Frankenstein easter egg - themed status messages
	frankenstein bool

	// Consent screen - shown on first launch
	showConsent   bool
	consentCursor int // 0 = Yes, 1 = No

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

	// Tool streaming state
	currentToolName  string            // name of currently running tool
	currentToolID    string            // ID of currently running tool
	currentToolInput strings.Builder   // accumulates tool input JSON as it streams
	toolNames        map[string]string          // maps tool use ID to tool name
	toolInputs       map[string]json.RawMessage // maps tool use ID to tool input
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

	// Create textarea for input (starts at 2 lines, auto-expands)
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Enter to send, Ctrl+J for newline)"
	ta.Focus()
	ta.SetHeight(2)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	ta.MaxHeight = 10 // Cap expansion at 10 lines

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
	// Disable viewport's built-in key bindings - we handle scrolling manually
	// This prevents arrow keys from scrolling (we use them for command history)
	vp.KeyMap = viewport.KeyMap{}

	// Show consent screen for new conversations (not when resuming)
	needsConsent := cfg.ConversationID == ""

	m := &Model{
		config:         cfg,
		textarea:       ta,
		textareaHeight: 2,
		spinner:        sp,
		viewport:       vp,
		renderer:       renderer,
		styles:         DefaultStyles(),
		messages:       []renderedMessage{},
		promptHistory:  []string{},
		historyIndex:   -1,
		responseChan:   make(chan responseMsg, 10),
		streamChan:     make(chan llm.StreamEvent, 1000),
		verbose:        cfg.Verbose,
		conversationID: cfg.ConversationID,
		mouseEnabled:   true,
		frankenstein:   true, // Themed status messages enabled by default
		showConsent:    needsConsent,
		toolNames:      make(map[string]string),
		toolInputs:     make(map[string]json.RawMessage),
	}

	// If resuming a conversation, load and display history
	if cfg.DB != nil && cfg.ConversationID != "" {
		history, err := m.loadHistoryFromDB()
		if err != nil {
			cfg.Logger.Warn("Failed to load conversation history", "error", err)
		} else {
			// First pass: extract all tool names and inputs from tool_use content
			for _, msg := range history {
				for _, content := range msg.Content {
					if content.Type == llm.ContentTypeToolUse && content.ID != "" && content.ToolName != "" {
						m.toolNames[content.ID] = content.ToolName
						if len(content.ToolInput) > 0 {
							m.toolInputs[content.ID] = content.ToolInput
						}
					}
				}
			}
			m.renderer.toolNames = m.toolNames
			m.renderer.toolInputs = m.toolInputs
			// Second pass: render messages
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
		// Handle consent screen input first
		if m.showConsent {
			return m.handleConsentInput(msg)
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			if m.loopCancel != nil {
				m.loopCancel()
			}
			return m, tea.Quit

		case tea.KeyEscape:
			// Cancel current operation or clear input
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
			} else {
				// Clear input box
				m.textarea.Reset()
			}

		case tea.KeyCtrlJ:
			// Ctrl+J inserts a newline (works reliably on Mac)
			m.textarea.InsertString("\n")
			return m, nil

		case tea.KeyEnter:
			// Alt+Enter also inserts newline (works in some terminals)
			if msg.Alt {
				m.textarea.InsertString("\n")
				return m, nil
			}
			// Send message on Enter
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" {
				if m.processing {
					// Queue message for later processing
					m.pendingMessages = append(m.pendingMessages, text)
					m.textarea.Reset()
					m.textareaHeight = 2
					m.textarea.SetHeight(2)
					m.recalculateViewportHeight()
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
				m.userScrolledUp = true
			}

		case tea.KeyPgDown, tea.KeyCtrlD:
			// Page/half-page down in viewport
			if m.viewportReady {
				m.viewport.HalfViewDown()
				if m.viewport.AtBottom() {
					m.userScrolledUp = false
				}
			}
		}

	case tea.MouseMsg:
		// Handle mouse wheel scrolling explicitly for Terminal.app compatibility
		if m.viewportReady {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.viewport.LineUp(3)
				m.userScrolledUp = true
			case tea.MouseButtonWheelDown:
				m.viewport.LineDown(3)
				if m.viewport.AtBottom() {
					m.userScrolledUp = false
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// Update textarea width
		m.textarea.SetWidth(msg.Width - 4)

		// Recalculate viewport height based on current textarea height
		m.recalculateViewportHeight()

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
				// Extract tool names and inputs from tool_use content and share with renderer
				for _, content := range msg.message.Content {
					if content.Type == llm.ContentTypeToolUse && content.ID != "" && content.ToolName != "" {
						m.toolNames[content.ID] = content.ToolName
						if len(content.ToolInput) > 0 {
							m.toolInputs[content.ID] = content.ToolInput
						}
					}
				}
				m.renderer.toolNames = m.toolNames
				m.renderer.toolInputs = m.toolInputs
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
				// Extract tool names and inputs from tool_use content for later lookup
				for _, content := range msg.message.Content {
					if content.Type == llm.ContentTypeToolUse && content.ID != "" && content.ToolName != "" {
						m.toolNames[content.ID] = content.ToolName
						if len(content.ToolInput) > 0 {
							m.toolInputs[content.ID] = content.ToolInput
						}
					}
				}
				m.renderer.toolNames = m.toolNames
				m.renderer.toolInputs = m.toolInputs
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
			m.processStatus = m.getWorkingStatus()

		case llm.StreamEventTextDelta:
			// Accumulate text and update display
			m.streamingActive = true
			if m.frankenstein {
				m.processStatus = "It's alive!"
			} else {
				m.processStatus = "Receiving..."
			}
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
			// Track current tool for input streaming
			m.currentToolName = msg.event.ToolName
			m.currentToolID = msg.event.ToolUseID
			m.currentToolInput.Reset()
			// Store tool name by ID for later lookup in results
			if msg.event.ToolUseID != "" {
				m.toolNames[msg.event.ToolUseID] = msg.event.ToolName
			}
			// Update status to show which tool is running
			if m.frankenstein {
				m.processStatus = fmt.Sprintf("The creature runs %s...", msg.event.ToolName)
			} else {
				m.processStatus = fmt.Sprintf("Running %s...", msg.event.ToolName)
			}
			// Show tool starting indicator (verbose: boxed, non-verbose: inline)
			toolMsg := m.styles.ToolName.Render(msg.event.ToolName) + " " + m.styles.ToolRunning.Render("running...")
			var content string
			if m.verbose {
				content = m.styles.ToolBoxStyle(m.width-4, false).Render(toolMsg)
			} else {
				content = toolMsg
			}
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: content,
			})
			m.updateViewportContent()

		case llm.StreamEventToolInputDelta:
			// Accumulate tool input as it streams
			m.currentToolInput.WriteString(msg.event.ToolInput)
			// Update the last message to show progress
			if len(m.messages) > 0 && m.currentToolName != "" {
				toolMsg := m.styles.ToolName.Render(m.currentToolName) + " " + m.styles.ToolRunning.Render("running...")
				// Show a compact summary of the input so far
				inputSummary := m.formatToolInputSummary(m.currentToolInput.String())
				if inputSummary != "" {
					toolMsg += " " + m.styles.ToolInput.Render(inputSummary)
				}
				if m.verbose {
					m.messages[len(m.messages)-1].content = m.styles.ToolBoxStyle(m.width-4, false).Render(toolMsg)
				} else {
					m.messages[len(m.messages)-1].content = toolMsg
				}
				m.updateViewportContent()
			}

		case llm.StreamEventContentBlockStop:
			// Content block finished - finalize streaming text if any
			m.finalizeStreamingText()
			// Save tool input before clearing (for later display in tool results)
			if m.currentToolID != "" && m.currentToolInput.Len() > 0 {
				m.toolInputs[m.currentToolID] = json.RawMessage(m.currentToolInput.String())
				m.renderer.toolInputs = m.toolInputs
			}
			// Clear tool state
			m.currentToolName = ""
			m.currentToolID = ""
			m.currentToolInput.Reset()

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

	case describeImageResultMsg:
		if msg.err != nil {
			m.showError("Image analysis failed: " + msg.err.Error())
		} else {
			// Render the response as markdown
			responseMsg := llm.Message{
				Role:    llm.MessageRoleAssistant,
				Content: []llm.Content{{Type: llm.ContentTypeText, Text: msg.text}},
			}
			rendered := m.renderer.RenderMessage(responseMsg, false)
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: rendered,
			})
			m.updateViewportContent()
		}
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

	// Auto-resize textarea based on content
	m.updateTextareaHeight()

	// Update placeholder based on state
	if m.processing {
		m.textarea.Placeholder = "Type next message (will be queued)..."
	} else {
		m.textarea.Placeholder = "Type your message... (Enter to send, Ctrl+J for newline)"
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
	m.textareaHeight = 2
	m.textarea.SetHeight(2)
	m.recalculateViewportHeight()
	m.processing = true
	m.processStatus = m.getWorkingStatus()
	m.err = nil
	m.streamingActive = false
	m.streamingText.Reset()
	m.currentToolName = ""
	m.currentToolInput.Reset()
	m.userScrolledUp = false // Reset scroll state on new message

	// Clear suggested commands for new conversation turn
	m.suggestedCmds = nil

	// Auto-extract image paths from text (e.g., from drag-drop which pastes file path)
	cleanedText, imagePaths := extractImagePathsFromText(text, m.config.WorkingDir)
	maxImageDim := m.config.LLMService.MaxImageDimension()
	for _, imgPath := range imagePaths {
		if att, err := loadImageAsAttachment(imgPath, maxImageDim); err == nil {
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
	isFirstMessage := m.config.DB != nil && m.conversationID == ""
	if isFirstMessage {
		if err := m.createConversation(context.Background()); err != nil {
			m.config.Logger.Error("Failed to create conversation", "error", err)
			isFirstMessage = false
		}
	}

	// Generate slug for new conversation (in background)
	if isFirstMessage && m.config.ModelManager != nil {
		convID := m.conversationID
		modelID := m.config.Model
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := slug.GenerateSlug(ctx, m.config.ModelManager, m.config.DB, m.config.Logger, convID, text, modelID); err != nil {
				m.config.Logger.Debug("Failed to generate slug", "error", err)
			}
		}()
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
	// If we have database sync, persist CWD changes
	if m.config.DB != nil && m.conversationID != "" {
		convID := m.conversationID
		toolSetConfig.OnWorkingDirChange = func(newDir string) {
			if err := m.config.DB.UpdateConversationCwd(context.Background(), convID, newDir); err != nil {
				m.config.Logger.Debug("Failed to persist CWD change", "error", err)
			}
		}
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
			m.config.Logger.Debug("stream event received", "type", event.Type.String(), "tool", event.ToolName)
			select {
			case m.streamChan <- event:
			default:
				// Channel full, skip (shouldn't happen with large buffer)
				m.config.Logger.Warn("stream channel full, dropping event", "type", event.Type.String())
			}
		},
	})

	return nil
}

// updateTextareaHeight adjusts textarea height based on content (1-10 lines)
func (m *Model) updateTextareaHeight() {
	text := m.textarea.Value()
	
	// Calculate visual lines needed
	lines := 0
	if text == "" {
		lines = 1
	} else {
		// Get textarea width (account for padding)
		textareaWidth := m.textarea.Width()
		if textareaWidth < 10 {
			textareaWidth = 80 // fallback
		}
		
		// Count visual lines: each hard line may wrap into multiple visual lines
		for _, line := range strings.Split(text, "\n") {
			if len(line) == 0 {
				lines++
			} else {
				// Calculate how many visual lines this line takes
				lines += (len(line) + textareaWidth - 1) / textareaWidth
			}
		}
	}
	
	// Clamp to 1-10 lines
	if lines < 1 {
		lines = 1
	}
	if lines > 10 {
		lines = 10
	}
	
	// Only update if height changed
	if lines != m.textareaHeight {
		m.textareaHeight = lines
		m.textarea.SetHeight(lines)
		m.recalculateViewportHeight()
	}
}

// recalculateViewportHeight adjusts viewport to account for textarea size
func (m *Model) recalculateViewportHeight() {
	// Calculate heights for each section:
	// Header: 1 line
	// Footer: 3 lines (status bar + divider + prompt) + textarea height
	headerHeight := 1
	footerHeight := 3 + m.textareaHeight

	viewportHeight := m.height - headerHeight - footerHeight
	if viewportHeight < 3 {
		viewportHeight = 3
	}

	m.viewport.Width = m.width
	m.viewport.Height = viewportHeight
}

// updateViewportContent rebuilds the viewport content from messages
// Use scrollToBottom=true to force scroll after loading new content
func (m *Model) updateViewportContentAndScroll(scrollToBottom bool) {
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

	// Only update if content actually changed
	if newContent != m.lastViewportContent {
		m.lastViewportContent = newContent
		m.viewport.SetContent(newContent)

		// Scroll to bottom if explicitly requested, or if user hasn't scrolled up
		if scrollToBottom {
			m.userScrolledUp = false
			m.viewport.GotoBottom()
		} else if !m.userScrolledUp {
			m.viewport.GotoBottom()
		}
	}
}

func (m *Model) updateViewportContent() {
	var content strings.Builder

	// Show welcome message if no messages yet
	showingWelcome := len(m.messages) == 0 && m.streamingText.Len() == 0
	if showingWelcome {
		content.WriteString(m.renderWelcome())
	}

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
		// Detect transition from welcome message to real content
		wasShowingWelcome := strings.Contains(m.lastViewportContent, "Welcome to Shelley CLI")
		transitionFromWelcome := wasShowingWelcome && !showingWelcome
		
		m.lastViewportContent = newContent
		m.viewport.SetContent(newContent)
		
		// Auto-scroll unless user explicitly scrolled up
		// Reset userScrolledUp on transition from welcome or when user returns to bottom
		if transitionFromWelcome {
			m.userScrolledUp = false
		}
		if !m.userScrolledUp {
			m.viewport.GotoBottom()
		}
	}
}

// updateStreamingDisplay updates the viewport with the current streaming text
func (m *Model) updateStreamingDisplay() {
	m.updateViewportContent()
}

// formatToolInputSummary returns a compact summary of tool input JSON
// It prioritizes showing the most relevant field based on the tool type
func (m *Model) formatToolInputSummary(inputJSON string) string {
	if inputJSON == "" {
		return ""
	}
	// Try to parse as JSON object
	var input map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		// Partial JSON - just show length indicator
		if len(inputJSON) > 20 {
			return fmt.Sprintf("(%d chars)", len(inputJSON))
		}
		return ""
	}

	// Priority order for different tools
	// bash: show command
	// patch: show path
	// keyword_search: show query
	// change_dir: show path
	priorityKeys := []string{"command", "path", "query", "search_terms", "thoughts"}

	for _, key := range priorityKeys {
		if v, ok := input[key]; ok {
			return m.formatInputValue(key, v)
		}
	}

	// Fall back to first string value
	for k, v := range input {
		if str, ok := v.(string); ok {
			return m.formatInputValue(k, str)
		}
	}
	return ""
}

// formatInputValue formats a single input key-value for display
func (m *Model) formatInputValue(key string, value any) string {
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []any:
		// For arrays (like search_terms), join first few items
		var parts []string
		for i, item := range v {
			if i >= 3 {
				parts = append(parts, "...")
				break
			}
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		str = strings.Join(parts, ", ")
	default:
		return ""
	}

	// For command, show more context (first line, up to 60 chars)
	maxLen := 40
	if key == "command" {
		maxLen = 60
	}

	// Truncate on newline
	if idx := strings.Index(str, "\n"); idx > 0 {
		str = str[:idx]
		if len(str) > maxLen {
			str = str[:maxLen] + "..."
		} else {
			str = str + " ..."
		}
	} else if len(str) > maxLen {
		str = str[:maxLen] + "..."
	}

	// For command, don't show the key name (it's obvious)
	if key == "command" {
		return str
	}
	return key + ": " + str
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

// renderConsentScreen shows the initial consent/warning screen
// Frankenstein accent color - bright lime for decorative elements
const frankensteinAccent = lipgloss.Color("#919831")

func (m *Model) renderConsentScreen() string {
	var sb strings.Builder

	// Accent style for decorative elements only
	accentStyle := lipgloss.NewStyle().Foreground(frankensteinAccent)

	// Build the content first, then wrap in a border
	var content strings.Builder

	// Title - uses accent color
	title := accentStyle.Bold(true).Render("Shelley wants to work here")
	content.WriteString(title)
	content.WriteString("\n\n")

	// Working directory - bold, default terminal color
	wd := lipgloss.NewStyle().Bold(true).Render(m.config.WorkingDir)
	content.WriteString(wd)
	content.WriteString("\n\n")

	// Permission explanation - default color
	content.WriteString("To help you, Shelley needs permission to:")
	content.WriteString("\n\n")

	// Capabilities list - default color, accent bullets
	bullets := []string{
		"Read files in this directory and subdirectories",
		"Create, modify, or delete files",
		"Execute shell commands (git, npm, make, etc.)",
	}
	if m.config.EnableBrowser {
		bullets = append(bullets, "Control a browser and take screenshots")
	}

	bulletChar := accentStyle.Render("•")
	for _, bullet := range bullets {
		content.WriteString(fmt.Sprintf("%s %s\n", bulletChar, bullet))
	}
	content.WriteString("\n")

	// Selection options
	arrow := accentStyle.Render("❯")
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	if m.consentCursor == 0 {
		content.WriteString(fmt.Sprintf("%s 1. Yes, let's go\n", arrow))
		content.WriteString(dimStyle.Render("  2. No, exit") + "\n")
	} else {
		content.WriteString(dimStyle.Render("  1. Yes, let's go") + "\n")
		content.WriteString(fmt.Sprintf("%s 2. No, exit\n", arrow))
	}
	content.WriteString("\n")

	// Unofficial fork notice - dim, italic
	content.WriteString(dimStyle.Italic(true).Render("This is an unofficial fork with a CLI interface."))
	content.WriteString("\n\n")

	// Help text - dim
	help := dimStyle.Render("↑/↓ to select · Enter to confirm · Esc to cancel")
	content.WriteString(help)

	// Wrap content in a border box with accent color
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(frankensteinAccent).
		Padding(1, 3)

	box := boxStyle.Render(content.String())

	// Center the box vertically and horizontally
	boxHeight := lipgloss.Height(box)
	boxWidth := lipgloss.Width(box)

	verticalPadding := (m.height - boxHeight) / 2
	if verticalPadding < 0 {
		verticalPadding = 0
	}

	horizontalPadding := (m.width - boxWidth) / 2
	if horizontalPadding < 0 {
		horizontalPadding = 0
	}

	for i := 0; i < verticalPadding; i++ {
		sb.WriteString("\n")
	}

	// Add horizontal padding to each line of the box
	padding := strings.Repeat(" ", horizontalPadding)
	for _, line := range strings.Split(box, "\n") {
		sb.WriteString(padding + line + "\n")
	}

	return sb.String()
}

// handleConsentInput processes key events on the consent screen
func (m *Model) handleConsentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEscape:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyUp, tea.KeyShiftTab:
		if m.consentCursor > 0 {
			m.consentCursor--
		}
		return m, nil

	case tea.KeyDown, tea.KeyTab:
		if m.consentCursor < 1 {
			m.consentCursor++
		}
		return m, nil

	case tea.KeyEnter:
		if m.consentCursor == 0 {
			// User accepted
			m.showConsent = false
			return m, nil
		}
		// User declined
		m.quitting = true
		return m, tea.Quit

	case tea.KeyRunes:
		// Handle number keys
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case '1', 'y', 'Y':
				m.showConsent = false
				return m, nil
			case '2', 'n', 'N':
				m.quitting = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// renderWelcome creates the welcome message shown on startup
func (m *Model) renderWelcome() string {
	var sb strings.Builder

	// Add spacing at top
	sb.WriteString("\n")

	// Use styles for consistent look
	title := m.styles.HeaderTitle.Render("Ready to bring your code to life?")
	sb.WriteString(title)
	sb.WriteString("\n\n")

	hint := m.styles.SystemMessage.Render
	cmd := m.styles.ToolName.Render

	// Show working directory prominently
	sb.WriteString(m.styles.WorkingDir.Render(m.config.WorkingDir))
	sb.WriteString("\n\n")

	sb.WriteString(hint("I can help you by:"))
	sb.WriteString("\n")
	sb.WriteString("  • Reading and understanding your codebase\n")
	sb.WriteString("  • Creating, editing, and organizing files\n")
	sb.WriteString("  • Running shell commands (git, tests, builds, etc.)\n")
	if m.config.EnableBrowser {
		sb.WriteString("  • Taking screenshots and browsing the web\n")
	}
	sb.WriteString("\n")

	sb.WriteString(hint("Getting started:"))
	sb.WriteString("\n")
	sb.WriteString("  • Type a message and press Enter to chat\n")
	sb.WriteString("  • Use " + cmd("/help") + " to see all commands\n")
	if m.config.DB != nil {
		sb.WriteString("  • Use " + cmd("/switch") + " to resume a previous conversation\n")
	}
	sb.WriteString("\n")

	sb.WriteString(hint("Tip:") + " " + cmd("/frankenstein") + " toggles themed status messages\n")

	return sb.String()
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

	// Show consent screen if needed
	if m.showConsent {
		return m.renderConsentScreen()
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
			status = m.getWorkingStatus()
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
// It also calculates total token usage from the conversation
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
	var totalUsage llm.Usage
	for _, msg := range messages {
		// Sum up usage from all messages
		if msg.UsageData != nil {
			var usage llm.Usage
			if err := json.Unmarshal([]byte(*msg.UsageData), &usage); err == nil {
				totalUsage.Add(usage)
			}
		}
		
		// Skip system and gitinfo messages for history
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
	
	// Update the model's total usage with historical data
	m.totalUsage = totalUsage
	
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

	// Check file exists (for better error messages)
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

	// Load and optionally resize the image
	maxImageDim := m.config.LLMService.MaxImageDimension()
	att, err := loadImageAsAttachment(path, maxImageDim)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to load image: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Add to pending attachments
	m.pendingAttachments = append(m.pendingAttachments, *att)

	// Show confirmation
	filename := filepath.Base(path)
	// Calculate size after potential resizing
	dataLen := len(att.data) * 3 / 4 // approximate decoded size from base64
	sizeKB := float64(dataLen) / 1024
	msg := fmt.Sprintf("🖼️  Attached: %s (%.1fKB, %s)", filename, sizeKB, att.mediaType)
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
// showSystemMessage displays a system message and updates the viewport
func (m *Model) showSystemMessage(text string) {
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(text),
	})
	m.updateViewportContent()
}

// showError displays an error message and updates the viewport
func (m *Model) showError(text string) {
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ErrorMessage.Render(text),
	})
	m.updateViewportContent()
}

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
