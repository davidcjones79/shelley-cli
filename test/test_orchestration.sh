#!/bin/bash
# Test script for Shelley orchestration features
# Run from shelley-cli directory: ./test/test_orchestration.sh

set -e

echo "=== Testing Shelley Orchestration Features ==="
echo ""

# Build the test binary
echo "Building shelley..."
go build -o /tmp/shelley-test ./cmd/shelley
SHELLEY="/tmp/shelley-test"

# Test 1: Help includes new commands
echo "Test 1: Help text includes orchestration commands..."
$SHELLEY chat -no-sync -prompt "/help" 2>&1 | grep -q "Sub-Agents" && echo "✅ Help includes Sub-Agents section" || echo "❌ Missing Sub-Agents section"
$SHELLEY chat -no-sync -prompt "/help" 2>&1 | grep -q "Scripts" && echo "✅ Help includes Scripts section" || echo "❌ Missing Scripts section"
$SHELLEY chat -no-sync -prompt "/help" 2>&1 | grep -q "Orchestration" && echo "✅ Help includes Orchestration section" || echo "❌ Missing Orchestration section"

# Test 2: Script registry
echo ""
echo "Test 2: Script registry..."

# Create a test script
cat > /tmp/test-script.sh << 'EOF'
#!/bin/bash
echo "Test script executed successfully"
echo "Working directory: $(pwd)"
EOF
chmod +x /tmp/test-script.sh

# Save the script
echo "  Saving script..."
SAVE_OUTPUT=$($SHELLEY chat -no-sync -prompt "/script-save test-script /tmp/test-script.sh Test script for validation" 2>&1)
if echo "$SAVE_OUTPUT" | grep -q "Saved script"; then
    echo "✅ Script saved successfully"
else
    echo "❌ Script save failed"
    echo "$SAVE_OUTPUT"
fi

# List scripts
echo "  Listing scripts..."
LIST_OUTPUT=$($SHELLEY chat -no-sync -prompt "/scripts" 2>&1)
if echo "$LIST_OUTPUT" | grep -q "test-script"; then
    echo "✅ Script appears in list"
else
    echo "❌ Script not in list"
    echo "$LIST_OUTPUT"
fi

# Show script
echo "  Showing script..."
SHOW_OUTPUT=$($SHELLEY chat -no-sync -prompt "/script-show test-script" 2>&1)
if echo "$SHOW_OUTPUT" | grep -q "Test script executed"; then
    echo "✅ Script content shown correctly"
else
    echo "❌ Script show failed"
fi

# Run script
echo "  Running script..."
RUN_OUTPUT=$($SHELLEY chat -no-sync -prompt "/script-run test-script" 2>&1)
if echo "$RUN_OUTPUT" | grep -q "Test script executed successfully"; then
    echo "✅ Script executed successfully"
else
    echo "❌ Script run failed"
    echo "$RUN_OUTPUT"
fi

# Delete script
echo "  Deleting script..."
DEL_OUTPUT=$($SHELLEY chat -no-sync -prompt "/script-delete test-script" 2>&1)
if echo "$DEL_OUTPUT" | grep -q "Deleted"; then
    echo "✅ Script deleted successfully"
else
    echo "❌ Script delete failed"
fi

# Test 3: Parse plan function
echo ""
echo "Test 3: Plan parsing (tested via /orchestrate output)..."

# Note: Full orchestrate test would require coordinator, so we just verify the command exists
ORCH_OUTPUT=$($SHELLEY chat -no-sync -prompt "/orchestrate" 2>&1)
if echo "$ORCH_OUTPUT" | grep -q "Usage:"; then
    echo "✅ /orchestrate command available"
else
    echo "❌ /orchestrate command not working"
fi

# Test 4: Coord commands exist
echo ""
echo "Test 4: Coordinator commands..."
COORD_OUTPUT=$($SHELLEY chat -no-sync -prompt "/coord" 2>&1)
if echo "$COORD_OUTPUT" | grep -q "Coordinator commands:"; then
    echo "✅ /coord command available with subcommands"
else
    echo "❌ /coord command not working"
fi

# Test 5: Spawn commands exist (but don't actually spawn to avoid resource issues)
echo ""
echo "Test 5: Spawn commands..."
SPAWN_OUTPUT=$($SHELLEY chat -no-sync -prompt "/spawn" 2>&1)
if echo "$SPAWN_OUTPUT" | grep -q "Usage:"; then
    echo "✅ /spawn command available"
else
    echo "❌ /spawn command not working"
fi

SPAWNS_OUTPUT=$($SHELLEY chat -no-sync -prompt "/spawns" 2>&1)
if echo "$SPAWNS_OUTPUT" | grep -q "No sub-agents"; then
    echo "✅ /spawns command available"
else
    echo "❌ /spawns command not working"
fi

# Cleanup
rm -f /tmp/shelley-test /tmp/test-script.sh

echo ""
echo "=== All basic tests completed ==="
echo ""
echo "Note: Full integration tests (sub-agent spawning, coordinator interaction)"
echo "require a running shelley environment and are not included here."
