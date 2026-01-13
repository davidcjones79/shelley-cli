package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"shelley.exe.dev/llm"
)

// MessageRenderer handles rendering of LLM messages to terminal output
type MessageRenderer struct {
	styles   *Styles
	renderer *glamour.TermRenderer
	width    int
}

// NewMessageRenderer creates a new message renderer
func NewMessageRenderer(width int) (*MessageRenderer, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dracula"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// Fall back to dark style if dracula not available
		renderer, err = glamour.NewTermRenderer(
			glamour.WithStylePath("dark"),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create glamour renderer: %w", err)
		}
	}

	return &MessageRenderer{
		styles:   DefaultStyles(),
		renderer: renderer,
		width:    width,
	}, nil
}

// RenderMessage renders an LLM message to a string for terminal display
func (r *MessageRenderer) RenderMessage(msg llm.Message, verbose bool) string {
	var parts []string

	for _, content := range msg.Content {
		rendered := r.renderContent(msg.Role, content, verbose)
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}

	return strings.Join(parts, "\n")
}

// renderContent renders a single content block
func (r *MessageRenderer) renderContent(role llm.MessageRole, content llm.Content, verbose bool) string {
	switch content.Type {
	case llm.ContentTypeText:
		return r.renderText(role, content.Text)
	case llm.ContentTypeToolUse:
		if !verbose {
			return ""
		}
		return r.renderToolUse(content)
	case llm.ContentTypeToolResult:
		if !verbose {
			return ""
		}
		return r.renderToolResult(content)
	case llm.ContentTypeThinking:
		if !verbose {
			return ""
		}
		return r.renderThinking(content.Text)
	default:
		return ""
	}
}

// renderText renders text content with markdown support
func (r *MessageRenderer) renderText(role llm.MessageRole, text string) string {
	if text == "" {
		return ""
	}

	// Try to render as markdown
	rendered, err := r.renderer.Render(text)
	if err != nil {
		// Fall back to plain text
		rendered = text
	}

	// Trim whitespace that glamour adds
	rendered = strings.TrimSpace(rendered)

	// Add role prefix on same line for short messages, or with colon
	prefix := ""
	switch role {
	case llm.MessageRoleUser:
		prefix = r.styles.UserMessage.Render("You: ")
	case llm.MessageRoleAssistant:
		prefix = r.styles.AssistantMessage.Render("Shelley: ")
	}

	if prefix != "" {
		return prefix + rendered
	}
	return rendered
}

// renderToolUse renders a tool use block with bordered box
func (r *MessageRenderer) renderToolUse(content llm.Content) string {
	// Header with tool name and running status
	header := r.styles.ToolName.Render(content.ToolName) +
		" " + r.styles.ToolRunning.Render("running...")

	// For simple tools, just show the header
	// For tools with complex input, show a compact summary
	var inputSummary string
	if len(content.ToolInput) > 0 {
		var input map[string]any
		if err := json.Unmarshal(content.ToolInput, &input); err == nil {
			// Show first key-value pair as summary
			for k, v := range input {
				if str, ok := v.(string); ok {
					if len(str) > 50 {
						str = str[:50] + "..."
					}
					inputSummary = r.styles.ToolInput.Render(k + ": " + str)
				}
				break
			}
		}
	}

	// Wrap in bordered box
	boxStyle := r.styles.ToolBoxStyle(r.width-4, false)
	if inputSummary != "" {
		return boxStyle.Render(header + " " + inputSummary)
	}
	return boxStyle.Render(header)
}

// renderToolResult renders a tool result block with status indicator
func (r *MessageRenderer) renderToolResult(content llm.Content) string {
	var sb strings.Builder

	// Header with status indicator
	var statusIcon string
	var headerStyle = r.styles.ToolSuccess
	if content.ToolError {
		statusIcon = "✗"
		headerStyle = r.styles.ToolError
	} else {
		statusIcon = "✓"
	}

	// Output style
	style := r.styles.ToolOutput
	if content.ToolError {
		style = r.styles.ToolError
	}

	// Render the result content
	var hasContent bool
	for _, result := range content.ToolResult {
		if result.Type == llm.ContentTypeText && result.Text != "" {
			text := strings.TrimSpace(result.Text)
			// Truncate very long outputs
			if len(text) > 1000 {
				text = text[:1000] + "... (truncated)"
			}
			if hasContent {
				sb.WriteString("\n")
			}
			sb.WriteString(style.Render(text))
			hasContent = true
		}
	}

	// Wrap in bordered box with status icon in header
	boxStyle := r.styles.ToolBoxStyle(r.width-4, content.ToolError)
	header := headerStyle.Render(statusIcon)
	if sb.Len() > 0 {
		return boxStyle.Render(header + " " + sb.String())
	}
	return boxStyle.Render(header + " Done")
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// renderThinking renders thinking content
func (r *MessageRenderer) renderThinking(text string) string {
	if text == "" {
		return ""
	}
	// Truncate long thinking
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return r.styles.Thinking.Render("(thinking: " + text + ")")
}

// RenderDivider renders a divider line
func (r *MessageRenderer) RenderDivider() string {
	return r.styles.Divider.Render(strings.Repeat("─", r.width))
}

// RenderError renders an error message
func (r *MessageRenderer) RenderError(err error) string {
	return r.styles.ErrorMessage.Render("Error: " + err.Error())
}

// RenderPrompt renders the input prompt
func (r *MessageRenderer) RenderPrompt() string {
	return r.styles.Prompt.Render("> ")
}
