package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/models"
)

// Model name constants for quick switching
const (
	ModelHaiku  = "claude-haiku-4.5"
	ModelSonnet = "claude-sonnet-4.5"
	ModelOpus   = "claude-opus-4.5"
)

// listModels lists available models
func (m *Model) listModels() tea.Cmd {
	if m.config.ModelManager == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Model manager not configured"),
		})
		m.updateViewportContent()
		return nil
	}

	available := m.config.ModelManager.GetAvailableModels()
	if len(available) == 0 {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.SystemMessage.Render("No models available"),
		})
		m.updateViewportContent()
		return nil
	}

	// Store available models for number selection
	m.availableModels = available

	allModels := models.All()
	modelMap := make(map[string]models.Model)
	for _, model := range allModels {
		modelMap[model.ID] = model
	}

	var sb strings.Builder
	sb.WriteString("Available models:\n")
	for i, id := range available {
		marker := "  "
		if id == m.config.Model {
			marker = "→ "
		}
		line := fmt.Sprintf("%s%d. %s", marker, i+1, id)
		if model, ok := modelMap[id]; ok {
			line += fmt.Sprintf(" (%s)", model.Provider)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /model <id> or /model <number> to switch")

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// switchModel switches to a different model
func (m *Model) switchModel(modelID string) tea.Cmd {
	if m.config.ModelManager == nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Model manager not configured"),
		})
		m.updateViewportContent()
		return nil
	}

	// Check if modelID is a number (index into availableModels)
	if idx, err := strconv.Atoi(modelID); err == nil {
		if len(m.availableModels) == 0 {
			m.availableModels = m.config.ModelManager.GetAvailableModels()
		}
		if idx < 1 || idx > len(m.availableModels) {
			m.messages = append(m.messages, renderedMessage{
				role:    llm.MessageRoleAssistant,
				content: m.styles.ErrorMessage.Render(fmt.Sprintf("Invalid model number: %d (use 1-%d)", idx, len(m.availableModels))),
			})
			m.updateViewportContent()
			return nil
		}
		modelID = m.availableModels[idx-1]
	}

	// Check if model is available
	if !m.config.ModelManager.HasModel(modelID) {
		available := m.config.ModelManager.GetAvailableModels()
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render(fmt.Sprintf("Model %q not available. Available: %s", modelID, strings.Join(available, ", "))),
		})
		m.updateViewportContent()
		return nil
	}

	// Get the new service
	newService, err := m.config.ModelManager.GetService(modelID)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Failed to get model: " + err.Error()),
		})
		m.updateViewportContent()
		return nil
	}

	// Update the config
	oldModel := m.config.Model
	m.config.Model = modelID
	m.config.LLMService = newService
	m.modelExplicitlySet = true // User explicitly chose this model

	// If we have an active loop, we need to restart it with the new model
	// The next message will create a new loop with the updated service
	if m.loop != nil {
		if m.loopCancel != nil {
			m.loopCancel()
		}
		m.loop = nil
		m.loopCtx = nil
		m.loopCancel = nil
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render(fmt.Sprintf("Switched model: %s → %s", oldModel, modelID)),
	})
	m.updateViewportContent()
	return nil
}

// showContext shows context window usage information
func (m *Model) showContext() tea.Cmd {
	maxContext := 0
	if m.config.LLMService != nil {
		maxContext = m.config.LLMService.TokenContextWindow()
	}

	// Calculate approximate context usage from total usage
	// Input tokens represent what we've sent to the model
	usedTokens := m.totalUsage.InputTokens

	var sb strings.Builder
	sb.WriteString("Context Window:\n")
	sb.WriteString(fmt.Sprintf("  Model: %s\n", m.config.Model))
	sb.WriteString(fmt.Sprintf("  Max tokens: %s\n", formatTokensInt(maxContext)))
	sb.WriteString(fmt.Sprintf("  Input tokens used: %s\n", formatTokens(usedTokens)))
	sb.WriteString(fmt.Sprintf("  Output tokens used: %s\n", formatTokens(m.totalUsage.OutputTokens)))

	if maxContext > 0 {
		percent := float64(usedTokens) / float64(maxContext) * 100
		sb.WriteString(fmt.Sprintf("  Usage: %.1f%%\n", percent))

		// Visual bar
		barWidth := 40
		filled := int(percent / 100 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		sb.WriteString(fmt.Sprintf("  [%s]", bar))
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// showUsage shows token usage and estimated costs
func (m *Model) showUsage() tea.Cmd {
	u := m.totalUsage

	var sb strings.Builder
	sb.WriteString("Session Usage:\n")

	// Token counts
	sb.WriteString(fmt.Sprintf("  Input tokens:    %s\n", formatTokens(u.InputTokens)))
	sb.WriteString(fmt.Sprintf("  Output tokens:   %s\n", formatTokens(u.OutputTokens)))

	// Cache stats
	if u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0 {
		sb.WriteString(fmt.Sprintf("  Cache created:   %s\n", formatTokens(u.CacheCreationInputTokens)))
		sb.WriteString(fmt.Sprintf("  Cache read:      %s\n", formatTokens(u.CacheReadInputTokens)))
	}

	// Cost estimates based on current model
	// Prices per million tokens (as of 2024)
	var inputPrice, outputPrice, cacheReadPrice, cacheWritePrice float64
	switch {
	case strings.Contains(m.config.Model, "opus"):
		inputPrice, outputPrice = 15.0, 75.0
		cacheReadPrice, cacheWritePrice = 1.5, 18.75
	case strings.Contains(m.config.Model, "sonnet"):
		inputPrice, outputPrice = 3.0, 15.0
		cacheReadPrice, cacheWritePrice = 0.3, 3.75
	case strings.Contains(m.config.Model, "haiku"):
		inputPrice, outputPrice = 0.25, 1.25
		cacheReadPrice, cacheWritePrice = 0.025, 0.3125
	default:
		// Default to Sonnet pricing
		inputPrice, outputPrice = 3.0, 15.0
		cacheReadPrice, cacheWritePrice = 0.3, 3.75
	}

	// Calculate costs (prices are per million tokens)
	inputCost := float64(u.InputTokens) / 1_000_000 * inputPrice
	outputCost := float64(u.OutputTokens) / 1_000_000 * outputPrice
	cacheReadCost := float64(u.CacheReadInputTokens) / 1_000_000 * cacheReadPrice
	cacheWriteCost := float64(u.CacheCreationInputTokens) / 1_000_000 * cacheWritePrice
	totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost

	// Calculate savings from cache (cache read vs full input price)
	cacheSavings := float64(u.CacheReadInputTokens) / 1_000_000 * (inputPrice - cacheReadPrice)

	sb.WriteString(fmt.Sprintf("\nEstimated Cost (%s):\n", m.config.Model))
	sb.WriteString(fmt.Sprintf("  Input:      $%.4f\n", inputCost))
	sb.WriteString(fmt.Sprintf("  Output:     $%.4f\n", outputCost))
	if cacheReadCost > 0 || cacheWriteCost > 0 {
		sb.WriteString(fmt.Sprintf("  Cache read: $%.4f\n", cacheReadCost))
		sb.WriteString(fmt.Sprintf("  Cache write:$%.4f\n", cacheWriteCost))
	}
	sb.WriteString(fmt.Sprintf("  ─────────────────\n"))
	sb.WriteString(fmt.Sprintf("  Total:      $%.4f\n", totalCost))

	if cacheSavings > 0.0001 {
		sb.WriteString(fmt.Sprintf("\n  💰 Cache saved: $%.4f\n", cacheSavings))
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// toggleTheme switches between dark and light themes
func (m *Model) toggleTheme() tea.Cmd {
	if m.styles.Theme() == ThemeDark {
		return m.setTheme("light")
	}
	return m.setTheme("dark")
}

// setTheme changes to a specific theme
func (m *Model) setTheme(themeName string) tea.Cmd {
	var newStyles *Styles
	switch themeName {
	case "dark":
		newStyles = DarkStyles()
	case "light":
		newStyles = LightStyles()
	case "mono", "monochrome":
		newStyles = MonoStyles()
	default:
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Unknown theme: " + themeName + " (available: dark, light, mono)"),
		})
		m.updateViewportContent()
		return nil
	}

	m.styles = newStyles
	m.renderer.styles = newStyles
	m.spinner.Style = newStyles.Spinner

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render("Theme set to: " + themeName),
	})
	m.updateViewportContent()
	return nil
}

// changeWorkingDir changes the working directory
func (m *Model) changeWorkingDir(path string) tea.Cmd {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		}
	}

	// Make path absolute if relative
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.config.WorkingDir, path)
	}

	// Clean the path
	path = filepath.Clean(path)

	// Check if path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Directory not found: " + path),
		})
		m.updateViewportContent()
		return nil
	}
	if !info.IsDir() {
		m.messages = append(m.messages, renderedMessage{
			role:    llm.MessageRoleAssistant,
			content: m.styles.ErrorMessage.Render("Not a directory: " + path),
		})
		m.updateViewportContent()
		return nil
	}

	oldDir := m.config.WorkingDir
	m.config.WorkingDir = path

	// Update the toolset's working directory if it exists
	if m.toolSet != nil {
		m.toolSet.WorkingDir().Set(path)
	}

	// Format paths for display
	oldDisplay := oldDir
	newDisplay := path
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(oldDisplay, home) {
			oldDisplay = "~" + oldDisplay[len(home):]
		}
		if strings.HasPrefix(newDisplay, home) {
			newDisplay = "~" + newDisplay[len(home):]
		}
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.ToolSuccess.Render(fmt.Sprintf("Changed directory: %s → %s", oldDisplay, newDisplay)),
	})
	m.updateViewportContent()
	return nil
}

// showStatus shows current session status
func (m *Model) showStatus() tea.Cmd {
	var sb strings.Builder
	sb.WriteString("Session Status:\n")

	// Model
	sb.WriteString(fmt.Sprintf("  Model: %s\n", m.styles.ModelName.Render(m.config.Model)))

	// Working directory
	wd := m.config.WorkingDir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(wd, home) {
		wd = "~" + wd[len(home):]
	}
	sb.WriteString(fmt.Sprintf("  Working Dir: %s\n", m.styles.WorkingDir.Render(wd)))

	// Conversation
	if m.conversationID != "" {
		sb.WriteString(fmt.Sprintf("  Conversation: %s\n", m.conversationID))
	} else {
		sb.WriteString("  Conversation: (none)\n")
	}

	// Context usage
	if m.config.LLMService != nil {
		maxContext := m.config.LLMService.TokenContextWindow()
		if maxContext > 0 && m.totalUsage.InputTokens > 0 {
			percent := float64(m.totalUsage.InputTokens) / float64(maxContext) * 100
			sb.WriteString(fmt.Sprintf("  Context: %s / %s (%.1f%%)\n",
				formatTokens(m.totalUsage.InputTokens),
				formatTokensInt(maxContext),
				percent))
		} else {
			sb.WriteString(fmt.Sprintf("  Context: %s used\n", formatTokens(m.totalUsage.InputTokens)))
		}
	}

	// Token totals
	sb.WriteString(fmt.Sprintf("  Tokens: ↑%s ↓%s\n",
		formatTokens(m.totalUsage.InputTokens),
		formatTokens(m.totalUsage.OutputTokens)))

	// Theme
	sb.WriteString(fmt.Sprintf("  Theme: %s\n", m.styles.Theme()))

	// Processing state
	if m.processing {
		sb.WriteString("  State: " + m.styles.ToolRunning.Render("processing...") + "\n")
	} else {
		sb.WriteString("  State: ready\n")
	}

	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: m.styles.SystemMessage.Render(sb.String()),
	})
	m.updateViewportContent()
	return nil
}

// tempSwitchModel switches to a target model for the next turn,
// restoring the previous model after the response completes.
// If already on the target model, it does nothing.
func (m *Model) tempSwitchModel(targetModel, reason string) {
	if m.config.ModelManager == nil {
		return
	}

	// If we're already on the target model, nothing to do
	if m.config.Model == targetModel {
		return
	}

	// Only save restoreModel if we don't already have one pending
	if m.restoreModel == "" {
		m.restoreModel = m.config.Model
	}

	svc, err := m.config.ModelManager.GetService(targetModel)
	if err != nil {
		return
	}

	m.config.Model = targetModel
	m.config.LLMService = svc

	// Reset loop so next turn uses the new model
	if m.loop != nil {
		if m.loopCancel != nil {
			m.loopCancel()
		}
		m.loop = nil
	}

	if reason != "" {
		m.showSystemMessage(reason)
	}
}
