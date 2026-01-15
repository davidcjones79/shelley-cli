# Coordinator Integration Plan

## Summary

We're merging the standalone coordinator (from `/home/exedev/coordinator-v2/`) into shelley-cli as subcommands.

## Current State

- Fixed the `tool_use.input: Field required` bug in shelley-cli (commit 038d278)
- Tested parallel workers successfully - 3 workers processed 6 tasks with 0 failures
- Coordinator exists as separate codebase (~1100 lines of Go)

## Decision: Bundle into shelley-cli

**Why:**
- Single binary is easier to distribute
- Version sync is critical (workers need same shelley version)
- Coordinator is tightly coupled to shelley (spawns shelley processes)
- Shared code for db, auth patterns

## Target Structure

```
shelley-cli/
  cmd/shelley/
    main.go           # adds "coord" and "worker" commands
  coordinator/
    coordinator.go    # task queue, worker management, Config, Task, Worker types
    handlers.go       # HTTP API handlers
    schema.sql        # embedded SQL schema
```

## New Commands

- `shelley coord` - Start coordinator server
- `shelley worker` - Start as a worker (connects to coordinator)

## Source Files to Migrate

From `/home/exedev/coordinator-v2/`:
- `main.go` (556 lines) - Coordinator struct, task/worker management
- `handlers.go` (417 lines) - HTTP handlers + embedded HTML UI
- `cmd.go` (78 lines) - CLI flags/setup (merge into main.go commands)
- `schema.sql` (50 lines) - Database schema

## Next Steps

1. Create `coordinator/` package in shelley-cli
2. Copy and adapt the coordinator code
3. Add `coord` and `worker` subcommands to main.go
4. Build and test
5. Then: Build improved web dashboard

## Test Results (before integration)

```
18:25:52 - queued:3 running:3 completed:5 failed:0
18:25:57 - queued:0 running:3 completed:8 failed:0
18:26:02 - queued:0 running:3 completed:8 failed:0
18:26:07 - queued:0 running:0 completed:11 failed:0
All tasks completed!
```

All 3 workers processed tasks in parallel with no errors.
