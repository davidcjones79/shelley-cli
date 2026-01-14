package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// uploadedFile represents a file in the uploads directory
type uploadedFile struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
	Type    string // "image", "text", "csv", "markdown", "json", "unknown"
}

// getUploadsDir returns the uploads directory path
func getUploadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "./uploads"
	}
	return filepath.Join(home, "uploads")
}

// detectFileType returns the type of file based on extension
func detectFileType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return "image"
	case ".csv":
		return "csv"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".txt", ".log":
		return "text"
	case ".go", ".py", ".js", ".ts", ".rs", ".c", ".cpp", ".h", ".java", ".rb", ".sh", ".bash":
		return "code"
	case ".html", ".css", ".xml", ".yaml", ".yml", ".toml":
		return "text"
	default:
		return "unknown"
	}
}

// formatSize formats a file size in human-readable form
func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	} else {
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// formatTimeAgo formats a time as relative to now
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	} else if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", mins)
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// getTypeIcon returns an emoji for the file type
func getTypeIcon(fileType string) string {
	switch fileType {
	case "image":
		return "🖼️"
	case "csv":
		return "📊"
	case "markdown":
		return "📝"
	case "json":
		return "📋"
	case "text":
		return "📄"
	case "code":
		return "💻"
	default:
		return "📁"
	}
}

// listUploadedFiles shows files in the uploads directory
func (m *Model) listUploadedFiles() tea.Cmd {
	uploadsDir := getUploadsDir()

	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		if os.IsNotExist(err) {
			m.showSystemMessage(fmt.Sprintf("No uploads directory. Run 'shelley igor' to summon Igor."))
		} else {
			m.showError("Failed to read uploads: " + err.Error())
		}
		return nil
	}

	if len(entries) == 0 {
		m.showSystemMessage("No files in ~/uploads. Summon Igor with 'shelley igor'.")
		return nil
	}

	// Collect file info
	var files []uploadedFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		files = append(files, uploadedFile{
			Path:    filepath.Join(uploadsDir, name),
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Type:    detectFileType(name),
		})
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	// Limit to 10 most recent
	if len(files) > 10 {
		files = files[:10]
	}

	// Store for later picking
	m.uploadedFiles = files

	// Build display
	var sb strings.Builder
	sb.WriteString("Files in ~/uploads:\n")
	for i, f := range files {
		icon := getTypeIcon(f.Type)
		sb.WriteString(fmt.Sprintf("  %d. %s %s (%s, %s)\n",
			i+1, icon, f.Name, formatSize(f.Size), formatTimeAgo(f.ModTime)))
	}
	sb.WriteString("\nUse /pick <n> to analyze a file")

	m.showSystemMessage(sb.String())
	return nil
}

// pickUploadedFile picks a file by number and sends it for analysis
func (m *Model) pickUploadedFile(arg string) tea.Cmd {
	// Parse the number
	num, err := strconv.Atoi(arg)
	if err != nil || num < 1 {
		m.showError("Usage: /pick <number> - use /pick to see available files")
		return nil
	}

	// If we don't have a cached list, refresh it
	if len(m.uploadedFiles) == 0 {
		m.refreshUploadedFiles()
	}

	if num > len(m.uploadedFiles) {
		m.showError(fmt.Sprintf("Invalid selection: %d (only %d files)", num, len(m.uploadedFiles)))
		return nil
	}

	file := m.uploadedFiles[num-1]

	// For images, load directly as attachment to avoid path encoding issues
	if file.Type == "image" {
		// If user hasn't explicitly chosen a model, prefer GPT-5 for /pick image
		if !m.modelExplicitlySet {
			m.tempSwitchModel("gpt-5", "Using GPT-5 for image analysis from /pick (will restore after)")
		}

		maxImageDim := m.config.LLMService.MaxImageDimension()
		att, err := loadImageAsAttachment(file.Path, maxImageDim)
		if err != nil {
			m.showError(fmt.Sprintf("Failed to load image: %v", err))
			return nil
		}
		m.pendingAttachments = append(m.pendingAttachments, *att)
		m.textarea.SetValue(fmt.Sprintf("Please analyze this image (%s) and describe what you see.", file.Name))
		return m.sendMessage()
	}

	// For non-image files, use the file path in the prompt
	var prompt string
	switch file.Type {
	case "csv":
		prompt = fmt.Sprintf("Please read and summarize this CSV file. Show the structure and key insights: %s", file.Path)
	case "markdown":
		prompt = fmt.Sprintf("Please read and summarize this markdown file: %s", file.Path)
	case "json":
		prompt = fmt.Sprintf("Please read and explain the structure of this JSON file: %s", file.Path)
	case "code":
		prompt = fmt.Sprintf("Please read and explain this code file: %s", file.Path)
	default:
		prompt = fmt.Sprintf("Please read and summarize this file: %s", file.Path)
	}

	m.textarea.SetValue(prompt)
	return m.sendMessage()
}

// refreshUploadedFiles updates the cached list of uploaded files
func (m *Model) refreshUploadedFiles() {
	uploadsDir := getUploadsDir()
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		return
	}

	var files []uploadedFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		files = append(files, uploadedFile{
			Path:    filepath.Join(uploadsDir, name),
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Type:    detectFileType(name),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	if len(files) > 10 {
		files = files[:10]
	}
	m.uploadedFiles = files
}
