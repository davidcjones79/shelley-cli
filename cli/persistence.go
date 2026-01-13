package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"shelley.exe.dev/db"
	"shelley.exe.dev/db/generated"
	"shelley.exe.dev/gitstate"
	"shelley.exe.dev/llm"
)

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
