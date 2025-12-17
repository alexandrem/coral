#!/usr/bin/env bash
# Generate HTTP load on the cpu-app service to produce CPU samples.

set -e

DURATION=${1:-30}
CONCURRENCY=${2:-10}
REQUESTS_PER_WORKER=${3:-1000}

echo "Generating load for ${DURATION}s with ${CONCURRENCY} concurrent workers..."
echo "Each worker will make ${REQUESTS_PER_WORKER} requests"

# Function to make requests
make_requests() {
    local worker_id=$1
    local count=0
    local end_time=$((SECONDS + DURATION))

    while [ $SECONDS -lt $end_time ] && [ $count -lt $REQUESTS_PER_WORKER ]; do
        curl -s http://localhost:8081 > /dev/null
        count=$((count + 1))
    done

    echo "Worker $worker_id completed $count requests"
}

# Launch workers in background
for i in $(seq 1 $CONCURRENCY); do
    make_requests $i &
done

# Wait for all workers
wait

echo "Load generation complete"
