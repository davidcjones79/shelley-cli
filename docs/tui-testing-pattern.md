# Making Bubbletea TUIs Testable by AI Agents

When building Go TUI applications with [bubbletea](https://github.com/charmbracelet/bubbletea), you can make them testable by Shelley (or other AI agents) by adding a non-interactive mode.

## The Problem

TUI programs expect:
- A real terminal (TTY)
- Interactive input/output
- Escape sequences for rendering

AI agents run commands non-interactively, so raw TUI rendering breaks.

## The Solution

Add flags that bypass the TUI for testing:

```go
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Add flags for non-interactive/test mode
	testCmd := flag.String("cmd", "", "Run command non-interactively (for testing)")
	dumpState := flag.Bool("dump-state", false, "Dump model state as JSON and exit")
	flag.Parse()

	// Initialize your model
	model := NewModel()

	// Non-interactive mode: process command and exit
	if *testCmd != "" {
		result := model.ProcessCommand(*testCmd)
		fmt.Println(result)
		os.Exit(0)
	}

	// Dump state mode: useful for AI to inspect current state
	if *dumpState {
		fmt.Println(model.ToJSON())
		os.Exit(0)
	}

	// Normal interactive TUI mode
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Model is your bubbletea model
type Model struct {
	items    []string
	selected int
}

func NewModel() *Model {
	return &Model{
		items: []string{"item1", "item2", "item3"},
	}
}

// ProcessCommand handles non-interactive commands
// This is what Shelley calls when testing
func (m *Model) ProcessCommand(cmd string) string {
	switch cmd {
	case "list":
		return fmt.Sprintf("Items: %v", m.items)
	case "count":
		return fmt.Sprintf("Count: %d", len(m.items))
	case "add:foo":
		m.items = append(m.items, "foo")
		return "Added foo"
	default:
		return fmt.Sprintf("Unknown command: %s", cmd)
	}
}

// ToJSON dumps state for AI inspection
func (m *Model) ToJSON() string {
	return fmt.Sprintf(`{"items":%q,"selected":%d}`, m.items, m.selected)
}

// ... rest of your normal bubbletea Init/Update/View methods ...
```

## Usage

Then Shelley (or any AI agent) can test your TUI:

```bash
# Run a command non-interactively
./my-tui -cmd "list"

# Inspect current state
./my-tui -dump-state

# Modify state
./my-tui -cmd "add:newitem"
```

## Real Example

[Shelley CLI](https://github.com/davidcjones79/shelley-cli) uses this exact pattern with the `-prompt` flag:

```bash
# Interactive mode (full TUI)
shelley chat

# Non-interactive mode (for scripting/testing)
shelley chat -prompt "What is 2+2?"
```

See [cmd/shelley/main.go](https://github.com/davidcjones79/shelley-cli/blob/shelley-cli-test/cmd/shelley/main.go) for the implementation.

## Tips

1. **Keep commands simple** - Use a consistent format like `action:arg`
2. **Return structured output** - JSON is easy for AI to parse
3. **Add a help command** - `./my-tui -cmd "help"` lists available commands
4. **Test the non-interactive mode** - Write unit tests for `ProcessCommand()`
