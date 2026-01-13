package cli

import (
	"github.com/charmbracelet/lipgloss"
)

// Styles defines the visual styling for the CLI interface
type Styles struct {
	// Message styles
	UserMessage      lipgloss.Style
	AssistantMessage lipgloss.Style
	SystemMessage    lipgloss.Style
	ErrorMessage     lipgloss.Style

	// Tool styles
	ToolName    lipgloss.Style
	ToolInput   lipgloss.Style
	ToolOutput  lipgloss.Style
	ToolError   lipgloss.Style
	ToolRunning lipgloss.Style
	ToolSuccess lipgloss.Style

	// Input styles
	Prompt      lipgloss.Style
	InputCursor lipgloss.Style

	// Header styles
	Header      lipgloss.Style
	HeaderTitle lipgloss.Style

	// Status styles
	StatusBar  lipgloss.Style
	Spinner    lipgloss.Style
	TokenCount lipgloss.Style
	ModelName  lipgloss.Style
	WorkingDir lipgloss.Style

	// Thinking styles
	Thinking lipgloss.Style

	// Border and divider
	Divider     lipgloss.Style
	BorderColor lipgloss.Color
}

// DefaultStyles returns the default color scheme
func DefaultStyles() *Styles {
	return &Styles{
		UserMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")). // Cyan
			Bold(true),

		AssistantMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")). // Green
			Bold(true),

		SystemMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")). // Gray
			Italic(true),

		ErrorMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")). // Red
			Bold(true),

		ToolName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")). // Cyan
			Bold(true),

		ToolInput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")), // Gray

		ToolOutput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")), // Green

		ToolError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")), // Red

		ToolRunning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // Yellow
			Italic(true),

		ToolSuccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")). // Green
			Bold(true),

		Prompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")). // Magenta
			Bold(true),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")),

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")). // Magenta
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Background(lipgloss.Color("236")).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")),

		TokenCount: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")), // Yellow

		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")), // Magenta

		WorkingDir: lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")), // Blue

		Thinking: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")). // Gray
			Italic(true),

		Divider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),

		BorderColor: lipgloss.Color("8"), // Gray
	}
}

// RolePrefix returns a styled prefix for a message role
func (s *Styles) RolePrefix(role string) string {
	switch role {
	case "user":
		return s.UserMessage.Render("You: ")
	case "assistant":
		return s.AssistantMessage.Render("Shelley: ")
	case "system":
		return s.SystemMessage.Render("System: ")
	default:
		return role + ": "
	}
}

// ToolBoxStyle creates a bordered box style for tool displays
func (s *Styles) ToolBoxStyle(width int, isError bool) lipgloss.Style {
	borderColor := s.BorderColor
	if isError {
		borderColor = lipgloss.Color("9") // Red for errors
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width)
}
