package cli

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme represents a color theme
type Theme string

const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

// Styles defines the visual styling for the CLI interface
type Styles struct {
	theme Theme
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

// DefaultStyles returns the default (dark) color scheme
func DefaultStyles() *Styles {
	return DarkStyles()
}

// DarkStyles returns the dark color scheme
func DarkStyles() *Styles {
	return &Styles{
		theme: ThemeDark,

		UserMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")). // White
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
			Foreground(lipgloss.Color("10")). // Green
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
			Foreground(lipgloss.Color("10")). // Green
			Bold(true),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")),

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")). // Green
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Background(lipgloss.Color("236")).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")), // Green

		TokenCount: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")), // Yellow

		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")), // Green

		WorkingDir: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")), // Green

		Thinking: lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // Orange/gold - visible
			Italic(true),

		Divider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")),

		BorderColor: lipgloss.Color("8"), // Gray
	}
}

// LightStyles returns the light color scheme
func LightStyles() *Styles {
	return &Styles{
		theme: ThemeLight,

		UserMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")). // Black
			Bold(true),

		AssistantMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Dark green
			Bold(true),

		SystemMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")). // Dark gray
			Italic(true),

		ErrorMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("160")). // Dark red
			Bold(true),

		ToolName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Green
			Bold(true),

		ToolInput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")), // Dark gray

		ToolOutput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")), // Dark green

		ToolError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("160")), // Dark red

		ToolRunning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("136")). // Dark yellow/orange
			Italic(true),

		ToolSuccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Dark green
			Bold(true),

		Prompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Green
			Bold(true),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")), // Black

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Green
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("252")).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")), // Green

		TokenCount: lipgloss.NewStyle().
			Foreground(lipgloss.Color("136")), // Dark yellow/orange

		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")), // Green

		WorkingDir: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")), // Green

		Thinking: lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")). // Orange/gold - visible
			Italic(true),

		Divider: lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")), // Light gray

		BorderColor: lipgloss.Color("250"), // Light gray
	}
}

// Theme returns the current theme
func (s *Styles) Theme() Theme {
	return s.theme
}

// RolePrefix returns a styled prefix for a message role
func (s *Styles) RolePrefix(role string) string {
	switch role {
	case "user":
		return s.UserMessage.Render("▶ ")
	case "assistant":
		return ""
	case "system":
		return s.SystemMessage.Render("")
	default:
		return ""
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
