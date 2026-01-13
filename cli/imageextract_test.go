package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractImagePathsFromText(t *testing.T) {
	// Create a test image file
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	// A minimal valid PNG (1x1 red pixel)
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, // IHDR length
		0x49, 0x48, 0x44, 0x52, // IHDR
		0x00, 0x00, 0x00, 0x01, // width = 1
		0x00, 0x00, 0x00, 0x01, // height = 1
		0x08, 0x02, // bit depth = 8, color type = RGB
		0x00, 0x00, 0x00, // compression, filter, interlace
		0x90, 0x77, 0x53, 0xDE, // IHDR CRC
		0x00, 0x00, 0x00, 0x0C, // IDAT length
		0x49, 0x44, 0x41, 0x54, // IDAT
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00, // compressed data
		0x1B, 0xB6, 0xEE, 0x56, // IDAT CRC
		0x00, 0x00, 0x00, 0x00, // IEND length
		0x49, 0x45, 0x4E, 0x44, // IEND
		0xAE, 0x42, 0x60, 0x82, // IEND CRC
	}
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		input          string
		wantText       string
		wantImageCount int
	}{
		{
			name:           "no images",
			input:          "Hello world",
			wantText:       "Hello world",
			wantImageCount: 0,
		},
		{
			name:           "bracketed path only",
			input:          "[" + imgPath + "]",
			wantText:       "",
			wantImageCount: 1,
		},
		{
			name:           "text with bracketed image",
			input:          "Check this out: [" + imgPath + "]",
			wantText:       "Check this out:",
			wantImageCount: 1,
		},
		{
			name:           "bare absolute path",
			input:          "Look at " + imgPath,
			wantText:       "Look at",
			wantImageCount: 1,
		},
		{
			name:           "nonexistent image",
			input:          "Look at [/nonexistent/image.png]",
			wantText:       "Look at [/nonexistent/image.png]",
			wantImageCount: 0,
		},
		{
			name:           "non-image extension",
			input:          "File: [/some/file.txt]",
			wantText:       "File: [/some/file.txt]",
			wantImageCount: 0,
		},
		{
			name:           "single-quoted path",
			input:          "Check this: '" + imgPath + "'",
			wantText:       "Check this:",
			wantImageCount: 1,
		},
		{
			name:           "double-quoted path",
			input:          "Look at \"" + imgPath + "\"",
			wantText:       "Look at",
			wantImageCount: 1,
		},
		{
			name:           "file URL",
			input:          "See file://" + imgPath,
			wantText:       "See",
			wantImageCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotPaths := extractImagePathsFromText(tt.input, tmpDir)
			if gotText != tt.wantText {
				t.Errorf("got text = %q, want %q", gotText, tt.wantText)
			}
			if len(gotPaths) != tt.wantImageCount {
				t.Errorf("got %d images, want %d", len(gotPaths), tt.wantImageCount)
			}
		})
	}
}

func TestLoadImageAsAttachment(t *testing.T) {
	// Create a test image file
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x01,
		0x08, 0x02,
		0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xDE,
		0x00, 0x00, 0x00, 0x0C,
		0x49, 0x44, 0x41, 0x54,
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x01, 0x01, 0x01, 0x00,
		0x1B, 0xB6, 0xEE, 0x56,
		0x00, 0x00, 0x00, 0x00,
		0x49, 0x45, 0x4E, 0x44,
		0xAE, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(imgPath, pngData, 0o644); err != nil {
		t.Fatal(err)
	}

	att, err := loadImageAsAttachment(imgPath)
	if err != nil {
		t.Fatalf("loadImageAsAttachment() error = %v", err)
	}

	if att.mediaType != "image/png" {
		t.Errorf("got mediaType = %q, want %q", att.mediaType, "image/png")
	}

	if att.data == "" {
		t.Error("data should not be empty")
	}

	if att.path != imgPath {
		t.Errorf("got path = %q, want %q", att.path, imgPath)
	}
}
