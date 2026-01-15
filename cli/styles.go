package cli

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme represents a color theme
type Theme string

const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
	ThemeMono  Theme = "mono"
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

// DefaultStyles returns styles based on detected terminal background
func DefaultStyles() *Styles {
	if lipgloss.HasDarkBackground() {
		return DarkStyles()
	}
	return LightStyles()
}

// getStylesForTheme returns styles for the given theme name
// If theme is empty, auto-detects based on terminal background
func getStylesForTheme(theme string) *Styles {
	switch theme {
	case "dark":
		return DarkStyles()
	case "light":
		return LightStyles()
	case "mono", "monochrome":
		return MonoStyles()
	default:
		return DefaultStyles()
	}
}

// DarkStyles returns the dark color scheme
func DarkStyles() *Styles {
	return &Styles{
		theme: ThemeDark,

		UserMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")). // Green - distinct from assistant's cyan
			Bold(true),

		AssistantMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")). // Cyan
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
			Foreground(lipgloss.Color("7")), // Light gray (default text)

		ToolError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")), // Red

		ToolRunning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")). // Yellow
			Italic(true),

		ToolSuccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")). // Green - success is appropriate
			Bold(true),

		Prompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")). // Cyan
			Bold(true),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")),

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")). // White
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Background(lipgloss.Color("236")).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")), // Cyan

		TokenCount: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")), // Yellow

		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")), // Magenta

		WorkingDir: lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")), // Blue

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
			Foreground(lipgloss.Color("22")). // Dark green - distinct from assistant's cyan
			Bold(true),

		AssistantMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("30")). // Dark cyan
			Bold(true),

		SystemMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")). // Dark gray
			Italic(true),

		ErrorMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color("160")). // Dark red
			Bold(true),

		ToolName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("30")). // Dark cyan
			Bold(true),

		ToolInput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")), // Dark gray

		ToolOutput: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")), // Black (default text)

		ToolError: lipgloss.NewStyle().
			Foreground(lipgloss.Color("160")), // Dark red

		ToolRunning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("136")). // Dark yellow/orange
			Italic(true),

		ToolSuccess: lipgloss.NewStyle().
			Foreground(lipgloss.Color("22")). // Dark green - success is appropriate
			Bold(true),

		Prompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color("30")). // Dark cyan
			Bold(true),

		InputCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")), // Black

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")). // Black
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("252")).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle().
			Foreground(lipgloss.Color("30")), // Dark cyan

		TokenCount: lipgloss.NewStyle().
			Foreground(lipgloss.Color("136")), // Dark yellow/orange

		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color("90")), // Dark magenta

		WorkingDir: lipgloss.NewStyle().
			Foreground(lipgloss.Color("24")), // Dark blue

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
		if s.theme == ThemeMono {
			// Keep default border color for mono theme
		} else {
			borderColor = lipgloss.Color("9") // Red for errors
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width)
}

// MonoStyles returns a monochrome theme using only default terminal colors
// with bold, italic, and dim for differentiation
func MonoStyles() *Styles {
	return &Styles{
		theme: ThemeMono,

		UserMessage: lipgloss.NewStyle().
			Bold(true),

		AssistantMessage: lipgloss.NewStyle(),

		SystemMessage: lipgloss.NewStyle().
			Italic(true).
			Faint(true),

		ErrorMessage: lipgloss.NewStyle().
			Bold(true).
			Underline(true),

		ToolName: lipgloss.NewStyle().
			Bold(true),

		ToolInput: lipgloss.NewStyle().
			Faint(true),

		ToolOutput: lipgloss.NewStyle(),

		ToolError: lipgloss.NewStyle().
			Bold(true).
			Underline(true),

		ToolRunning: lipgloss.NewStyle().
			Italic(true),

		ToolSuccess: lipgloss.NewStyle().
			Bold(true),

		Prompt: lipgloss.NewStyle().
			Bold(true),

		InputCursor: lipgloss.NewStyle(),

		Header: lipgloss.NewStyle().
			Bold(true),

		HeaderTitle: lipgloss.NewStyle().
			Bold(true),

		StatusBar: lipgloss.NewStyle().
			Reverse(true).
			Padding(0, 1),

		Spinner: lipgloss.NewStyle(),

		TokenCount: lipgloss.NewStyle().
			Faint(true),

		ModelName: lipgloss.NewStyle().
			Bold(true),

		WorkingDir: lipgloss.NewStyle().
			Faint(true),

		Thinking: lipgloss.NewStyle().
			Italic(true),

		Divider: lipgloss.NewStyle().
			Faint(true),

		BorderColor: lipgloss.Color(""), // Empty = use terminal default
	}
}
