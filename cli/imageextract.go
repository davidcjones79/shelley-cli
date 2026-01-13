package cli

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

	// Detect media type
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/png" // fallback
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

// showLastImageResult displays the result from the Mac describe-image script
func (m *Model) showLastImageResult() tea.Cmd {
	resultFile := "/tmp/shelley-image-result.txt"
	
	data, err := os.ReadFile(resultFile)
	if err != nil {
		m.showError("No image result found. Run describe-image on your Mac first.")
		return nil
	}
	
	content := strings.TrimSpace(string(data))
	if content == "" {
		m.showError("Image result file is empty")
		return nil
	}
	
	// Render as assistant message
	responseMsg := llm.Message{
		Role:    llm.MessageRoleAssistant,
		Content: []llm.Content{{Type: llm.ContentTypeText, Text: content}},
	}
	rendered := m.renderer.RenderMessage(responseMsg, false)
	m.messages = append(m.messages, renderedMessage{
		role:    llm.MessageRoleAssistant,
		content: rendered,
	})
	m.updateViewportContent()
	return nil
}
