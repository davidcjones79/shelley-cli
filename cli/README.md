# Shelley CLI

An unofficial terminal interface for Shelley, the coding agent.

## Quick Start

```bash
shelley chat
```

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
| PgUp/PgDown | Scroll message history |
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

By default, mouse mode is off to allow text selection in your terminal.
Use `/mouse` to toggle mouse scrolling (disables text selection).
