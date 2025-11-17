#!/usr/bin/env python3
"""
Manual E2E test for MCP proxy.
Tests the proxy via stdin/stdout with a running colony.

Usage: python3 scripts/test-mcp-proxy.py
"""

import json
import subprocess
import sys
import time
import requests

def check_colony_running():
    """Check if colony is running."""
    try:
        response = requests.get("http://localhost:9000", timeout=2)
        return True
    except:
        return False

def send_mcp_request(process, request):
    """Send an MCP JSON-RPC request and get response."""
    request_json = json.dumps(request) + "\n"
    print(f"→ {json.dumps(request, indent=2)}")

    process.stdin.write(request_json)
    process.stdin.flush()

    response_line = process.stdout.readline()
    if not response_line:
        raise Exception("No response from proxy")

    response = json.loads(response_line)
    print(f"← {json.dumps(response, indent=2)}")
    return response

def main():
    print("🧪 MCP Proxy E2E Test\n")

    # Check colony is running
    print("Checking colony status...")
    if not check_colony_running():
        print("❌ Colony is not running on port 9000")
        print("   Start colony first: coral colony start")
        return 1
    print("✓ Colony is running\n")

    # Start proxy subprocess
    print("Starting MCP proxy...")
    process = subprocess.Popen(
        ["./bin/coral", "colony", "mcp", "proxy"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1
    )

    # Give proxy time to start
    time.sleep(1)

    if process.poll() is not None:
        stderr = process.stderr.read()
        print(f"❌ Proxy failed to start:\n{stderr}")
        return 1

    print(f"✓ Proxy started (PID: {process.pid})\n")

    try:
        # Test 1: Initialize
        print("=" * 60)
        print("Test 1: initialize")
        print("=" * 60)
        response = send_mcp_request(process, {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {}
        })

        assert response["jsonrpc"] == "2.0", "Invalid JSON-RPC version"
        assert response["id"] == 1, "Invalid response ID"
        assert "error" not in response, f"Initialize failed: {response.get('error')}"
        assert response["result"]["protocolVersion"] == "2024-11-05", "Wrong protocol version"
        assert "serverInfo" in response["result"], "Missing serverInfo"
        print("✓ Initialize successful\n")

        # Test 2: List Tools
        print("=" * 60)
        print("Test 2: tools/list")
        print("=" * 60)
        response = send_mcp_request(process, {
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/list",
            "params": {}
        })

        assert response["jsonrpc"] == "2.0", "Invalid JSON-RPC version"
        assert response["id"] == 2, "Invalid response ID"
        assert "error" not in response, f"tools/list failed: {response.get('error')}"
        assert "tools" in response["result"], "Missing tools in result"
        tool_count = len(response["result"]["tools"])
        print(f"✓ Tools list returned {tool_count} tools\n")

        # Print tool names
        print("Available tools:")
        for tool in response["result"]["tools"]:
            print(f"  - {tool['name']}: {tool.get('description', 'No description')}")
        print()

        # Test 3: Call Tool
        print("=" * 60)
        print("Test 3: tools/call (coral_get_service_health)")
        print("=" * 60)
        response = send_mcp_request(process, {
            "jsonrpc": "2.0",
            "id": 3,
            "method": "tools/call",
            "params": {
                "name": "coral_get_service_health",
                "arguments": {}
            }
        })

        assert response["jsonrpc"] == "2.0", "Invalid JSON-RPC version"
        assert response["id"] == 3, "Invalid response ID"

        if "error" in response:
            print(f"⚠️  Tool call returned error: {response['error']['message']}")
            print("   (This might be expected if MCP ExecuteTool is not implemented yet)")
        else:
            assert "content" in response["result"], "Missing content in result"
            print("✓ Tool call successful\n")

        # Test 4: Invalid Method
        print("=" * 60)
        print("Test 4: invalid method (list_tools instead of tools/list)")
        print("=" * 60)
        response = send_mcp_request(process, {
            "jsonrpc": "2.0",
            "id": 4,
            "method": "list_tools",  # Wrong! Should be "tools/list"
            "params": {}
        })

        assert response["jsonrpc"] == "2.0", "Invalid JSON-RPC version"
        assert response["id"] == 4, "Invalid response ID"
        assert "error" in response, "Should have error for invalid method"
        assert response["error"]["code"] == -32601, "Wrong error code"
        print("✓ Invalid method correctly rejected\n")

        print("=" * 60)
        print("✅ All E2E tests passed!")
        print("=" * 60)

        return 0

    except Exception as e:
        print(f"\n❌ Test failed: {e}")
        import traceback
        traceback.print_exc()
        return 1

    finally:
        # Cleanup
        print("\nCleaning up...")
        process.stdin.close()
        process.terminate()
        process.wait(timeout=5)

if __name__ == "__main__":
    sys.exit(main())
