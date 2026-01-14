package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

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

	// Cache conversation IDs for numbered selection
	m.recentConversations = nil
	for _, conv := range conversations {
		m.recentConversations = append(m.recentConversations, conv.ConversationID)
	}

	var sb strings.Builder
	sb.WriteString("Recent conversations:\n")
	for i, conv := range conversations {
		marker := "  "
		if conv.ConversationID == m.conversationID {
			marker = "→ "
		}
		name := conv.ConversationID
		if conv.Slug != nil && *conv.Slug != "" {
			name = *conv.Slug
		}
		line := fmt.Sprintf("%s%2d. %s (%s)", marker, i+1, name, conv.UpdatedAt.Format("Jan 2 15:04"))
		if conv.Cwd != nil && *conv.Cwd != "" {
			cwd := *conv.Cwd
			if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(cwd, home) {
				cwd = "~" + cwd[len(home):]
			}
			line += fmt.Sprintf(" [%s]", cwd)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /switch <number> or /switch <id> to switch")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// switchConversation switches to a different conversation
// conversationID can be:
// - empty string: switch to most recent conversation
// - a number (1-20): select from recent conversations list
// - a conversation ID or slug
func (m *Model) switchConversation(conversationID string) tea.Cmd {
	if m.config.DB == nil {
		m.showError("Database not configured. Use -db flag to enable conversation sync.")
		return nil
	}

	// Handle empty arg: switch to most recent
	if conversationID == "" {
		conversations, err := m.config.DB.ListConversations(context.Background(), 1, 0)
		if err != nil || len(conversations) == 0 {
			m.showError("No conversations found")
			return nil
		}
		// Skip if already on the most recent
		if conversations[0].ConversationID == m.conversationID {
			m.showSystemMessage("Already on most recent conversation")
			return nil
		}
		conversationID = conversations[0].ConversationID
	}

	// Handle numeric selection from /conversations list
	if num, err := strconv.Atoi(conversationID); err == nil && num >= 1 && num <= 20 {
		if len(m.recentConversations) == 0 {
			// No cached list, fetch it
			conversations, err := m.config.DB.ListConversations(context.Background(), 20, 0)
			if err != nil {
				m.showError("Failed to list conversations: " + err.Error())
				return nil
			}
			m.recentConversations = nil
			for _, conv := range conversations {
				m.recentConversations = append(m.recentConversations, conv.ConversationID)
			}
		}
		if num > len(m.recentConversations) {
			m.showError(fmt.Sprintf("Invalid selection: %d (only %d conversations)", num, len(m.recentConversations)))
			return nil
		}
		conversationID = m.recentConversations[num-1]
	}

	// Look up by ID or slug
	_, err := m.config.DB.GetConversationByID(context.Background(), conversationID)
	if err != nil {
		conv, err2 := m.config.DB.GetConversationBySlug(context.Background(), conversationID)
		if err2 != nil {
			m.showError("Conversation not found: " + conversationID)
			return nil
		}
		conversationID = conv.ConversationID
	}

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

	m.conversationID = conversationID
	m.messages = nil
	m.totalUsage = llm.Usage{}
	m.suggestedCmds = nil
	m.toolNames = make(map[string]string) // Reset tool names for new conversation

	history, err := m.loadHistoryFromDB()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to load history: " + err.Error()),
		})
	} else {
		// First pass: extract tool names
		for _, msg := range history {
			for _, content := range msg.Content {
				if content.Type == llm.ContentTypeToolUse && content.ID != "" && content.ToolName != "" {
					m.toolNames[content.ID] = content.ToolName
				}
			}
		}
		m.renderer.toolNames = m.toolNames
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

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Switched to conversation: " + conversationID),
	})
	m.updateViewportContent()
	return nil
}

// newConversation starts a new conversation
func (m *Model) newConversation() tea.Cmd {
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
	m.newConversation()

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Archived conversation: " + archivedID),
	})
	m.updateViewportContent()
	return nil
}

// listArchivedConversations lists archived conversations
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

// deleteConversation deletes the current conversation
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
			marker = "\u2192 "
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

// syncConversation reloads messages from the database, picking up any changes
// made by other Shelley instances (e.g., web UI or another terminal)
func (m *Model) syncConversation() tea.Cmd {
	if m.config.DB == nil {
		m.showError("Database not configured. Use -sync flag to enable.")
		return nil
	}

	if m.conversationID == "" {
		m.showError("No active conversation to sync")
		return nil
	}

	// Count current messages for comparison
	prevCount := len(m.messages)

	// Reload history from database
	history, err := m.loadHistoryFromDB()
	if err != nil {
		m.showError("Failed to sync: " + err.Error())
		return nil
	}

	// Clear and rebuild messages
	m.messages = nil
	m.toolNames = make(map[string]string)

	// First pass: extract tool names
	for _, msg := range history {
		for _, content := range msg.Content {
			if content.Type == llm.ContentTypeToolUse && content.ID != "" && content.ToolName != "" {
				m.toolNames[content.ID] = content.ToolName
			}
		}
	}
	m.renderer.toolNames = m.toolNames

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

	// Update the loop's history if it exists
	if m.loop != nil {
		m.loop.SetHistory(history)
	}

	newCount := len(m.messages)
	diff := newCount - prevCount

	var statusMsg string
	if diff > 0 {
		statusMsg = fmt.Sprintf("Synced: %d new message(s)", diff)
	} else if diff < 0 {
		statusMsg = fmt.Sprintf("Synced: %d message(s) removed", -diff)
	} else {
		statusMsg = "Synced: no changes"
	}

	m.showSystemMessage(statusMsg)
	return nil
}
