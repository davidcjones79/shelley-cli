package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"shelley.exe.dev/llm"
	"shelley.exe.dev/llm/imageutil"
)

// imagePathRegex matches:
// 1. Bracketed paths: [/path/to/image.png]
// 2. Bare paths that start with / or ~/ and end with an image extension
// 3. Single-quoted paths: '/path/to/image.png'
// 4. Double-quoted paths: "/path/to/image.png"
// 5. file:// URLs: file:///path/to/image.png
var imagePathRegex = regexp.MustCompile(`(?:\[([^\[\]]+\.(?:png|jpg|jpeg|gif|webp))\]|'([^']+\.(?:png|jpg|jpeg|gif|webp))'|"([^"]+\.(?:png|jpg|jpeg|gif|webp))"|file://([^\s]+\.(?:png|jpg|jpeg|gif|webp))|(?:^|\s)((?:/|~/)[^\s]+\.(?:png|jpg|jpeg|gif|webp)))`)

// unescapeShellPath converts shell-escaped paths (with backslash-space) to normal paths
func unescapeShellPath(path string) string {
	// Replace shell escape sequences: \ -> <placeholder>, then \ space -> space, then restore
	// Common escapes: \ (space), \( \) \[ \] etc.
	result := path
	result = strings.ReplaceAll(result, "\\ ", " ")
	result = strings.ReplaceAll(result, "\\(", "(")
	result = strings.ReplaceAll(result, "\\)", ")")
	result = strings.ReplaceAll(result, "\\[", "[")
	result = strings.ReplaceAll(result, "\\]", "]")
	result = strings.ReplaceAll(result, "\\'", "'")
	result = strings.ReplaceAll(result, "\\\"", "\"")
	return result
}

// extractImagePathsFromText extracts image file paths from text.
// Returns the cleaned text (with paths removed) and a list of valid image paths.
func extractImagePathsFromText(text, workingDir string) (cleanedText string, imagePaths []string) {
	// Special case: if the entire input looks like a bare image path (possibly with spaces),
	// try it directly. This handles drag-and-drop from Finder where paths aren't quoted.
	trimmed := strings.TrimSpace(text)
	// Try both raw and unescaped versions (for shell-escaped paths like /path/to/Screenshot\ 2024.png)
	unescaped := unescapeShellPath(trimmed)
	for _, candidate := range []string{trimmed, unescaped} {
		if (strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "~/")) && hasImageExtension(candidate) {
			path := candidate
			if strings.HasPrefix(path, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					path = filepath.Join(home, path[2:])
				}
			}
			if isValidImageFile(path) {
				return "", []string{path}
			}
		}
	}

	matches := imagePathRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var result strings.Builder
	lastEnd := 0

	for _, match := range matches {
		// match[0]:match[1] is the full match
		// Groups (each pair may be -1 if not matched):
		// match[2]:match[3] = group 1: bracketed path [path]
		// match[4]:match[5] = group 2: single-quoted 'path'
		// match[6]:match[7] = group 3: double-quoted "path"
		// match[8]:match[9] = group 4: file:// URL
		// match[10]:match[11] = group 5: bare path starting with / or ~/
		
		var path string
		var fullStart, fullEnd int
		
		switch {
		case match[2] != -1:
			// Bracketed path
			path = text[match[2]:match[3]]
			fullStart, fullEnd = match[0], match[1]
		case match[4] != -1:
			// Single-quoted path
			path = text[match[4]:match[5]]
			fullStart, fullEnd = match[0], match[1]
		case match[6] != -1:
			// Double-quoted path
			path = text[match[6]:match[7]]
			fullStart, fullEnd = match[0], match[1]
		case match[8] != -1:
			// file:// URL
			path = text[match[8]:match[9]]
			fullStart, fullEnd = match[0], match[1]
		case match[10] != -1:
			// Bare path
			path = text[match[10]:match[11]]
			fullStart, fullEnd = match[10], match[11]
			// Include any leading whitespace in removal
			if match[0] < match[10] {
				fullStart = match[0]
			}
		default:
			continue
		}

		// Expand ~ to home directory
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}

		// Make relative paths absolute
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDir, path)
		}

		// Check if file exists and is an image
		if !isValidImageFile(path) {
			continue
		}

		// Add text before this match
		result.WriteString(text[lastEnd:fullStart])
		lastEnd = fullEnd

		imagePaths = append(imagePaths, path)
	}

	// Add remaining text
	result.WriteString(text[lastEnd:])
	
	return strings.TrimSpace(result.String()), imagePaths
}

// hasImageExtension checks if a path ends with a supported image extension
func hasImageExtension(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".gif") ||
		strings.HasSuffix(lower, ".webp")
}

// isValidImageFile checks if a path points to a valid image file
func isValidImageFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	// Check size (10MB limit)
	if info.Size() > 10*1024*1024 {
		return false
	}

	// Read first bytes to detect content type
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}

	contentType := http.DetectContentType(buf[:n])
	return strings.HasPrefix(contentType, "image/")
}

// loadImageAsAttachment loads an image file and returns it as an attachment.
// If maxImageDimension > 0, images larger than that dimension will be resized.
func loadImageAsAttachment(path string, maxImageDimension int) (*imageAttachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Determine media type from file extension first, fall back to detection
	ext := strings.ToLower(filepath.Ext(path))
	contentType := supportedImageTypes[ext]
	if contentType == "" {
		// Fall back to content detection
		contentType = http.DetectContentType(data)
		if !strings.HasPrefix(contentType, "image/") {
			contentType = "image/png" // fallback
		}
	}

	// Resize if needed
	if maxImageDimension > 0 {
		resized, newFormat, _, err := imageutil.ResizeImage(data, maxImageDimension)
		if err == nil {
			data = resized
			contentType = newFormat
		}
		// If resize fails, just use original
	}

	return &imageAttachment{
		path:      path,
		mediaType: contentType,
		data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}

// describeImage sends an image to a vision model and returns the description
func (m *Model) describeImage(input string) tea.Cmd {
	// Parse input - could be just path or "path prompt"
	parts := strings.SplitN(strings.TrimSpace(input), " ", 2)
	imagePath := parts[0]
	prompt := "Describe this image in detail."
	if len(parts) > 1 {
		prompt = parts[1]
	}

	// Clean up path (remove quotes, brackets)
	imagePath = strings.Trim(imagePath, "\"'[]")
	
	// Expand ~ to home directory
	if strings.HasPrefix(imagePath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			imagePath = filepath.Join(home, imagePath[2:])
		}
	}

	// Check if file exists
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		m.showError("File not found: " + imagePath)
		return nil
	}

	// Load the image
	attachment, err := loadImageAsAttachment(imagePath, m.config.LLMService.MaxImageDimension())
	if err != nil {
		m.showError("Failed to load image: " + err.Error())
		return nil
	}

	m.showSystemMessage("Analyzing image with vision model...")

	// Build message with image
	contents := []llm.Content{
		{Type: llm.ContentTypeText, Text: prompt},
		{
			Type:      llm.ContentTypeText,
			MediaType: attachment.mediaType,
			Data:      attachment.data,
		},
	}

	userMsg := llm.Message{
		Role:    llm.MessageRoleUser,
		Content: contents,
	}

	// Send directly to LLM (bypassing the loop for this one-off request)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		req := &llm.Request{
			Messages: []llm.Message{userMsg},
		}
		resp, err := m.config.LLMService.Do(ctx, req)
		if err != nil {
			return describeImageResultMsg{err: err}
		}

		// Extract text from response
		var text string
		for _, content := range resp.Content {
			if content.Type == llm.ContentTypeText {
				text += content.Text
			}
		}

		return describeImageResultMsg{text: text}
	}
}

// describeImageResultMsg is the result of image description
type describeImageResultMsg struct {
	text string
	err  error
}

// getImageResultFiles returns all image result files sorted by timestamp (newest first)
func getImageResultFiles() ([]string, error) {
	// Check for timestamped files first
	matches, err := filepath.Glob("/tmp/shelley-image-*.txt")
	if err != nil {
		return nil, err
	}
	
	// Filter out the old-style result file from glob if present
	var files []string
	for _, f := range matches {
		if f != "/tmp/shelley-image-result.txt" {
			files = append(files, f)
		}
	}
	
	// Sort by filename descending (timestamp in name means lexical = chronological)
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	
	// Fall back to legacy file if no timestamped files
	if len(files) == 0 {
		if _, err := os.Stat("/tmp/shelley-image-result.txt"); err == nil {
			files = []string{"/tmp/shelley-image-result.txt"}
		}
	}
	
	return files, nil
}

// listImageResults shows available image results
func (m *Model) listImageResults() tea.Cmd {
	files, err := getImageResultFiles()
	if err != nil || len(files) == 0 {
		m.showError("No image results found. Run describe-image on your Mac first.")
		return nil
	}
	
	var sb strings.Builder
	sb.WriteString("Image results (newest first):\n")
	for i, f := range files {
		if i >= 10 {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(files)-10))
			break
		}
		// Get timestamp
		info, _ := os.Stat(f)
		var timeStr string
		if info != nil {
			timeStr = info.ModTime().Format("Jan 2 15:04")
		}
		// Read first line of content as preview
		preview := getImageResultPreview(f)
		sb.WriteString(fmt.Sprintf("  %d. %s (%s)\n", i+1, preview, timeStr))
	}
	sb.WriteString("\nUse /imgresult or /imgresult <n> to inject into conversation")
	m.showSystemMessage(sb.String())
	return nil
}

// getImageResultPreview reads the first meaningful line of an image result file
func getImageResultPreview(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(unable to read)"
	}
	
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "(empty)"
	}
	
	// Get first non-empty line
	lines := strings.Split(content, "\n")
	var preview string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			preview = line
			break
		}
	}
	
	if preview == "" {
		return "(empty)"
	}
	
	// Truncate if too long
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	
	return preview
}

// injectImageResult reads the Mac describe-image result and injects it into the conversation as context
// If index is provided, uses that specific result (1-indexed), otherwise uses most recent
func (m *Model) injectImageResult(index int) tea.Cmd {
	files, err := getImageResultFiles()
	if err != nil || len(files) == 0 {
		m.showError("No image results found. Run describe-image on your Mac first.")
		return nil
	}
	
	// Convert to 0-indexed
	idx := index - 1
	if idx < 0 {
		idx = 0 // Default to most recent
	}
	if idx >= len(files) {
		m.showError(fmt.Sprintf("Invalid index %d. Only %d results available.", index, len(files)))
		return nil
	}
	
	resultFile := files[idx]
	data, err := os.ReadFile(resultFile)
	if err != nil {
		m.showError("Failed to read image result: " + err.Error())
		return nil
	}
	
	content := strings.TrimSpace(string(data))
	if content == "" {
		m.showError("Image result file is empty")
		return nil
	}
	
	// Format as context for the LLM
	contextText := "[Image description from local machine]:\n" + content
	
	// Add as user message to display
	userMsg := llm.Message{
		Role:    llm.MessageRoleUser,
		Content: []llm.Content{{Type: llm.ContentTypeText, Text: contextText}},
	}
	rendered := m.renderer.RenderMessage(userMsg, true)
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleUser,
		content: rendered,
	})
	m.updateViewportContent()
	
	// Initialize loop if needed and add to conversation context
	if m.loop == nil {
		if err := m.initLoop(); err != nil {
			m.showError("Failed to initialize: " + err.Error())
			return nil
		}
	}
	
	// Queue the message so it becomes part of the conversation context
	m.loop.QueueUserMessage(userMsg)
	
	m.showSystemMessage("Image description added to conversation context.")
	return nil
}
