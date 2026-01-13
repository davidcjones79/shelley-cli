package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"shelley.exe.dev/llm"
)

// osc8Link creates an OSC 8 hyperlink for supported terminals
// Format: \033]8;;URL\033\\TEXT\033]8;;\033\\
func osc8Link(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// filePathRe matches absolute file paths
var filePathRe = regexp.MustCompile(`(/[a-zA-Z0-9._/-]+)`)

// linkifyPaths converts file paths in text to clickable OSC 8 links
// Only works in terminals that support OSC 8 (iTerm2, Kitty, etc.)
func linkifyPaths(text string) string {
	return filePathRe.ReplaceAllStringFunc(text, func(path string) string {
		// Only linkify if the path exists
		if _, err := os.Stat(path); err == nil {
			return osc8Link("file://"+path, path)
		}
		return path
	})
}

// MessageRenderer handles rendering of LLM messages to terminal output
type MessageRenderer struct {
	styles    *Styles
	renderer  *glamour.TermRenderer
	width     int
	toolNames map[string]string // maps tool use ID to tool name
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
	// Handle image content (has MediaType but uses ContentTypeText)
	if content.MediaType != "" && content.Data != "" {
		return r.renderImage(content)
	}

	switch content.Type {
	case llm.ContentTypeText:
		return r.renderText(role, content.Text)
	case llm.ContentTypeToolUse:
		// Tool use is shown via streaming ("running..."), skip in non-verbose
		if !verbose {
			return ""
		}
		return r.renderToolUse(content)
	case llm.ContentTypeToolResult:
		// Always show tool results (summary in non-verbose, full in verbose)
		return r.renderToolResult(content, verbose)
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

	// Format with role label on its own line for better readability
	var result strings.Builder
	switch role {
	case llm.MessageRoleUser:
		result.WriteString(r.styles.UserMessage.Render("▶ "))
		result.WriteString(rendered)
	case llm.MessageRoleAssistant:
		result.WriteString(rendered)
	default:
		result.WriteString(rendered)
	}

	return result.String()
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
// In verbose mode, shows full output. In non-verbose mode, shows just status.
func (r *MessageRenderer) renderToolResult(content llm.Content, verbose bool) string {
	var sb strings.Builder

	// Header with status indicator and timing
	var statusIcon string
	var headerStyle = r.styles.ToolSuccess
	if content.ToolError {
		statusIcon = "✗"
		headerStyle = r.styles.ToolError
	} else {
		statusIcon = "✓"
	}

	// Look up tool name from ID
	toolName := ""
	if r.toolNames != nil && content.ToolUseID != "" {
		toolName = r.toolNames[content.ToolUseID]
	}

	// Add timing info if available
	var timing string
	if content.ToolUseStartTime != nil && content.ToolUseEndTime != nil {
		d := content.ToolUseEndTime.Sub(*content.ToolUseStartTime)
		timing = " " + r.styles.ToolInput.Render("("+formatDuration(d)+")")
	}

	// Output style
	style := r.styles.ToolOutput
	if content.ToolError {
		style = r.styles.ToolError
	}

	// Check for display data with screenshot
	if content.Display != nil {
		if displayMap, ok := content.Display.(map[string]any); ok {
			if displayMap["type"] == "screenshot" {
				if path, ok := displayMap["path"].(string); ok {
					// Show path (no inline images in viewport - causes layout issues)
					sb.WriteString(r.styles.SystemMessage.Render(fmt.Sprintf("🖼️  Screenshot: %s", osc8Link("file://"+path, path))))
					boxStyle := r.styles.ToolBoxStyle(r.width-4, content.ToolError)
					// Build header with tool name if available
					header := headerStyle.Render(statusIcon)
					if toolName != "" {
						header += " " + r.styles.ToolName.Render(toolName)
					}
					header += timing
					return boxStyle.Render(header + " " + sb.String())
				}
			}
		}
	}

	// In non-verbose mode, show compact status
	if !verbose {
		// Always show errors (truncated)
		if content.ToolError {
			for _, result := range content.ToolResult {
				if result.Type == llm.ContentTypeText && result.Text != "" {
					errText := strings.TrimSpace(result.Text)
					if len(errText) > 200 {
						errText = errText[:200] + "..."
					}
					sb.WriteString(style.Render(errText))
					break
				}
			}
		}
		// Build header: "✓ toolname (timing)"
		header := headerStyle.Render(statusIcon)
		if toolName != "" {
			header += " " + r.styles.ToolName.Render(toolName)
		}
		header += timing
		if sb.Len() > 0 {
			return r.styles.ToolBoxStyle(r.width-4, content.ToolError).Render(header + " " + sb.String())
		}
		// Return just the header line without a box for compactness
		return header
	}

	// Verbose mode: render the full result content
	var hasContent bool
	for _, result := range content.ToolResult {
		// Check for image content in results (show indicator, no inline rendering)
		if result.MediaType != "" && result.Data != "" {
			if hasContent {
				sb.WriteString("\n")
			}
			sb.WriteString(r.styles.SystemMessage.Render(fmt.Sprintf("🖼️  [Image: %s]", result.MediaType)))
			hasContent = true
			continue
		}

		if result.Type == llm.ContentTypeText && result.Text != "" {
			text := strings.TrimSpace(result.Text)

			if len(text) > 1000 {
				text = text[:1000] + "... (truncated)"
			}
			// Linkify file paths in the output
			text = linkifyPaths(text)
			if hasContent {
				sb.WriteString("\n")
			}
			sb.WriteString(style.Render(text))
			hasContent = true
		}
	}

	// Wrap in bordered box with status icon in header
	boxStyle := r.styles.ToolBoxStyle(r.width-4, content.ToolError)
	// Build header: "✓ toolname (timing)"
	header := headerStyle.Render(statusIcon)
	if toolName != "" {
		header += " " + r.styles.ToolName.Render(toolName)
	}
	header += timing
	if sb.Len() > 0 {
		return boxStyle.Render(header + "\n" + sb.String())
	}
	return boxStyle.Render(header)
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

// renderImage renders an image attachment indicator
// Note: We don't render inline images in the viewport because the viewport
// can't properly account for their display height, causing layout corruption.
func (r *MessageRenderer) renderImage(content llm.Content) string {
	// Calculate approximate size from base64 data
	sizeBytes := len(content.Data) * 3 / 4 // base64 is ~4/3 the size
	var sizeStr string
	if sizeBytes < 1024 {
		sizeStr = fmt.Sprintf("%dB", sizeBytes)
	} else if sizeBytes < 1024*1024 {
		sizeStr = fmt.Sprintf("%.1fKB", float64(sizeBytes)/1024)
	} else {
		sizeStr = fmt.Sprintf("%.1fMB", float64(sizeBytes)/(1024*1024))
	}

	return r.styles.SystemMessage.Render(fmt.Sprintf("🖼️  [Image: %s, %s]", content.MediaType, sizeStr))
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
