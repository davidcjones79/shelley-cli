# Shelley CLI - Summary

**Shelley CLI** is a terminal interface for the Shelley coding agent, a fork that adds CLI capabilities, file transfer, and distributed task execution.

## Key Features

- **CLI Interface** - Terminal UI with streaming and tool execution (works with iTerm2, Windows Terminal, Alacritty)
- **Igor** - File transfer assistant (`shelley igor`)
- **Coordinator** - Distributed task queue with worker pool for parallel execution across VMs
- **Task Groups** - Batch related tasks with shared repo/branch settings
- **Git Integration** - Workers can clone, commit, and push to feature branches
- **`/pick` Command** - Quick analysis of uploaded files

## Main Commands

| Command | Description |
|---------|-------------|
| `shelley chat` | Interactive CLI mode |
| `shelley serve` | Web server |
| `shelley dashboard` | Coordinator management UI |
| `shelley coord` | Headless coordinator |
| `shelley igor` | File transfer server |

## Tech Stack

- **Backend:** Go
- **Storage:** SQLite
- **UI:** TypeScript + React

## Quick Install (exe.dev)

```bash
curl -fsSL https://raw.githubusercontent.com/davidcjones79/shelley-cli/main/install-cli.sh | bash
source ~/.bashrc
shelley chat
```

## License

Apache 2.0
