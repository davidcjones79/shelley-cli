// Package memory provides persistent memory storage for Shelley CLI.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Entry represents a single memory entry.
type Entry struct {
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Store holds all memory entries.
type Store struct {
	Entries []Entry `json:"entries"`
}

// memoryPath returns the path to the memory file.
func memoryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".shelley")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "memory.json"), nil
}

// Load reads the memory store from disk.
func Load() (*Store, error) {
	path, err := memoryPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{}, nil
	}
	if err != nil {
		return nil, err
	}

	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	return &store, nil
}

// Save writes the memory store to disk.
func (s *Store) Save() error {
	path, err := memoryPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Add adds a new memory entry.
func (s *Store) Add(text string) {
	s.Entries = append(s.Entries, Entry{
		Text:      text,
		CreatedAt: time.Now(),
	})
}

// Remove removes a memory entry by index (0-based).
func (s *Store) Remove(index int) error {
	if index < 0 || index >= len(s.Entries) {
		return fmt.Errorf("index %d out of range (0-%d)", index, len(s.Entries)-1)
	}
	s.Entries = append(s.Entries[:index], s.Entries[index+1:]...)
	return nil
}

// ForSystemPrompt returns memory entries formatted for inclusion in system prompt.
func (s *Store) ForSystemPrompt() string {
	if len(s.Entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n<user_memory>\nThe user has stored the following facts to remember across sessions:\n")
	for _, entry := range s.Entries {
		sb.WriteString("- ")
		sb.WriteString(entry.Text)
		sb.WriteString("\n")
	}
	sb.WriteString("</user_memory>")
	return sb.String()
}
