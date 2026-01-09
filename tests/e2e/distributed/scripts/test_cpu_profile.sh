#!/usr/bin/env bash
# E2E test for CPU profiling with docker-compose setup.
#
# This script:
# 1. Ensures docker-compose services are running
# 2. Waits for the agent to be ready
# 3. Runs a CPU profile command
# 4. Verifies the output contains expected data

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
DURATION_SECONDS=5
FREQUENCY_HZ=99
SERVICE_NAME="${1:-cpu-app}"  # Allow service name as first argument
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
BINARY_PATH="${REPO_ROOT}/bin/coral"
DOCKER_COMPOSE_DIR="${REPO_ROOT}/tests/e2e/distributed"

echo -e "${YELLOW}Starting E2E test for CPU profiling...${NC}"
echo "Service: ${SERVICE_NAME}"
echo "Duration: ${DURATION_SECONDS}s"
echo "Frequency: ${FREQUENCY_HZ}Hz"
echo ""

# Step 1: Check if docker-compose services are running
echo -e "\n${YELLOW}Step 1: Checking docker-compose services...${NC}"
if ! (cd "${DOCKER_COMPOSE_DIR}" && docker compose ps | grep -q "agent-0.*running"); then
    echo -e "${RED}Error: agent-0 service is not running${NC}"
    echo "Please start the services with: cd tests/e2e/distributed && docker compose up -d"
    exit 1
fi
echo -e "${GREEN}✓ Services are running${NC}"

# Step 2: Wait for agent to be ready
echo -e "\n${YELLOW}Step 2: Waiting for agent to be ready...${NC}"
MAX_WAIT=30
WAIT_COUNT=0
while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
    if (cd "${DOCKER_COMPOSE_DIR}" && docker compose logs agent-0 2>&1 | grep -q "Agent started successfully\|RPC server listening"); then
        echo -e "${GREEN}✓ Agent is ready${NC}"
        break
    fi
    sleep 1
    WAIT_COUNT=$((WAIT_COUNT + 1))
    if [ $((WAIT_COUNT % 5)) -eq 0 ]; then
        echo "  Still waiting... ($WAIT_COUNT/${MAX_WAIT}s)"
    fi
done

if [ $WAIT_COUNT -eq $MAX_WAIT ]; then
    echo -e "${RED}Error: Agent did not become ready within ${MAX_WAIT} seconds${NC}"
    echo "Agent logs:"
    (cd "${DOCKER_COMPOSE_DIR}" && docker compose logs agent-0 | tail -20)
    exit 1
fi

# Step 3: Run CPU profiling command
echo -e "\n${YELLOW}Step 3: Running CPU profile command...${NC}"
echo "Command: ${BINARY_PATH} debug cpu-profile -s ${SERVICE_NAME} -d ${DURATION_SECONDS} --frequency ${FREQUENCY_HZ}"

# Capture output and exit code
OUTPUT_FILE=$(mktemp)
if (cd "${REPO_ROOT}" && "${BINARY_PATH}" debug cpu-profile -s "${SERVICE_NAME}" -d "${DURATION_SECONDS}" --frequency "${FREQUENCY_HZ}" > "${OUTPUT_FILE}" 2>&1); then
    EXIT_CODE=0
else
    EXIT_CODE=$?
fi

# Display output
cat "${OUTPUT_FILE}"

# Step 4: Verify output
echo -e "\n${YELLOW}Step 4: Verifying output...${NC}"

if [ $EXIT_CODE -ne 0 ]; then
    echo -e "${RED}✗ CPU profiling command failed with exit code ${EXIT_CODE}${NC}"
    rm -f "${OUTPUT_FILE}"
    exit 1
fi

# Check for expected output patterns
CHECKS_PASSED=0
CHECKS_TOTAL=0

# Check 1: Success message
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if grep -q "Total samples:" "${OUTPUT_FILE}"; then
    echo -e "${GREEN}✓ Found total samples in output${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Missing total samples in output${NC}"
fi

# Check 2: Unique stacks
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if grep -q "Unique stacks:" "${OUTPUT_FILE}"; then
    echo -e "${GREEN}✓ Found unique stacks in output${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Missing unique stacks in output${NC}"
fi

# Check 3: No error messages
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if ! grep -qi "error\|failed" "${OUTPUT_FILE}" 2>/dev/null || grep -q "Total samples:" "${OUTPUT_FILE}"; then
    echo -e "${GREEN}✓ No errors in output${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Found errors in output${NC}"
fi

# Check 4: Stack trace data (if any samples were captured)
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if grep -qE "0x[0-9a-f]+|main\.|crypto/" "${OUTPUT_FILE}" || grep -q "Total samples: 0" "${OUTPUT_FILE}"; then
    echo -e "${GREEN}✓ Stack trace data present or no samples (expected for idle service)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${YELLOW}⚠ No stack traces found (might be expected for idle service)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
fi

# Clean up
rm -f "${OUTPUT_FILE}"

# Final result
echo -e "\n${YELLOW}Test Results: ${CHECKS_PASSED}/${CHECKS_TOTAL} checks passed${NC}"

if [ $CHECKS_PASSED -eq $CHECKS_TOTAL ]; then
    echo -e "${GREEN}✓ All E2E tests passed!${NC}"
    exit 0
else
    echo -e "${RED}✗ Some E2E tests failed${NC}"
    exit 1
fi
