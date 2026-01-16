package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Script represents a saved orchestration script
type Script struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Created     string   `json:"created"`
	Tags        []string `json:"tags,omitempty"`
	Usage       string   `json:"usage,omitempty"`
}

// ScriptRegistry holds metadata about saved scripts
type ScriptRegistry struct {
	Scripts []Script `json:"scripts"`
}

// getScriptsDir returns the path to the scripts directory
func getScriptsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "shelley", "scripts")
}

// loadRegistry loads the script registry from disk
func loadRegistry() (*ScriptRegistry, error) {
	registryPath := filepath.Join(getScriptsDir(), "registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return &ScriptRegistry{Scripts: []Script{}}, err
	}

	var registry ScriptRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return &ScriptRegistry{Scripts: []Script{}}, err
	}
	return &registry, nil
}

// saveRegistry writes the script registry to disk
func saveRegistry(registry *ScriptRegistry) error {
	scriptsDir := getScriptsDir()
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		return err
	}

	registryPath := filepath.Join(scriptsDir, "registry.json")
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath, data, 0644)
}

// listScripts returns a formatted list of saved scripts
func (m *Model) listScripts() string {
	registry, err := loadRegistry()
	if err != nil || len(registry.Scripts) == 0 {
		return "No scripts saved yet. Use /script-save <name> <file> [description] to save a script."
	}

	// Sort by name
	sort.Slice(registry.Scripts, func(i, j int) bool {
		return registry.Scripts[i].Name < registry.Scripts[j].Name
	})

	var sb strings.Builder
	sb.WriteString("📜 Saved Scripts:\n\n")

	for i, script := range registry.Scripts {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, script.Name))
		if script.Description != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", script.Description))
		}
		if len(script.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(script.Tags, ", ")))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Use /script-show <name> to view, /script-run <name> to execute")
	return sb.String()
}

// saveScript saves a script to the registry
func (m *Model) saveScript(name, filePath, description string, tags []string) string {
	scriptsDir := getScriptsDir()
	scriptSubDir := filepath.Join(scriptsDir, "scripts")
	if err := os.MkdirAll(scriptSubDir, 0755); err != nil {
		return fmt.Sprintf("Error creating scripts directory: %v", err)
	}

	// Resolve relative path against working directory
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(m.config.WorkingDir, filePath)
	}

	// Read the source file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}

	// Sanitize name for filesystem
	safeName := sanitizeScriptName(name)
	if safeName == "" {
		return "Invalid script name. Use alphanumeric characters and hyphens."
	}

	// Save to scripts directory
	destPath := filepath.Join(scriptSubDir, safeName+".sh")
	if err := os.WriteFile(destPath, content, 0755); err != nil {
		return fmt.Sprintf("Error saving script: %v", err)
	}

	// Update registry
	registry, _ := loadRegistry()

	// Remove existing entry with same name
	newScripts := []Script{}
	for _, s := range registry.Scripts {
		if s.Name != safeName {
			newScripts = append(newScripts, s)
		}
	}

	newScripts = append(newScripts, Script{
		Name:        safeName,
		Description: description,
		Path:        filepath.Join("scripts", safeName+".sh"),
		Created:     time.Now().Format(time.RFC3339),
		Tags:        tags,
	})
	registry.Scripts = newScripts

	if err := saveRegistry(registry); err != nil {
		return fmt.Sprintf("Error saving registry: %v", err)
	}

	return fmt.Sprintf("✅ Saved script '%s' to %s", safeName, destPath)
}

// showScript displays the contents of a saved script
func (m *Model) showScript(name string) string {
	registry, err := loadRegistry()
	if err != nil {
		return "No scripts found."
	}

	for _, script := range registry.Scripts {
		if script.Name == name {
			scriptPath := filepath.Join(getScriptsDir(), script.Path)
			content, err := os.ReadFile(scriptPath)
			if err != nil {
				return fmt.Sprintf("Error reading script: %v", err)
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("📜 Script: %s\n", script.Name))
			if script.Description != "" {
				sb.WriteString(fmt.Sprintf("Description: %s\n", script.Description))
			}
			sb.WriteString(fmt.Sprintf("Created: %s\n", script.Created))
			if len(script.Tags) > 0 {
				sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(script.Tags, ", ")))
			}
			sb.WriteString("\n```bash\n")
			sb.WriteString(string(content))
			if !strings.HasSuffix(string(content), "\n") {
				sb.WriteString("\n")
			}
			sb.WriteString("```")
			return sb.String()
		}
	}

	return fmt.Sprintf("Script '%s' not found. Use /scripts to list available scripts.", name)
}

// runScript executes a saved script
func (m *Model) runScript(name string) string {
	registry, err := loadRegistry()
	if err != nil {
		return "No scripts found."
	}

	for _, script := range registry.Scripts {
		if script.Name == name {
			scriptPath := filepath.Join(getScriptsDir(), script.Path)

			// Execute the script
			cmd := exec.Command("bash", scriptPath)
			cmd.Dir = m.config.WorkingDir
			output, err := cmd.CombinedOutput()

			var sb strings.Builder
			if err != nil {
				sb.WriteString(fmt.Sprintf("❌ Script '%s' failed: %v\n\n", name, err))
			} else {
				sb.WriteString(fmt.Sprintf("✅ Script '%s' completed:\n\n", name))
			}

			if len(output) > 0 {
				out := string(output)
				if len(out) > 5000 {
					out = out[:2500] + "\n\n... (truncated) ...\n\n" + out[len(out)-2500:]
				}
				sb.WriteString(out)
			} else {
				sb.WriteString("(no output)")
			}

			return sb.String()
		}
	}

	return fmt.Sprintf("Script '%s' not found. Use /scripts to list available scripts.", name)
}

// deleteScript removes a script from the registry
func (m *Model) deleteScript(name string) string {
	registry, err := loadRegistry()
	if err != nil {
		return "No scripts found."
	}

	var found *Script
	newScripts := []Script{}
	for _, s := range registry.Scripts {
		if s.Name == name {
			found = &s
		} else {
			newScripts = append(newScripts, s)
		}
	}

	if found == nil {
		return fmt.Sprintf("Script '%s' not found. Use /scripts to list available scripts.", name)
	}

	// Remove the script file
	scriptPath := filepath.Join(getScriptsDir(), found.Path)
	os.Remove(scriptPath) // Ignore error if file doesn't exist

	// Update registry
	registry.Scripts = newScripts
	if err := saveRegistry(registry); err != nil {
		return fmt.Sprintf("Error updating registry: %v", err)
	}

	return fmt.Sprintf("🗑️ Deleted script '%s'", name)
}

// sanitizeScriptName makes a name safe for filesystem use
func sanitizeScriptName(name string) string {
	// Convert to lowercase, replace spaces with hyphens
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")

	// Keep only alphanumeric and hyphens
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}

	// Remove consecutive hyphens and trim
	cleaned := result.String()
	for strings.Contains(cleaned, "--") {
		cleaned = strings.ReplaceAll(cleaned, "--", "-")
	}
	cleaned = strings.Trim(cleaned, "-")

	// Limit length
	if len(cleaned) > 50 {
		cleaned = cleaned[:50]
	}

	return cleaned
}
