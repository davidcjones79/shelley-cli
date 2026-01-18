# Shelley CLI

> A terminal interface for [Shelley](https://github.com/boldsoftware/shelley), the coding agent. This fork adds CLI, Igor file transfer, Coordinator for distributed tasks, and `/pick` workflow.

## Quick Install (exe.dev)

```bash
curl -fsSL https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh | bash
source ~/.bashrc
shelley chat
```

This installs the CLI with Igor file transfer service. See [cli/README.md](cli/README.md) for full documentation.

## What's Added in This Fork

- **CLI Interface** - Full terminal UI with streaming, tool execution, conversation sync. Tested with:
  - [iTerm2](https://iterm2.com/downloads.html) (Mac) - works best, most tested
  - [Windows Terminal](https://aka.ms/terminal) (Windows)
  - [Alacritty](https://alacritty.org/) (Windows/Mac)
  - Mac built-in Terminal - works, but not as well as the others. If mouse scrolling is enabled, selecting text is difficult without first disabling it via `/mouse`
- **Igor** - Your faithful laboratory assistant for file transfers (`shelley igor`)
- **Coordinator & Dashboard** - Distributed task queue with worker pool for parallelizing Shelley across multiple VMs. Includes shared filesystem via Tailscale (`shelley dashboard` or `shelley coord`) - see [coordinator/README.md](coordinator/README.md)
- **Task Groups** - Batch multiple related tasks together with shared repo/branch settings
- **Git Integration** - Workers can clone repos, make changes, commit, and push to feature branches
- **`/pick` Command** - Quick workflow to analyze uploaded files
- **Systemd Service** - Igor runs persistently at `https://your-vm.exe.xyz:8099/`

## Commands

| Command | Description |
|---------|-------------|
| `shelley chat` | Interactive CLI chat mode |
| `shelley serve` | Start the web server |
| `shelley dashboard` | Start dashboard with coordinator management UI |
| `shelley coord` | Start coordinator server (headless) |
| `shelley coord-cli` | Manage coordinator from command line |
| `shelley watch` | Live CLI dashboard for coordinator |
| `shelley igor` | Start Igor file transfer server |
| `shelley status` | Show status of all Shelley services |
| `shelley unpack-template` | Unpack a project template |
| `shelley version` | Print version information as JSON |

Use `shelley <command> -h` for command-specific help.

### Coordinator CLI (`shelley coord-cli`)

Manage the coordinator programmatically without the web dashboard:

```bash
# Add a single task
shelley coord-cli add-task "Create a landing page for Docker"

# Create a task group with multiple parallel tasks
shelley coord-cli add-group "Landing Pages" \
  "Create docker.html" \| "Create k8s.html" \| "Create terraform.html"

# Scale workers and monitor
shelley coord-cli scale 3
shelley coord-cli stats
shelley coord-cli workers
shelley coord-cli groups

# Live dashboard (auto-refreshes)
shelley watch
```

See [docs/CLI_REFERENCE.md](docs/CLI_REFERENCE.md) for complete documentation.

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

---

# About Shelley

Shelley is a mobile-friendly, web-based, multi-conversation, multi-modal,
multi-model, single-user coding agent built for [exe.dev](https://exe.dev/).

For the original web UI version, see [boldsoftware/shelley](https://github.com/boldsoftware/shelley).

## Build from Source

You'll need Go and Node.

```bash
git clone https://github.com/davidcjones79/shelley-cli.git
cd shelley-cli
make
```



# Architecture 

The technical stack is Go for the backend, SQLite for storage, and Typescript
and React for the UI. 

The data model is that Conversations have Messages, which might be from the
user, the model, the tools, or the harness. All of that is stored in the
database, and we use a SSE endpoint to keep the UI updated. 

# History

Shelley is partially based on our previous coding agent effort, [Sketch](https://github.com/boldsoftware/sketch). 

Unsurprisingly, much of Shelley is written by Shelley, Sketch, Claude Code, and Codex. 

# Shelley's Name

Shelley is so named because the main tool it uses is the shell, and I like
putting "-ey" at the end of words. It is also named after Percy Bysshe Shelley,
with an appropriately ironic nod at
"[Ozymandias](https://www.poetryfoundation.org/poems/46565/ozymandias)."
Shelley is a computer program, and, it's an it.

# Global Flags

These flags apply to all commands:

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | | Path to shelley.json configuration file |
| `-db` | shelley.db | Path to SQLite database file |
| `-debug` | false | Enable debug logging |
| `-model` | (from config) | LLM model to use |
| `-default-model` | claude-opus-4.5 | Default model for web UI |
| `-predictable-only` | false | Use only the predictable test model |

Example:
```bash
shelley -config ~/.config/shelley/shelley.json -db ~/.config/shelley/shelley.db chat
```

# Using exe.dev LLM Gateway

If running on an exe.dev VM, you can use the built-in LLM gateway instead of your own API keys.

## Option 1: Config File (Recommended)

Create a config file at `~/.config/shelley/shelley.json`:

```bash
mkdir -p ~/.config/shelley
cat > ~/.config/shelley/shelley.json << 'EOF'
{
  "llm_gateway": "http://169.254.169.254/gateway/llm",
  "default_model": "claude-sonnet-4.5"
}
EOF
```

Then add an alias to your `~/.bashrc`:

```bash
alias shelley='~/shelley-cli/bin/shelley -db ~/.config/shelley/shelley.db -config ~/.config/shelley/shelley.json'
```

Now you can simply run:

```bash
shelley chat    # CLI mode
shelley serve   # Web UI
```

## Option 2: Environment Variables

Alternatively, add to your `~/.bashrc`:

```bash
# exe.dev LLM Gateway configuration
export ANTHROPIC_API_KEY="$EXE_DEV_TOKEN"
export ANTHROPIC_BASE_URL="http://169.254.169.254/gateway/llm/anthropic"
export OPENAI_API_KEY="$EXE_DEV_TOKEN"
export OPENAI_BASE_URL="http://169.254.169.254/gateway/llm/openai/v1"
export FIREWORKS_API_KEY="$EXE_DEV_TOKEN"
export FIREWORKS_BASE_URL="http://169.254.169.254/gateway/llm/fireworks/inference/v1"
```

Then `source ~/.bashrc` or start a new terminal.

## Available Models

| Provider | Models |
|----------|--------|
| Anthropic | claude-opus-4.5, claude-sonnet-4.5, claude-haiku-4.5 |
| OpenAI | gpt-5, gpt-5-nano, gpt-5.1-codex |
| Fireworks | qwen3-coder-fireworks, glm-4p6-fireworks |

In the CLI, use `/models` to list available models and `/model <name>` to switch.

# License

Shelley is Apache 2.0 licensed. See [LICENSE](LICENSE) for details.

This fork maintains the same license. For contributions to upstream Shelley, see [boldsoftware/shelley](https://github.com/boldsoftware/shelley).

# Building Shelley

Run `make`. Run `make serve` to start Shelley locally.

## Dev Tricks

If you want to see how mobile looks, and you're on your home
network where you've got mDNS working fine, you can
run 

```
socat TCP-LISTEN:9001,fork TCP:localhost:9999
```
