package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// TerminalImageProtocol represents the image display protocol supported by the terminal
type TerminalImageProtocol int

const (
	TerminalImageNone   TerminalImageProtocol = iota // No inline image support
	TerminalImageITerm2                              // iTerm2 inline images protocol
	TerminalImageKitty                               // Kitty graphics protocol
)

// DetectTerminalImageSupport detects which image protocol the terminal supports
func DetectTerminalImageSupport() TerminalImageProtocol {
	// Check for Kitty first (most feature-rich)
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return TerminalImageKitty
	}

	// Check for iTerm2 protocol support
	// Many terminals support this: iTerm2, WezTerm, Mintty, VSCode, Hyper, etc.
	termProgram := os.Getenv("TERM_PROGRAM")
	switch termProgram {
	case "iTerm.app", "WezTerm", "vscode", "Hyper":
		return TerminalImageITerm2
	}

	// Check LC_TERMINAL for iTerm2 (sometimes TERM_PROGRAM isn't set)
	if os.Getenv("LC_TERMINAL") == "iTerm2" {
		return TerminalImageITerm2
	}

	// Check for WezTerm via its specific env var
	if os.Getenv("WEZTERM_PANE") != "" {
		return TerminalImageITerm2
	}

	// Check TERM for terminals known to support sixel/iTerm2
	term := os.Getenv("TERM")
	if strings.Contains(term, "mintty") || strings.Contains(term, "mlterm") {
		return TerminalImageITerm2
	}

	return TerminalImageNone
}

// RenderInlineImage returns escape sequences to display an image inline in the terminal.
// Returns empty string if the terminal doesn't support inline images.
// The base64Data should be the raw base64-encoded image data.
// maxWidth and maxHeight limit the display size (0 = no limit).
func RenderInlineImage(base64Data string, maxWidth, maxHeight int, protocol TerminalImageProtocol) string {
	switch protocol {
	case TerminalImageITerm2:
		return renderITerm2Image(base64Data, maxWidth, maxHeight)
	case TerminalImageKitty:
		return renderKittyImage(base64Data, maxWidth, maxHeight)
	default:
		return ""
	}
}

// renderITerm2Image renders an image using the iTerm2 inline images protocol.
// Protocol: ESC ] 1337 ; File = [arguments] : base64-data BEL
// Supported by: iTerm2, WezTerm, Mintty, VSCode terminal, Hyper, and others.
func renderITerm2Image(base64Data string, maxWidth, maxHeight int) string {
	var args []string
	args = append(args, "inline=1") // Display inline

	if maxWidth > 0 {
		args = append(args, fmt.Sprintf("width=%d", maxWidth))
	}
	if maxHeight > 0 {
		args = append(args, fmt.Sprintf("height=%d", maxHeight))
	}

	// Preserve aspect ratio
	args = append(args, "preserveAspectRatio=1")

	// Build the escape sequence
	// OSC 1337 ; File = args : base64data ST
	// OSC = ESC ]
	// ST = ESC \ or BEL (\a)
	return fmt.Sprintf("\033]1337;File=%s:%s\a", strings.Join(args, ";"), base64Data)
}

// renderKittyImage renders an image using the Kitty graphics protocol.
// This is more complex but provides better quality and more features.
func renderKittyImage(base64Data string, maxWidth, maxHeight int) string {
	// Kitty protocol sends image in chunks
	// Format: ESC_G<control data>;<payload>ESC\
	// where ESC_G is \033_G and ESC\ is \033\\

	const chunkSize = 4096

	var result strings.Builder

	// Calculate total chunks needed
	totalLen := len(base64Data)
	
	for i := 0; i < totalLen; i += chunkSize {
		end := i + chunkSize
		if end > totalLen {
			end = totalLen
		}
		chunk := base64Data[i:end]
		isFirst := i == 0
		isLast := end >= totalLen

		var control string
		if isFirst {
			// First chunk includes format info
			// a=T means transmit and display
			// f=100 means PNG/JPEG/GIF (auto-detect)
			// t=d means direct transmission (data follows)
			// m=1 means more chunks follow (unless last)
			if isLast {
				control = "a=T,f=100,t=d"
			} else {
				control = "a=T,f=100,t=d,m=1"
			}

			// Add size constraints if specified
			if maxWidth > 0 || maxHeight > 0 {
				// c = columns, r = rows (in cells)
				// We estimate ~10 pixels per cell
				if maxWidth > 0 {
					cols := maxWidth / 10
					if cols < 1 {
						cols = 1
					}
					control += fmt.Sprintf(",c=%d", cols)
				}
				if maxHeight > 0 {
					rows := maxHeight / 10
					if rows < 1 {
						rows = 1
					}
					control += fmt.Sprintf(",r=%d", rows)
				}
			}
		} else if isLast {
			// Last chunk (not first)
			control = "m=0"
		} else {
			// Middle chunk
			control = "m=1"
		}

		result.WriteString(fmt.Sprintf("\033_G%s;%s\033\\", control, chunk))
	}

	// Add newline after image
	result.WriteString("\n")

	return result.String()
}

// RenderInlineImageFromFile reads a file and renders it as an inline image.
// Returns empty string if unsupported or on error.
func RenderInlineImageFromFile(path string, maxWidth, maxHeight int, protocol TerminalImageProtocol) string {
	if protocol == TerminalImageNone {
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return RenderInlineImage(base64Data, maxWidth, maxHeight, protocol)
}
