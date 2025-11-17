#!/bin/bash
# Manual E2E test for MCP proxy
# Usage: ./scripts/test-mcp-proxy.sh

set -e

echo "🧪 MCP Proxy E2E Test"
echo ""

# Check if colony is running
if ! curl -s http://localhost:9000 > /dev/null 2>&1; then
    echo "❌ Colony is not running on port 9000"
    echo "   Start colony first: coral colony start"
    exit 1
fi

echo "✓ Colony is running"
echo ""

# Build fresh binary
echo "→ Building coral binary..."
make build-dev > /dev/null 2>&1
echo "✓ Built bin/coral"
echo ""

# Start proxy in background
echo "→ Starting MCP proxy..."
./bin/coral colony mcp proxy > /tmp/mcp-proxy-stdout.log 2> /tmp/mcp-proxy-stderr.log &
PROXY_PID=$!

# Cleanup on exit
trap "kill $PROXY_PID 2>/dev/null || true; rm -f /tmp/mcp-*.log" EXIT

# Give proxy time to start
sleep 1

if ! ps -p $PROXY_PID > /dev/null; then
    echo "❌ Proxy failed to start"
    echo "   Check logs:"
    echo "   cat /tmp/mcp-proxy-stderr.log"
    exit 1
fi

echo "✓ Proxy started (PID: $PROXY_PID)"
echo ""

# Test 1: Initialize
echo "Test 1: initialize"
echo '→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | tee -a /tmp/mcp-proxy-stdin.log
response=$(echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | nc -q1 localhost 9001 || echo "")

if [ -z "$response" ]; then
    # Fallback: send via stdin if proxy is reading from it
    response=$(echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' > /proc/$PROXY_PID/fd/0 && cat /proc/$PROXY_PID/fd/1)
fi

if echo "$response" | jq -e '.result.protocolVersion == "2024-11-05"' > /dev/null 2>&1; then
    echo "✓ Initialize successful"
else
    echo "❌ Initialize failed"
    echo "   Response: $response"
    exit 1
fi
echo ""

# Test 2: List Tools
echo "Test 2: tools/list"
echo '→ {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
response=$(echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}')

if echo "$response" | jq -e '.result.tools | length > 0' > /dev/null 2>&1; then
    tool_count=$(echo "$response" | jq '.result.tools | length')
    echo "✓ Tools list returned $tool_count tools"
else
    echo "❌ Tools list failed"
    echo "   Response: $response"
    exit 1
fi
echo ""

# Test 3: Invalid Method
echo "Test 3: invalid method (list_tools instead of tools/list)"
echo '→ {"jsonrpc":"2.0","id":3,"method":"list_tools","params":{}}'
response=$(echo '{"jsonrpc":"2.0","id":3,"method":"list_tools","params":{}}')

if echo "$response" | jq -e '.error.code == -32601' > /dev/null 2>&1; then
    echo "✓ Invalid method correctly rejected"
else
    echo "❌ Invalid method test failed"
    echo "   Response: $response"
    exit 1
fi
echo ""

echo "✅ All E2E tests passed!"
echo ""
echo "Logs available at:"
echo "  stdout: /tmp/mcp-proxy-stdout.log"
echo "  stderr: /tmp/mcp-proxy-stderr.log"
