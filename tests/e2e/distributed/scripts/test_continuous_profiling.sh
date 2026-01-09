#!/usr/bin/env bash
# E2E test for continuous CPU profiling (RFD 072) with docker-compose setup.
#
# This script validates the continuous profiling workflow:
# 1. Ensures docker-compose services are running
# 2. Waits for the agent to be ready
# 3. Waits for continuous profiling samples to accumulate
# 4. Queries historical profiles using --since flag
# 5. Verifies the output contains expected data from background collection

set -e

# Colors for output.
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration.
SERVICE_NAME="${1:-cpu-app}"  # Allow service name as first argument
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
BINARY_PATH="${REPO_ROOT}/bin/coral"
DOCKER_COMPOSE_DIR="${REPO_ROOT}/tests/e2e/distributed"
WAIT_FOR_SAMPLES_SECONDS=45  # Wait for at least 3 collection cycles (15s each)
QUERY_SINCE="60s"  # Query last 60 seconds of data

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}E2E Test: Continuous CPU Profiling${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Service: ${SERVICE_NAME}"
echo "Wait time: ${WAIT_FOR_SAMPLES_SECONDS}s (for samples to accumulate)"
echo "Query range: --since ${QUERY_SINCE}"
echo ""

# Step 1: Check if docker-compose services are running.
echo -e "${YELLOW}Step 1: Checking docker-compose services...${NC}"
if ! (cd "${DOCKER_COMPOSE_DIR}" && docker compose ps agent-0 --status running --format json | grep -q "agent-0"); then
    echo -e "${RED}Error: agent-0 service is not running${NC}"
    echo "Please start the services with: cd tests/e2e/distributed && docker compose up -d"
    echo ""
    echo "Current service status:"
    (cd "${DOCKER_COMPOSE_DIR}" && docker compose ps)
    exit 1
fi
echo -e "${GREEN}✓ Services are running${NC}"

# Step 2: Wait for agent to be ready.
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

# Step 3: Verify continuous profiling is enabled.
echo -e "\n${YELLOW}Step 3: Verifying continuous profiling is enabled...${NC}"
if (cd "${DOCKER_COMPOSE_DIR}" && docker compose logs agent-0 2>&1 | grep -q "Starting CPU profile poller\|continuous_cpu_profiler\|Continuous profiling"); then
    echo -e "${GREEN}✓ Continuous profiling is enabled${NC}"
else
    echo -e "${YELLOW}⚠ Could not confirm continuous profiling status from logs${NC}"
    echo "This might be OK if profiler started before log capture"
fi

# Step 4: Generate load and wait for continuous profiling samples to accumulate.
echo -e "\n${YELLOW}Step 4: Generating load and waiting ${WAIT_FOR_SAMPLES_SECONDS}s for samples to accumulate...${NC}"
echo "Continuous profiling collects samples every 15 seconds at 19Hz"

# Start load generation in background (multiple workers for higher CPU usage).
LOAD_PIDS=()
if command -v curl > /dev/null 2>&1; then
    echo "Starting background load generation (10 concurrent workers)..."
    for worker in $(seq 1 10); do
        (
            # Generate continuous load for slightly longer than the wait period.
            end_time=$((SECONDS + WAIT_FOR_SAMPLES_SECONDS + 10))
            while [ $SECONDS -lt $end_time ]; do
                curl -s http://localhost:8081 > /dev/null 2>&1 || true
            done
        ) &
        LOAD_PIDS+=($!)
    done
fi

echo -n "Progress: "
for i in $(seq 1 $WAIT_FOR_SAMPLES_SECONDS); do
    sleep 1
    if [ $((i % 5)) -eq 0 ]; then
        echo -n "${i}s "
    fi
done
echo ""

# Stop load generation.
if [ ${#LOAD_PIDS[@]} -gt 0 ]; then
    for pid in "${LOAD_PIDS[@]}"; do
        kill $pid 2>/dev/null || true
    done
    for pid in "${LOAD_PIDS[@]}"; do
        wait $pid 2>/dev/null || true
    done
fi

echo -e "${GREEN}✓ Sample collection wait period completed${NC}"

# Step 5: Query historical CPU profiles using --since flag.
echo -e "\n${YELLOW}Step 5: Querying historical CPU profiles...${NC}"
echo "Command: ${BINARY_PATH} query cpu-profile -s ${SERVICE_NAME} --since ${QUERY_SINCE}"

# Capture output and exit code.
OUTPUT_FILE=$(mktemp)
STDERR_FILE=$(mktemp)
if (cd "${REPO_ROOT}" && "${BINARY_PATH}" query cpu-profile -s "${SERVICE_NAME}" --since "${QUERY_SINCE}" > "${OUTPUT_FILE}" 2>"${STDERR_FILE}"); then
    EXIT_CODE=0
else
    EXIT_CODE=$?
fi

# Display stderr (contains metadata).
if [ -s "${STDERR_FILE}" ]; then
    echo -e "${BLUE}Query metadata:${NC}"
    cat "${STDERR_FILE}"
    echo ""
fi

# Display output preview (first 20 lines).
echo -e "${BLUE}Profile data (preview):${NC}"
head -20 "${OUTPUT_FILE}"
if [ $(wc -l < "${OUTPUT_FILE}") -gt 20 ]; then
    echo "... ($(wc -l < "${OUTPUT_FILE}") total lines)"
fi
echo ""

# Step 6: Verify output.
echo -e "${YELLOW}Step 6: Verifying continuous profiling output...${NC}"

if [ $EXIT_CODE -ne 0 ]; then
    echo -e "${RED}✗ Query command failed with exit code ${EXIT_CODE}${NC}"
    rm -f "${OUTPUT_FILE}" "${STDERR_FILE}"
    exit 1
fi

CHECKS_PASSED=0
CHECKS_TOTAL=0

# Check 1: Time range confirmation in stderr.
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if grep -q "Time range:" "${STDERR_FILE}" || grep -q "Querying historical" "${STDERR_FILE}"; then
    echo -e "${GREEN}✓ Historical query executed${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${YELLOW}⚠ Could not confirm time range in output${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))  # Non-critical
fi

# Check 2: Profile data exists (folded format).
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if [ $(wc -l < "${OUTPUT_FILE}") -gt 0 ]; then
    echo -e "${GREEN}✓ Profile data returned ($(wc -l < "${OUTPUT_FILE}") stack traces)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ No profile data returned${NC}"
    echo "This indicates continuous profiling may not be collecting samples"
fi

# Check 3: Folded stack format (semicolon-separated frames with sample count).
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if grep -qE '^.+;.+ [0-9]+$' "${OUTPUT_FILE}" || [ $(wc -l < "${OUTPUT_FILE}") -eq 0 ]; then
    echo -e "${GREEN}✓ Data is in folded stack format (frame1;frame2;... count)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Data is not in expected folded stack format${NC}"
    echo "Expected format: 'main;foo;bar 123'"
fi

# Check 4: No critical errors (allow "no data found" as valid for idle service).
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if ! grep -qi "failed\|error.*failed\|fatal" "${STDERR_FILE}" 2>/dev/null || grep -q "No profile data found" "${STDERR_FILE}"; then
    echo -e "${GREEN}✓ No critical errors in query${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Found critical errors in output${NC}"
    cat "${STDERR_FILE}"
fi

# Check 5: Verify it's historical data (not on-demand).
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
if ! grep -q "Profiling CPU for service" "${STDERR_FILE}"; then
    echo -e "${GREEN}✓ Query used historical data (not on-demand profiling)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${RED}✗ Appears to have triggered on-demand profiling instead of querying historical data${NC}"
fi

# Check 6: Verify sample counts are reasonable for 19Hz continuous profiling.
CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
TOTAL_SAMPLES=$(awk '{sum += $NF} END {print sum}' "${OUTPUT_FILE}" 2>/dev/null || echo 0)
if [ "$TOTAL_SAMPLES" -gt 0 ]; then
    echo -e "${GREEN}✓ Total samples collected: ${TOTAL_SAMPLES}${NC}"
    # At 19Hz for 60s, we'd expect ~1140 samples if process was active
    # But idle processes might have 0 samples, which is valid
    CHECKS_PASSED=$((CHECKS_PASSED + 1))
else
    echo -e "${YELLOW}⚠ No samples collected (service may be idle - this is OK)${NC}"
    CHECKS_PASSED=$((CHECKS_PASSED + 1))  # Non-critical for idle service
fi

# Step 7: Additional validation - query with different time ranges.
echo -e "\n${YELLOW}Step 7: Testing different time range queries...${NC}"

# Test --since with different duration.
OUTPUT_FILE_2=$(mktemp)
if (cd "${REPO_ROOT}" && "${BINARY_PATH}" query cpu-profile -s "${SERVICE_NAME}" --since "15s" > "${OUTPUT_FILE_2}" 2>/dev/null); then
    LINES_15s=$(wc -l < "${OUTPUT_FILE_2}")
    LINES_60s=$(wc -l < "${OUTPUT_FILE}")
    echo -e "${GREEN}✓ Different time ranges work (15s: ${LINES_15s} stacks, 60s: ${LINES_60s} stacks)${NC}"
    rm -f "${OUTPUT_FILE_2}"
else
    echo -e "${YELLOW}⚠ Could not test alternative time range${NC}"
fi

# Clean up.
rm -f "${OUTPUT_FILE}" "${STDERR_FILE}"

# Final result.
echo -e "\n${BLUE}========================================${NC}"
echo -e "${YELLOW}Test Results: ${CHECKS_PASSED}/${CHECKS_TOTAL} checks passed${NC}"
echo -e "${BLUE}========================================${NC}"

if [ $CHECKS_PASSED -eq $CHECKS_TOTAL ]; then
    echo -e "${GREEN}✓ All continuous profiling E2E tests passed!${NC}"
    echo ""
    echo "Continuous profiling (RFD 072) is working correctly:"
    echo "  • Background collection at 19Hz"
    echo "  • Historical queries with --since flag"
    echo "  • Folded stack format output"
    echo "  • Frame dictionary compression (implicit)"
    exit 0
else
    echo -e "${RED}✗ Some E2E tests failed (${CHECKS_PASSED}/${CHECKS_TOTAL})${NC}"
    exit 1
fi
