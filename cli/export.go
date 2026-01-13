package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

// exportConversation exports the conversation to a markdown file
func (m *Model) exportConversation(filename string) tea.Cmd {
	// Generate default filename if not provided
	if filename == "" {
		timestamp := time.Now().Format("2006-01-02-150405")
		if m.conversationID != "" {
			filename = fmt.Sprintf("conversation-%s-%s.md", m.conversationID, timestamp)
		} else {
			filename = fmt.Sprintf("conversation-%s.md", timestamp)
		}
	}

	// Ensure .md extension
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}

	// Expand ~ to home directory
	if strings.HasPrefix(filename, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			filename = filepath.Join(home, filename[2:])
		}
	}

	// Make path absolute if relative
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(m.config.WorkingDir, filename)
	}

	// Build markdown content
	var md strings.Builder

	// Header
	md.WriteString("# Conversation Export\n\n")
	md.WriteString(fmt.Sprintf("- **Date**: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	md.WriteString(fmt.Sprintf("- **Model**: %s\n", m.config.Model))
	md.WriteString(fmt.Sprintf("- **Working Directory**: %s\n", m.config.WorkingDir))
	if m.conversationID != "" {
		md.WriteString(fmt.Sprintf("- **Conversation ID**: %s\n", m.conversationID))
	}
	md.WriteString("\n---\n\n")

	// Try to get history from loop first
	var history []llm.Message
	if m.loop != nil {
		history = m.loop.GetHistory()
	}

	if len(history) > 0 {
		// Export from raw message history
		for _, msg := range history {
			md.WriteString(formatMessageAsMarkdown(msg))
		}
	} else {
		// Fall back to rendered messages
		for _, msg := range m.messages {
			md.WriteString(formatRenderedMessageAsMarkdown(msg))
		}
	}

	// Write file
	err := os.WriteFile(filename, []byte(md.String()), 0644)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to export: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Exported to: " + filename),
	})
	m.updateViewportContent()
	return nil
}

// formatMessageAsMarkdown formats an llm.Message as markdown
func formatMessageAsMarkdown(msg llm.Message) string {
	var sb strings.Builder

	// Role header
	switch msg.Role {
	case llm.MessageRoleUser:
		sb.WriteString("## \U0001F464 User\n\n")
	case llm.MessageRoleAssistant:
		sb.WriteString("## \U0001F916 Assistant\n\n")
	default:
		sb.WriteString("## Message\n\n")
	}

	// Content
	for _, content := range msg.Content {
		switch content.Type {
		case llm.ContentTypeText:
			if content.Text != "" {
				sb.WriteString(content.Text)
				sb.WriteString("\n\n")
			}
		case llm.ContentTypeToolUse:
			sb.WriteString(fmt.Sprintf("### \U0001F527 Tool: %s\n\n", content.ToolName))
			if len(content.ToolInput) > 0 {
				// Pretty print JSON input
				var prettyInput any
				if err := json.Unmarshal(content.ToolInput, &prettyInput); err == nil {
					if formatted, err := json.MarshalIndent(prettyInput, "", "  "); err == nil {
						sb.WriteString("```json\n")
						sb.WriteString(string(formatted))
						sb.WriteString("\n```\n\n")
					}
				}
			}
		case llm.ContentTypeToolResult:
			statusIcon := "\u2713"
			if content.ToolError {
				statusIcon = "\u2717"
			}
			sb.WriteString(fmt.Sprintf("### %s Tool Result\n\n", statusIcon))
			for _, result := range content.ToolResult {
				if result.Type == llm.ContentTypeText && result.Text != "" {
					// Truncate very long outputs
					text := result.Text
					if len(text) > 2000 {
						text = text[:2000] + "\n... (truncated)"
					}
					sb.WriteString("```\n")
					sb.WriteString(text)
					sb.WriteString("\n```\n\n")
				}
				if result.MediaType != "" {
					sb.WriteString(fmt.Sprintf("*[Image: %s]*\n\n", result.MediaType))
				}
			}
		case llm.ContentTypeThinking:
			if content.Text != "" {
				sb.WriteString("<details>\n<summary>\U0001F4AD Thinking</summary>\n\n")
				sb.WriteString(content.Text)
				sb.WriteString("\n\n</details>\n\n")
			}
		}

		// Handle image content
		if content.MediaType != "" && content.Data != "" {
			sb.WriteString(fmt.Sprintf("*[Attached Image: %s]*\n\n", content.MediaType))
		}
	}

	sb.WriteString("---\n\n")
	return sb.String()
}

// formatRenderedMessageAsMarkdown formats a renderedMessage as markdown (fallback)
func formatRenderedMessageAsMarkdown(msg renderedMessage) string {
	var sb strings.Builder

	switch msg.role {
	case llm.MessageRoleUser:
		sb.WriteString("## \U0001F464 User\n\n")
	case llm.MessageRoleAssistant:
		sb.WriteString("## \U0001F916 Assistant\n\n")
	default:
		sb.WriteString("## Message\n\n")
	}

	content := stripAnsiCodes(msg.content)
	sb.WriteString(content)
	sb.WriteString("\n\n---\n\n")

	return sb.String()
}

// stripAnsiCodes removes ANSI escape codes from a string
func stripAnsiCodes(s string) string {
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRe.ReplaceAllString(s, "")
}
