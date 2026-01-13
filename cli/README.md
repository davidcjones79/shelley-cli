# Shelley CLI

A terminal interface for [Shelley](https://github.com/boldsoftware/shelley), the coding agent.

![Shelley CLI Demo](https://github.com/davidcjones79/shelley-cli/raw/shelley-cli-test/cli/demo.gif)

## Quick Start (exe.dev)

On exe.dev VMs, Shelley CLI works out of the box with the built-in LLM gateway - no API keys needed.

```bash
# Clone and build
git clone https://github.com/davidcjones79/shelley-cli.git
cd shelley-cli
git checkout shelley-cli-test
make

# Create config file
mkdir -p ~/.config/shelley
cat > ~/.config/shelley/shelley.json << 'EOF'
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
EOF

# Add alias (recommended)
echo 'alias shelley="~/shelley-cli/bin/shelley --config ~/.config/shelley/shelley.json"' >> ~/.bashrc
source ~/.bashrc

# Run it
shelley chat
```

## Installation (non-exe.dev)

You'll need Go 1.21+ and Node.js 18+ installed, plus API keys from [Anthropic](https://console.anthropic.com/) or [OpenAI](https://platform.openai.com/).

```bash
# Clone and build
git clone https://github.com/davidcjones79/shelley-cli.git
cd shelley-cli
git checkout shelley-cli-test
make

# Set your API key
export ANTHROPIC_API_KEY="your-api-key-here"
# Or for OpenAI:
# export OPENAI_API_KEY="your-api-key-here"

# Run it
./bin/shelley chat
```

Add the export to your `~/.bashrc` to persist across sessions.

## Features

- **Streaming responses** - See text as it's generated, token by token
- **Tool execution** - Runs bash commands, edits files, takes screenshots
- **Conversation sync** - Share conversations with the Shelley web UI via SQLite
- **Multi-model support** - Switch between Claude, GPT, and open source models on the fly
- **Image attachments** - Drag-drop images into terminal or use `/attach`
- **Tab completion** - Complete file paths and slash commands
- **Prompt history** - Up/Down arrows cycle through previous prompts
- **Git integration** - View recent commits and diffs inline
- **Themes** - Dark and light themes available

## Command Line Flags

```
Usage: shelley chat [flags]

Flags:
  -sync              Sync conversations with database (enables /conversations, /switch)
  -conversation ID   Resume specific conversation by ID or slug (requires -sync)
  -browser           Enable browser tools (screenshots, navigation, etc.)
  -verbose           Show tool execution details (commands, inputs, outputs)
  -yes               Auto-accept all tool operations (no confirmation prompts)
  -prompt TEXT       Send initial prompt and exit (for scripting/piping)
```

### Examples

```bash
# Basic interactive chat
shelley chat

# With conversation persistence (syncs to web UI)
shelley chat -sync

# Resume a specific conversation
shelley chat -sync -conversation my-project

# See all tool details
shelley chat -verbose

# No confirmation prompts (use with caution)
shelley chat -yes

# Enable browser automation (requires Chrome/Chromium)
shelley chat -browser

# Non-interactive: send one prompt and exit
shelley chat -prompt "What is 2+2?"

# Pipe a file for analysis
cat main.go | shelley chat -prompt "Explain this code:"
```

## Keyboard Shortcuts

### Input

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Ctrl+J` | Insert newline (for multi-line input) |
| `Escape` | Clear input / Cancel current operation |
| `Tab` | Complete file paths and commands |
| `Up` / `Down` | Cycle through prompt history |

### Scrolling

| Key | Action |
|-----|--------|
| `Ctrl+U` | Scroll up half page |
| `Ctrl+D` | Scroll down half page |
| `PgUp` | Scroll up full page |
| `PgDown` | Scroll down full page |
| `Home` | Scroll to top |
| `End` | Scroll to bottom |
| Mouse wheel | Scroll (when mouse mode enabled) |

### Control

| Key | Action |
|-----|--------|
| `Ctrl+C` | Quit |
| `Escape` | Cancel current operation (while processing) |

## Slash Commands

Type `/help` in the CLI to see all available commands.

### General

| Command | Description |
|---------|-------------|
| `/help` | Show all commands |
| `/clear` | Clear conversation display |
| `/stop` | Cancel current operation |
| `/quit` | Exit the CLI (also `/exit`, `/q`) |
| `/verbose` | Toggle tool detail visibility |
| `/status` | Show session status (model, tokens, working dir) |
| `/mouse` | Toggle mouse mode on/off |
| `/theme <dark\|light>` | Switch color theme |
| `/cwd` or `/cd <path>` | Show or change working directory |

### Models

| Command | Description |
|---------|-------------|
| `/models` | List all available models |
| `/model <id>` | Switch to a specific model |
| `/fast` | Switch to Haiku (cheap & fast) |
| `/smart` | Switch to Sonnet (balanced) |
| `/think` | Switch to Opus (complex reasoning) |
| `/context` | Show context window usage |
| `/usage` | Show token usage and estimated cost |

### Conversations (requires `-sync` flag)

| Command | Description |
|---------|-------------|
| `/conversations` | List recent conversations |
| `/switch <id>` | Switch to conversation by ID or slug |
| `/new` | Start a new conversation |
| `/search <query>` | Search conversations by content |
| `/rename <slug>` | Rename current conversation |
| `/archive` | Archive current conversation |
| `/archived` | List archived conversations |
| `/unarchive <id>` | Restore archived conversation |
| `/delete` | Delete current conversation |
| `/export [file]` | Export conversation to markdown |

### Images

| Command | Description |
|---------|-------------|
| `/attach <path>` | Attach image to next message |
| `/image <path>` | Same as `/attach` |
| `/attachments` | List pending attachments |

You can also:
- Drag-drop image files into the terminal
- Paste file paths directly in your message
- Use bracketed paths like `[/path/to/image.png]`

Supported formats: PNG, JPG, JPEG, GIF, WEBP

### Git

| Command | Description |
|---------|-------------|
| `/git` | List recent commits (last 10) |
| `/git show <id>` | Show files changed in a commit |
| `/git diff <file>` | Show colorized diff for a file |

### Legacy Sessions (JSON-based, deprecated)

| Command | Description |
|---------|-------------|
| `/save <name>` | Save session to JSON file |
| `/load <name>` | Load session from JSON file |
| `/sessions` | List saved sessions |

## Tool Confirmation

By default, Shelley asks for confirmation before running tools that could modify your system:

```
🔧 Execute bash command?
   echo "hello world"

   [y]es  [n]o  [a]lways
```

- Press `y` or `Enter` to allow once
- Press `n` to deny
- Press `a` to allow all future operations this session

Use `-yes` flag to skip all confirmations (for trusted automation).

## Conversation Sync

With the `-sync` flag, conversations are stored in SQLite and can be:

1. **Continued later** - Use `/switch` to resume any conversation
2. **Shared with web UI** - The same database is used by `shelley serve`
3. **Searched** - Use `/search` to find conversations by content

```bash
# Start a synced conversation
shelley chat -sync

# Later, resume it
shelley chat -sync -conversation my-project-name

# Or switch from within the CLI
/conversations     # list all
/switch my-proj    # switch by slug
```

## Available Models

Models vary by provider and configuration. On exe.dev:

| Provider | Models |
|----------|--------|
| Anthropic | claude-opus-4.5, claude-sonnet-4.5, claude-haiku-4.5 |
| OpenAI | gpt-5, gpt-5-nano, gpt-5.1-codex |
| Fireworks | qwen3-coder-fireworks, glm-4p6-fireworks |

Use `/models` to see what's available in your environment.

## Tips

### Multi-line Input

Press `Ctrl+J` to insert a newline. The input area expands automatically (up to 10 lines).

### Text Selection

Mouse mode is on by default for scrolling. To select text:
- Toggle mouse mode off: `/mouse`
- Or in iTerm2: hold `Option (⌥)` while selecting

### Working Directory

Shelley operates in your current working directory. Use `/cwd` to check it, or `/cd <path>` to change.

### Verbose Mode

Use `/verbose` or start with `-verbose` to see:
- Full bash commands being executed
- Tool inputs and outputs
- File paths being modified

### Cost Tracking

Use `/usage` to see:
- Total tokens consumed (input + output)
- Estimated cost in USD
- Breakdown by input vs output tokens

## Troubleshooting

### "No models available"

Check your API key is set correctly:
```bash
echo $ANTHROPIC_API_KEY  # Should show your key
```

Or on exe.dev, ensure your config points to the gateway:
```bash
cat ~/.config/shelley/shelley.json
```

### "Browser tools not available"

Install Chrome or Chromium:
```bash
# Ubuntu/Debian
sudo apt install chromium-browser

# macOS
brew install --cask chromium
```

### Slow startup

First run may be slow as Go compiles. Subsequent runs use the cached binary in `bin/`.

### UI glitches

Try resizing your terminal or pressing `Ctrl+L` to redraw.

## Architecture

The CLI is built on top of the existing Shelley codebase:

```
cli/
├── cli.go           # Core TUI (Bubble Tea model, init, update, view)
├── commands.go      # Slash command parsing and execution
├── models.go        # Model switching, context tracking, usage
├── conversations.go # Database conversation management
├── sessions.go      # Legacy JSON session save/load
├── message.go       # Message rendering with glamour markdown
├── styles.go        # Theme definitions (colors, borders)
├── completion.go    # Tab completion for paths and commands
├── git.go           # Git log and diff rendering
├── imageextract.go  # Image path detection and loading
├── export.go        # Markdown export functionality
└── utils.go         # Shared utilities
```

The CLI reuses:
- `llm/` - LLM provider integrations (Anthropic, OpenAI, etc.)
- `loop/` - Conversation loop and tool execution
- `claudetool/` - Tool implementations (bash, patch, browser, etc.)
- `db/` - SQLite conversation storage

## Contributing

This is a community fork. Issues and PRs welcome at [davidcjones79/shelley-cli](https://github.com/davidcjones79/shelley-cli).

To run tests:
```bash
go test ./cli/...     # CLI unit tests
make test-go          # All Go tests
```

## Credits

Built on top of [Shelley](https://github.com/boldsoftware/shelley) by Bold Software.

This CLI was built with significant help from AI coding assistants (including Shelley itself). It started as an experiment to see if a terminal interface could complement the existing web UI.

Built for the [exe.dev](https://exe.dev) community. 🚀
