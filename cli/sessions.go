package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
)

// Session represents a saved chat session (legacy JSON format)
type Session struct {
	Name       string           `json:"name"`
	Model      string           `json:"model"`
	WorkingDir string           `json:"working_dir"`
	Messages   []SessionMessage `json:"messages"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// SessionMessage is a simplified message for storage
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
