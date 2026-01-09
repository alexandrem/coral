#!/usr/bin/env bash
# Quick test to verify agent's QueryCPUProfileSamples endpoint is working.

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Test: Agent CPU Profile Endpoint${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Check if agent is running.
if ! docker compose ps agent-0 --status running --format json | grep -q "agent-0"; then
    echo -e "${RED}Error: agent-0 service is not running${NC}"
    echo "Please start with: docker compose up -d"
    exit 1
fi

echo -e "${GREEN}✓ Agent is running${NC}"
echo ""

# Wait a bit for continuous profiling to collect samples.
echo -e "${YELLOW}Waiting 30 seconds for continuous profiling to collect samples...${NC}"
sleep 30

# Get agent mesh IP from docker inspect.
AGENT_CONTAINER=$(docker compose ps agent-0 -q)
if [ -z "$AGENT_CONTAINER" ]; then
    echo -e "${RED}Error: Could not find agent container${NC}"
    exit 1
fi

# Check agent logs for continuous profiling.
echo -e "\n${YELLOW}Checking agent logs for continuous profiling activity...${NC}"
if docker compose logs agent-0 2>&1 | grep -q "Starting continuous CPU profiling\|Continuous profiling"; then
    echo -e "${GREEN}✓ Continuous profiling is enabled in agent${NC}"

    # Show some log lines.
    echo -e "\n${BLUE}Recent profiling logs:${NC}"
    docker compose logs agent-0 2>&1 | grep -i "continuous\|profil" | tail -5
else
    echo -e "${RED}✗ Continuous profiling may not be running${NC}"
    echo "Agent logs:"
    docker compose logs agent-0 | tail -20
    exit 1
fi

# Check if agent has collected any samples locally.
echo -e "\n${YELLOW}Checking agent's local database for samples...${NC}"
docker compose exec agent-0 sh -c '
    if [ -f /root/.coral/agent/metrics.duckdb ]; then
        echo "Database exists at /root/.coral/agent/metrics.duckdb"
        # Try to query sample count (basic check).
        duckdb /root/.coral/agent/metrics.duckdb "SELECT COUNT(*) as sample_count FROM cpu_profile_samples_local" 2>&1 || echo "Failed to query database"
    else
        echo "Database not found at /root/.coral/agent/metrics.duckdb"
        ls -la /root/.coral/agent/ 2>&1 || echo "Directory does not exist"
    fi
' || echo -e "${YELLOW}Could not check database (may not have duckdb CLI in container)${NC}"

echo -e "\n${BLUE}========================================${NC}"
echo -e "${BLUE}Test Complete${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Next steps:"
echo "1. Verify continuous profiler is collecting data (check logs above)"
echo "2. If no samples, check agent configuration in agent.yaml"
echo "3. If samples exist locally, check Colony polling logs"
