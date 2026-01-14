package cli

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleTabCompletion handles tab key for completion
func (m *Model) handleTabCompletion() tea.Cmd {
	text := m.textarea.Value()
	textToCursor := text

	if len(m.completions) > 0 && strings.HasPrefix(text, m.completionPrefix) {
		m.completionIndex = (m.completionIndex + 1) % len(m.completions)
		m.textarea.SetValue(m.completions[m.completionIndex])
		m.textarea.CursorEnd()
		return nil
	}

	m.completionPrefix = text
	m.completions = nil
	m.completionIndex = 0

	if strings.HasPrefix(textToCursor, "/") && !strings.Contains(textToCursor, " ") {
		m.completions = m.completeSlashCommand(textToCursor)
	} else {
		m.completions = m.completeFilePath(textToCursor)
	}

	if len(m.completions) == 0 {
		return nil
	}

	if len(m.completions) == 1 {
		m.textarea.SetValue(m.completions[0])
		m.textarea.CursorEnd()
		m.completions = nil
	} else {
		common := longestCommonPrefix(m.completions)
		if common != text {
			m.textarea.SetValue(common)
			m.textarea.CursorEnd()
			m.completionPrefix = common
		} else {
			m.textarea.SetValue(m.completions[0])
			m.textarea.CursorEnd()
		}
	}

	return nil
}

// completeSlashCommand returns completions for slash commands
func (m *Model) completeSlashCommand(prefix string) []string {
	commands := []string{
		"/help",
		"/keys",
		"/shortcuts",
		"/clear",
		"/run",
		"/stop",
		"/cancel",
		"/quit",
		"/exit",
		"/verbose",
		"/attach",
		"/image",
		"/attachments",
		"/theme",
		"/cwd",
		"/cd",
		"/status",
		"/export",
	}

	if m.config.DB != nil {
		commands = append(commands,
			"/conversations",
			"/convos",
			"/search",
			"/switch",
			"/new",
			"/rename",
			"/archive",
			"/archived",
			"/unarchive",
			"/delete",
		)
	} else {
		commands = append(commands,
			"/save",
			"/load",
			"/sessions",
		)
	}

	if m.config.ModelManager != nil {
		commands = append(commands,
			"/models",
			"/model",
			"/fast",
			"/smart",
			"/think",
			"/opus",
			"/context",
		)
	}

	commands = append(commands, "/git")

	var matches []string
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// completeFilePath returns completions for file paths
func (m *Model) completeFilePath(text string) []string {
	words := splitRespectingQuotes(text)
	if len(words) == 0 {
		return nil
	}

	lastWord := words[len(words)-1]
	prefix := strings.Join(words[:len(words)-1], " ")
	if prefix != "" {
		prefix += " "
	}

	pathToComplete := lastWord
	homeExpanded := false
	if strings.HasPrefix(pathToComplete, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			pathToComplete = filepath.Join(home, pathToComplete[2:])
			homeExpanded = true
		}
	}

	if !filepath.IsAbs(pathToComplete) {
		pathToComplete = filepath.Join(m.config.WorkingDir, pathToComplete)
	}

	dir := filepath.Dir(pathToComplete)
	base := filepath.Base(pathToComplete)

	if strings.HasSuffix(lastWord, "/") || strings.HasSuffix(lastWord, string(filepath.Separator)) {
		dir = pathToComplete
		base = ""
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}

		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			var completedPath string
			if homeExpanded {
				home, _ := os.UserHomeDir()
				relDir := strings.TrimPrefix(dir, home)
				completedPath = "~" + filepath.Join(relDir, name)
			} else if strings.HasPrefix(lastWord, "/") {
				completedPath = filepath.Join(dir, name)
			} else {
				origDir := filepath.Dir(lastWord)
				if origDir == "." && !strings.HasPrefix(lastWord, "./") {
					completedPath = name
				} else {
					completedPath = filepath.Join(origDir, name)
				}
			}

			if entry.IsDir() {
				completedPath += "/"
			}

			matches = append(matches, prefix+completedPath)
		}
	}

	return matches
}

// splitRespectingQuotes splits a string by spaces, respecting quoted sections
func splitRespectingQuotes(s string) []string {
	var result []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for i, r := range s {
		if (r == '"' || r == '\'') && (i == 0 || s[i-1] != '\\') {
			if !inQuote {
				inQuote = true
				quoteChar = r
			} else if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
		} else if r == ' ' && !inQuote {
			if current.Len() > 0 {
				result = append(result, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	if len(s) > 0 && s[len(s)-1] == ' ' && !inQuote {
		result = append(result, "")
	}

	return result
}

// longestCommonPrefix finds the longest common prefix of a slice of strings
func longestCommonPrefix(strs []string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}

	prefix := strs[0]
	for _, s := range strs[1:] {
		for len(prefix) > 0 && !strings.HasPrefix(s, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

// clearCompletions resets completion state
func (m *Model) clearCompletions() {
	m.completions = nil
	m.completionIndex = 0
	m.completionPrefix = ""
}
