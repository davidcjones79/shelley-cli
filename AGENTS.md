1. Never add sleeps to tests.
2. Brevity, brevity, brevity! Do not do weird defaults; have only one way of doing things; refactor relentlessly as necessary.
3. If something doesn't work, propagate the error or exit or crash. Do not have "fallbacks".
4. Do not keep old methods around for "compatibility"; this is a new project and there
   are no compatibility concerns yet.
5. The "predictable" model is a test fixture that lets you specify what a model would say if you said
   a thing. This is useful for interactive testing with a browser, since you don't rely on a model,
   and can fabricate some inputs and outputs. To test things, launch shelley with the relevant flag
   to only expose this model, and use shelley with a browser.
6. Build the UI (`make ui` or `cd ui && pnpm install && pnpm run build`) before running Go tests so `ui/dist` exists for the embed.
7. Run Go unit tests with `go test ./server` (or narrower packages while iterating) once the UI bundle is built.
8. To programmatically type into the React message input (e.g., in browser automation), you must use React's internal setter:
   ```javascript
   const input = document.querySelector('[data-testid="message-input"]');
   const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
   nativeInputValueSetter.call(input, 'your message');
   input.dispatchEvent(new Event('input', { bubbles: true }));
   ```
   Simply setting `input.value = '...'` won't work because React won't detect the change.
9. Commit your changes before finishing your turn.
10. If you are testing Shelley itself, be aware that you might be running "under" shelley,
  and indiscriminately running pkill -f shelley may break things.
11. To test the Shelley UI in a separate instance, build with `make build`, then run on a
    different port with a separate database:
    ```
    ./bin/shelley -config /exe.dev/shelley.json -db /tmp/shelley-test.db serve -port 8002
    ```
    Then use browser tools to navigate to http://localhost:8002/ and interact with the UI.

---

## CLI Reference

See [docs/CLI_REFERENCE.md](docs/CLI_REFERENCE.md) for complete Shelley CLI documentation including:
- All commands and flags
- Coordinator system for distributed task execution
- Common workflows and examples

---

## Spawning Sub-Agents

You can spawn additional Shelley instances to work in parallel. This is useful when:
- A task can be decomposed into independent subtasks
- You need to try multiple approaches simultaneously
- Long-running tasks shouldn't block the main conversation

### Direct Sub-Agent Spawning

Run another Shelley instance with a specific prompt:

```bash
# Spawn a sub-agent that runs a single task and exits
shelley chat -yes -no-sync -prompt "Create a Python script that calculates fibonacci numbers"

# Spawn with output captured
shelley chat -yes -no-sync -prompt "Analyze main.go for bugs" > /tmp/analysis.txt 2>&1 &

# Check if sub-agent is still running
pgrep -f "shelley chat"

# Wait for all sub-agents to complete
wait
```

### Key Flags for Sub-Agents

| Flag | Purpose |
|------|--------|
| `-yes` | Auto-accept all tool operations (required for unattended execution) |
| `-no-sync` | Don't sync to database (prevents conversation clutter) |
| `-prompt "..."` | Execute this prompt and exit (non-interactive mode) |
| `-no-browser` | Disable browser tools (faster startup if not needed) |

### Using Slash Commands

From within a Shelley chat session:

```
/spawn "Review auth.go for security issues"     # Spawn single sub-agent
/spawns                                          # List all sub-agents with status
/spawn-output agent-1                            # View output from agent-1
/spawn-wait                                      # Wait for all to complete
/spawn-clear                                     # Clear completed agents

/parallel "Review auth.go" | "Review api.go" | "Review db.go"  # Spawn multiple
```

### Example: Parallel File Processing

```bash
# Process 3 files in parallel
for file in auth.go api.go db.go; do
  shelley chat -yes -no-sync -prompt "Review $file for security issues, save report to /tmp/${file%.go}-security.md" &
done
wait
echo "All reviews complete"
cat /tmp/*-security.md > /tmp/full-security-report.md
```

### When to Use the Coordinator Instead

For more than 3-4 parallel tasks, or when you need:
- Task queue with retry logic
- Progress monitoring dashboard
- Git integration (branches, commits)
- Worker health monitoring

Use the coordinator system instead: `shelley dashboard -auto-start`

---

## Saved Scripts

Shelley can save and reuse orchestration scripts across sessions. Before creating new automation, check if a relevant script exists.

### Script Registry

Scripts are stored in `~/.config/shelley/scripts/` with metadata in `registry.json`:

```
~/.config/shelley/scripts/
├── registry.json           # Script metadata
└── scripts/
    ├── parallel-reviews.sh
    ├── setup-project.sh
    └── deploy-staging.sh
```

### Using Scripts

From within a Shelley chat session:

```
/scripts                    # List all saved scripts
/script-show <name>         # View script contents
/script-run <name>          # Execute a saved script
/script-delete <name>       # Remove a script
```

### Creating Reusable Scripts

When you create a useful orchestration script, save it for future use:

```bash
# Create your script
cat > /tmp/parallel-reviews.sh << 'EOF'
#!/bin/bash
for file in *.go; do
  shelley chat -yes -no-sync -prompt "Review $file for security issues" &
done
wait
echo "All reviews complete"
EOF
chmod +x /tmp/parallel-reviews.sh
```

Then save it:
```
/script-save parallel-reviews /tmp/parallel-reviews.sh "Run security reviews on all Go files in parallel"
```

### Best Practices

1. **Check existing scripts first** before creating new automation
2. **Use descriptive names** that indicate the script's purpose
3. **Add descriptions** when saving to help future discovery
4. **Test scripts** before saving to ensure they work correctly
