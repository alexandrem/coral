# CLI Reference

## Colony Management

```bash
# Start the colony
coral colony start                    # Start in foreground
coral colony start --daemon           # Start as background daemon
coral colony start --port 3001        # Use custom port

# Check colony status
coral colony status
coral colony status --json            # JSON output

# Stop the colony
coral colony stop
```

## Agent Management

```bash
# Start the agent daemon (required before connecting services)
coral agent start
coral agent start --config /etc/coral/agent.yaml
coral agent start --colony-id my-app-prod

# Check agent status
coral agent status

# Stop the agent
coral agent stop
```

## Service Connections

```bash
# Connect the running agent to observe services
# Format: name:port[:health][:type]
coral connect <service-spec>...

# Single service examples
coral connect frontend:3000
coral connect api:8080:/health:http
coral connect database:5432

# Multiple services at once
coral connect frontend:3000:/health api:8080:/health redis:6379

# Legacy syntax (still supported for single service)
coral connect frontend --port 3000 --health /health

> **Note:**
> - The agent must be running (`coral agent start`) before using `coral connect`
> - Services are dynamically added without restarting the agent
> - The agent uses discovery-provided WireGuard endpoints
> - For local testing, ensure discovery advertises a reachable address (e.g., `127.0.0.1:41580`)
```

## AI Queries

```bash
# Configure your LLM (first time setup)
coral ask config
# Choose provider: OpenAI, Anthropic, or Ollama (local)
# Provide API key (stored locally, never sent to Coral servers)

# Ask questions about your system (uses YOUR LLM account)
coral ask "Why is the API slow?"
coral ask "What changed in the last hour?"
coral ask "Are there any errors in the frontend?"
coral ask "Show me the service dependencies"

# JSON output
coral ask "System status?" --json

# Use specific model
coral ask "What's happening?" --model anthropic:claude-3-5-sonnet-20241022
```

**How it works:**
- `coral ask` runs a local Genkit agent on your workstation
- Connects to Colony as MCP server to access observability data
- Uses **your own LLM API keys** (OpenAI, Anthropic, or local Ollama)
- You control model choice, costs, and data privacy

## Live Debugging (SDK-integrated mode)

```bash
# Live debugging - attach probes on-demand
coral debug attach <service> --function <func-name> --duration 60s
coral debug trace <service> --path "/api/endpoint" --duration 5m
coral debug list <service>  # Show active probes
coral debug detach <service> --all
coral debug logs <service>  # View collected probe data
```

## Diagnostic Commands

```bash
# Run diagnostic tools on agent hosts
coral exec <service> <command>

# Examples
coral exec api "netstat -an | grep ESTABLISHED"
coral exec api "ps aux | grep node"
coral exec api "lsof -i :8080"
coral exec api "tcpdump -i any port 8080 -c 100"
coral exec frontend "free -h"
coral exec database "iostat -x 5 3"

# LLM can orchestrate these automatically
coral ask "Why is the API not responding?"
# → May run: netstat, lsof, strace to diagnose
```

**Note:** Commands run with agent's permissions. Configure allowed commands via
agent policy for security.

## Version

```bash
coral version
```
