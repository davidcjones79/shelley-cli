# Shelley CLI

A terminal interface for [Shelley](https://github.com/boldsoftware/shelley), the coding agent.

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
- **Image attachments** - Attach local images via `/attach` or use remote image analysis
- **Tab completion** - Complete file paths and slash commands
- **Prompt history** - Up/Down arrows cycle through previous prompts
- **Git integration** - View recent commits and diffs inline
- **Themes** - Dark and light themes available

## Command Line Flags

```
Usage: shelley chat [flags]

Flags:
  -conversation ID   Resume specific conversation by ID or slug
  -no-sync           Disable database sync (ephemeral conversation)
  -browser           Enable browser tools (screenshots, navigation, etc.)
  -verbose           Show tool execution details (commands, inputs, outputs)
  -yes               Auto-accept all tool operations (no confirmation prompts)
  -prompt TEXT       Send initial prompt and exit (for scripting/piping)
```

### Examples

```bash
# Basic interactive chat (syncs to database by default)
shelley chat

# Resume a specific conversation
shelley chat -conversation my-project

# Ephemeral conversation (not saved to database)
shelley chat -no-sync

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

### Conversations

Conversations sync to the database by default, so you can switch between CLI and web UI seamlessly.

| Command | Description |
|---------|-------------|
| `/conversations` | List recent conversations (numbered) |
| `/switch` | Switch to most recent conversation |
| `/switch <n>` | Switch by number from `/conversations` list |
| `/switch <id>` | Switch by conversation ID or slug |
| `/new` | Start a new conversation |
| `/search <query>` | Search conversations by content |
| `/rename <slug>` | Rename current conversation |
| `/archive` | Archive current conversation |
| `/archived` | List archived conversations |
| `/unarchive <id>` | Restore archived conversation |
| `/delete` | Delete current conversation |
| `/sync` | Reload messages from database (see below) |
| `/export [file]` | Export conversation to markdown |

### File Uploads

If you prefer working in the terminal but need a quick way to get files (screenshots, images, documents) from your local machine to your VM, use the built-in uploader:

```bash
shelley uploader
```

This starts a web server at `http://localhost:8000` (or `https://your-vm.exe.xyz:8000` on exe.dev). Open it in your browser, drag and drop files, and they're saved to `~/uploads/` on your VM.

| Command | Description |
|---------|-------------|
| `/uploads` | List files in ~/uploads |
| `/pick` | List recent uploads (numbered) |
| `/pick <n>` | Analyze file n with Shelley |

**Workflow:**
1. Run `shelley uploader` (or add `-port 8001` for a custom port)
2. Open the uploader URL in your browser
3. Drag and drop files (screenshots, CSVs, code, etc.)
4. In Shelley CLI, type `/pick` to see uploads
5. Type `/pick 1` to have Shelley analyze the most recent file

For images, `/pick` loads them directly as attachments. For text files (CSV, JSON, Markdown, code), Shelley reads and analyzes the content.

### Images

| Command | Description |
|---------|-------------|
| `/attach <path>` | Attach image to next message |
| `/image <path>` | Same as `/attach` |
| `/describe <path> [prompt]` | Analyze image with vision model |
| `/attachments` | List pending attachments |
| `/imglist` | List available image descriptions from remote analysis |
| `/imgresult [n]` | Inject image description into conversation |

**Note:** Drag-and-drop only works if you're running the CLI locally on your computer (not recommended for exe.dev workflows). When SSHed into a remote VM, you have these options for working with local images:

1. **Use the uploader** (recommended) - Run `shelley uploader`, drag files to the web page, then use `/pick` in the CLI. See [File Uploads](#file-uploads) above.

2. **Switch to web client** - The Shelley web UI supports drag-and-drop. Use `/conversations` to find your conversation ID, then open it in the web UI at the same path.

3. **Use the `describe-image` script** - Download from `scripts/describe-image` to your local Mac. It uploads images to your VM and analyzes them:
   ```bash
   # On your local machine:
   describe-image -v myvm screenshot.png "What's in this image?"
   ```
   Then in the CLI, use `/imglist` to see results and `/imgresult` to inject into your conversation.

4. **For images already on the VM** - Use `/attach /path/to/image.png` or include the path in brackets in your message: `[/path/to/image.png]`

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

Conversations sync to the database **by default**, enabling seamless switching between CLI and web UI.

### CLI ↔ Web UI Workflow

1. **Start in CLI, continue in web:**
   ```bash
   shelley chat
   # ... have a conversation ...
   # Check the conversation ID with /status or /conversations
   ```
   Then open the web UI (`shelley serve`) - your conversation is there.

2. **Start in web, continue in CLI:**
   ```bash
   # Find the conversation ID/slug in the web UI, then:
   shelley chat -conversation my-project
   ```

3. **Switch conversations from CLI:**
   ```
   /conversations     # list all
   /switch my-proj    # switch by slug or ID
   ```

### Syncing External Changes

If you have the same conversation open in multiple places (e.g., CLI and web UI, or two terminal sessions), use `/sync` to pull in messages added elsewhere:

```
/switch          # Switch to most recent conversation (or /switch <id>)
/sync            # Pull in any new messages
```

**Note:** You must `/switch` to a conversation before `/sync` will work. The CLI starts with no active conversation by default.

This reloads all messages from the database and updates the LLM's context, so it knows about the full conversation history. Useful when:

- You're chatting in the web UI and want to continue in terminal
- Another Shelley instance added messages to the same conversation
- You want to verify the conversation state matches the database

Aliases: `/refresh`, `/pull`

### Ephemeral Mode

For throwaway conversations that don't need to be saved:
```bash
shelley chat -no-sync
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
