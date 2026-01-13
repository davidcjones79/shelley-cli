package cli

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// imagePathRegex matches:
// 1. Bracketed paths: [/path/to/image.png]
// 2. Bare paths that start with / or ~/ and end with an image extension
var imagePathRegex = regexp.MustCompile(`(?:\[([^\[\]]+\.(?:png|jpg|jpeg|gif|webp))\]|(?:^|\s)((?:/|~/)[^\s]+\.(?:png|jpg|jpeg|gif|webp)))`)

// extractImagePathsFromText extracts image file paths from text.
// Returns the cleaned text (with paths removed) and a list of valid image paths.
func extractImagePathsFromText(text, workingDir string) (cleanedText string, imagePaths []string) {
	matches := imagePathRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var result strings.Builder
	lastEnd := 0

	for _, match := range matches {
		// match[0]:match[1] is the full match
		// match[2]:match[3] is group 1 (bracketed path, may be -1 if not matched)
		// match[4]:match[5] is group 2 (bare path, may be -1 if not matched)
		
		var path string
		var fullStart, fullEnd int
		
		if match[2] != -1 {
			// Bracketed path
			path = text[match[2]:match[3]]
			fullStart, fullEnd = match[0], match[1]
		} else if match[4] != -1 {
			// Bare path
			path = text[match[4]:match[5]]
			fullStart, fullEnd = match[4], match[5]
			// Include any leading whitespace in removal
			if match[0] < match[4] {
				fullStart = match[0]
			}
		} else {
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

// loadImageAsAttachment loads an image file and returns it as an attachment
func loadImageAsAttachment(path string) (*imageAttachment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Detect media type
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/png" // fallback
	}

	return &imageAttachment{
		path:      path,
		mediaType: contentType,
		data:      base64.StdEncoding.EncodeToString(data),
	}, nil
}
