package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

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

// attachImage attaches an image file to be sent with the next message
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
