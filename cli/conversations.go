package cli

import (
	"context"
	"fmt"
	"os"
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

	var sb strings.Builder
	sb.WriteString("Recent conversations:\n")
	for _, conv := range conversations {
		marker := "  "
		if conv.ConversationID == m.conversationID {
			marker = "\u2192 "
		}
		name := conv.ConversationID
		if conv.Slug != nil && *conv.Slug != "" {
			name = *conv.Slug
		}
		line := fmt.Sprintf("%s%s (%s)", marker, name, conv.UpdatedAt.Format("Jan 2 15:04"))
		if conv.Cwd != nil && *conv.Cwd != "" {
			cwd := *conv.Cwd
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

	_, err := m.config.DB.GetConversationByID(context.Background(), conversationID)
	if err != nil {
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

	history, err := m.loadHistoryFromDB()
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to load history: " + err.Error()),
		})
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
