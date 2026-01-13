package server

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"shelley.exe.dev/llm"
	"shelley.exe.dev/llm/imageutil"
)

// bracketedPathRegex matches paths enclosed in square brackets, e.g., [/path/to/image.png]
var bracketedPathRegex = regexp.MustCompile(`\[([^\[\]]+\.(png|jpg|jpeg|gif|webp))\]`)

// isImagePath checks if a path points to an image file based on extension
func isImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// ExtractImagesFromMessage extracts image paths from bracketed text in a message
// and returns the modified text (with image paths removed) and image contents.
// maxImageDimension controls resizing; if 0, no resizing is done.
func ExtractImagesFromMessage(text string, maxImageDimension int) (string, []llm.Content, error) {
	matches := bracketedPathRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil, nil
	}

	var images []llm.Content
	var result strings.Builder
	lastEnd := 0

	for _, match := range matches {
		// match[0]:match[1] is the full match including brackets
		// match[2]:match[3] is the captured path (without brackets)
		fullMatchStart, fullMatchEnd := match[0], match[1]
		pathStart, pathEnd := match[2], match[3]
		path := text[pathStart:pathEnd]

		// Check if the file exists and is readable
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// File doesn't exist, keep the original text
			continue
		}

		// Try to read the image
		imageData, err := os.ReadFile(path)
		if err != nil {
			// Can't read, keep original text
			continue
		}

		// Verify it's actually an image
		detectedType := http.DetectContentType(imageData)
		if !strings.HasPrefix(detectedType, "image/") {
			continue
		}

		// Resize if needed
		format := strings.TrimPrefix(detectedType, "image/")
		if maxImageDimension > 0 {
			resized, newFormat, _, err := imageutil.ResizeImage(imageData, maxImageDimension)
			if err == nil {
				imageData = resized
				format = newFormat
			}
		}

		// Add text before this match
		result.WriteString(text[lastEnd:fullMatchStart])
		lastEnd = fullMatchEnd

		// Create image content
		base64Data := base64.StdEncoding.EncodeToString(imageData)
		images = append(images, llm.Content{
			Type:      llm.ContentTypeText, // Uses Text type with MediaType to indicate image
			MediaType: "image/" + format,
			Data:      base64Data,
		})
	}

	// Add any remaining text after the last match
	result.WriteString(text[lastEnd:])

	return strings.TrimSpace(result.String()), images, nil
}
