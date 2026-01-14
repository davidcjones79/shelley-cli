# Shelley CLI

> A terminal interface for [Shelley](https://github.com/boldsoftware/shelley), the coding agent. This fork adds CLI, file uploader, and `/pick` workflow.

## Quick Install (exe.dev)

```bash
curl -fsSL https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh | bash
source ~/.bashrc
shelley chat
```

This installs the CLI with file uploader service. See [cli/README.md](cli/README.md) for full documentation.

## What's Added in This Fork

- **CLI Interface** - Full terminal UI with streaming, tool execution, conversation sync
- **File Uploader** - Drag-and-drop web UI to get files onto your VM (`shelley uploader`)
- **`/pick` Command** - Quick workflow to analyze uploaded files
- **Systemd Service** - Uploader runs persistently at `https://your-vm.exe.xyz:8099/`

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

# Releases

New releases are automatically created on every commit to `main`. Versions
follow the pattern `v0.N.9OCTAL` where N is the total commit count and 9OCTAL is the commit SHA encoded as octal (prefixed with 9).

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
alias shelley='~/shelley-cli/bin/shelley -db ~/.config/shelley/shelley.db -config /exe.dev/shelley.json'
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

# Open source

Shelley is Apache licensed. We require a CLA for contributions.

# Building Shelley

Run `make`. Run `make serve` to start Shelley locally.

## Dev Tricks

If you want to see how mobile looks, and you're on your home
network where you've got mDNS working fine, you can
run 

```
socat TCP-LISTEN:9001,fork TCP:localhost:9000
```
