# Shelley CLI

An unofficial terminal interface for Shelley, the coding agent.

## Installation

### On exe.dev VMs (Recommended)

Shelley CLI automatically uses the exe.dev LLM gateway - no API keys needed.

```bash
# Clone and build
git clone https://github.com/davidcjones79/shelley-cli.git
cd shelley-cli
make

# Create config file
mkdir -p ~/.config/shelley
cat > ~/.config/shelley/shelley.json << 'EOF'
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
EOF

# Add alias to your shell (optional but recommended)
echo 'alias shelley="~/shelley-cli/shelley --config ~/.config/shelley/shelley.json"' >> ~/.bashrc
source ~/.bashrc

# Run it
shelley chat
```

### On non-exe.dev machines

You'll need your own API keys from [Anthropic](https://console.anthropic.com/) or [OpenAI](https://platform.openai.com/).

```bash
# Clone and build
git clone https://github.com/davidcjones79/shelley-cli.git
cd shelley-cli
make

# Set your API key (Anthropic example)
export ANTHROPIC_API_KEY="your-api-key-here"

# Or for OpenAI
export OPENAI_API_KEY="your-api-key-here"

# Run it
./shelley chat
```

You can also add the API key to your `~/.bashrc` to persist it across sessions.

## Features

- **Streaming responses** - See text as it's generated
- **Conversation sync** - Share conversations with the web UI via SQLite
- **Multi-model support** - Switch between Claude, GPT, and open source models
- **Image attachments** - Attach screenshots or drag-drop images
- **Tab completion** - Complete file paths and commands
- **Prompt history** - Use Up/Down arrows to cycle through previous prompts

## Flags

| Flag | Description |
|------|-------------|
| `-sync` | Sync conversations with database (enables `/conversations`, `/switch`) |
| `-conversation <id>` | Resume specific conversation by ID or slug (requires `-sync`) |
| `-browser` | Enable browser tools (screenshots, navigation) |
| `-verbose` | Show tool execution details |
| `-yes` | Auto-accept all tool operations (no confirmations) |
| `-prompt <text>` | Send initial prompt (for non-interactive/piped usage) |

## Commands

Type `/help` in the CLI to see all commands. Here are the highlights:

### General

| Command | Description |
|---------|-------------|
| `/help` | Show all commands |
| `/clear` | Clear conversation display |
| `/stop` | Cancel current operation (or press Escape) |
| `/quit` | Exit the CLI (also `/exit`, `/q`) |
| `/verbose` | Toggle tool detail visibility |
| `/status` | Show session status |

### Models

| Command | Description |
|---------|-------------|
| `/models` | List available models |
| `/model <id>` | Switch to a different model |
| `/fast` | Switch to Haiku (cheap & fast) |
| `/smart` | Switch to Sonnet (balanced) |
| `/think` | Switch to Opus (complex reasoning) |
| `/context` | Show context window usage |
| `/usage` | Show token usage and estimated cost |

### Conversations (requires `-sync`)

| Command | Description |
|---------|-------------|
| `/conversations` | List recent conversations |
| `/switch <id>` | Switch to conversation by ID or slug |
| `/new` | Start a new conversation |
| `/search <query>` | Search conversations by content |
| `/rename <slug>` | Rename current conversation |
| `/archive` | Archive current conversation |
| `/delete` | Delete current conversation |
| `/export [file]` | Export conversation to markdown |

### Images

| Command | Description |
|---------|-------------|
| `/attach <path>` | Attach image to next message |
| `/attachments` | List pending attachments |

You can also drag-drop image files into the terminal, or paste file paths.

### Git

| Command | Description |
|---------|-------------|
| `/git` | List recent commits |
| `/git show <id>` | Show files in commit |
| `/git diff <file>` | Show diff for file |

### Navigation

| Key | Action |
|-----|--------|
| Enter | Send message |
| Escape | Cancel current operation |
| Tab | Complete file paths and commands |
| Up/Down | Cycle through prompt history |
| Ctrl+U/D | Scroll message history (half-page) |
| PgUp/PgDown | Scroll message history (full page) |
| Ctrl+C | Quit |

## Examples

### Basic chat

```bash
shelley chat
```

### With conversation sync (share with web UI)

```bash
shelley chat -sync
```

### Resume a conversation

```bash
shelley chat -sync -conversation my-project
```

### With browser tools enabled

```bash
shelley chat -browser
```

### Non-interactive mode (pipe input)

```bash
echo "explain this code" | shelley chat -prompt "$(cat main.go)"
```

## Themes

Use `/theme dark` or `/theme light` to switch. The default is dark.

## Mouse Mode

By default, mouse mode is on for in-app scrolling. Use `/mouse` to toggle it off if you need terminal text selection.

**Tip:** In iTerm2, hold **Option (⌥)** while clicking/dragging to select text even with mouse mode on.

## Background

This CLI was built by someone who isn't a software engineer by trade, with significant help from AI coding assistants (including Shelley itself). It started as an experiment to see if a terminal interface could be added to the existing [Shelley](https://github.com/boldsoftware/shelley) codebase.

The process involved:

1. **Forking Shelley** - Starting with the existing web-based agent architecture
2. **Learning the codebase** - Understanding how the `loop/`, `llm/`, and `db/` packages work together
3. **Building with AI assistance** - Using Claude and Shelley to help write the CLI, debug edge cases, and refactor code
4. **Iterating on UX** - Adding features like streaming responses, image drag-drop, and conversation sync based on actual usage

The CLI is marked as "unofficial" because it's a community contribution, not part of the official Shelley project. The code has been refactored to minimize merge conflicts with upstream, so it should be possible to pull in future Shelley updates.

If you're interested in contributing or have questions about how something works, feel free to explore the code. The main files are:

- `cli.go` - Core TUI (Bubble Tea model, init, update, view)
- `commands.go` - Slash command handling
- `models.go` - Model switching, context, usage tracking
- `conversations.go` - Database conversation management
- `sessions.go` - Legacy JSON session save/load
- `message.go` - Message rendering with glamour
- `styles.go` - Theme definitions

Built for the [exe.dev](https://exe.dev) community. 🚀
