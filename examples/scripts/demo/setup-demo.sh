#!/bin/bash
# Demo setup script for Coral sandboxed TypeScript execution
# This script sets up a demo environment to showcase script execution

set -e

echo "🚀 Setting up Coral Script Execution Demo"
echo ""

# Check if Deno is installed
if ! command -v deno &> /dev/null; then
    echo "❌ Deno is not installed. Installing..."
    curl -fsSL https://deno.land/install.sh | sh
    export PATH="$HOME/.deno/bin:$PATH"
    echo "✅ Deno installed"
else
    echo "✅ Deno found: $(deno --version | head -n 1)"
fi

# Create demo directory structure
DEMO_DIR="$(pwd)/coral-demo"
mkdir -p "$DEMO_DIR/duckdb"
mkdir -p "$DEMO_DIR/scripts"

echo ""
echo "📁 Created demo directory: $DEMO_DIR"

# Create a sample DuckDB database with test data
echo ""
echo "📊 Creating sample DuckDB database with test data..."

cat > "$DEMO_DIR/setup-db.sql" <<'EOF'
-- Create tables
CREATE TABLE otel_spans_local (
    trace_id VARCHAR,
    span_id VARCHAR,
    service_name VARCHAR,
    duration_ns BIGINT,
    is_error BOOLEAN,
    http_status INTEGER,
    http_method VARCHAR,
    http_route VARCHAR,
    start_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE system_metrics_local (
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    name VARCHAR,
    value DOUBLE,
    unit VARCHAR,
    metric_type VARCHAR,
    attributes VARCHAR
);

-- Insert sample spans (simulating real service traffic)
INSERT INTO otel_spans_local VALUES
    -- Payments service - mostly healthy
    ('trace-001', 'span-001', 'payments', 45000000, false, 200, 'GET', '/api/payments', CURRENT_TIMESTAMP - INTERVAL '5 minutes'),
    ('trace-002', 'span-002', 'payments', 52000000, false, 200, 'POST', '/api/payments', CURRENT_TIMESTAMP - INTERVAL '4 minutes'),
    ('trace-003', 'span-003', 'payments', 650000000, true, 500, 'POST', '/api/payments', CURRENT_TIMESTAMP - INTERVAL '3 minutes'),
    ('trace-004', 'span-004', 'payments', 48000000, false, 200, 'GET', '/api/payments/123', CURRENT_TIMESTAMP - INTERVAL '2 minutes'),
    ('trace-005', 'span-005', 'payments', 710000000, true, 500, 'POST', '/api/payments', CURRENT_TIMESTAMP - INTERVAL '1 minute'),

    -- Orders service - very healthy
    ('trace-006', 'span-006', 'orders', 25000000, false, 200, 'GET', '/api/orders', CURRENT_TIMESTAMP - INTERVAL '5 minutes'),
    ('trace-007', 'span-007', 'orders', 30000000, false, 200, 'POST', '/api/orders', CURRENT_TIMESTAMP - INTERVAL '3 minutes'),
    ('trace-008', 'span-008', 'orders', 28000000, false, 200, 'GET', '/api/orders/456', CURRENT_TIMESTAMP - INTERVAL '1 minute'),

    -- User service - some issues
    ('trace-009', 'span-009', 'users', 520000000, false, 200, 'GET', '/api/users', CURRENT_TIMESTAMP - INTERVAL '4 minutes'),
    ('trace-010', 'span-010', 'users', 580000000, false, 200, 'POST', '/api/users', CURRENT_TIMESTAMP - INTERVAL '2 minutes');

-- Insert system metrics
INSERT INTO system_metrics_local VALUES
    (CURRENT_TIMESTAMP - INTERVAL '5 minutes', 'system.cpu.utilization', 45.5, 'percent', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '4 minutes', 'system.cpu.utilization', 52.3, 'percent', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '3 minutes', 'system.cpu.utilization', 78.9, 'percent', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '2 minutes', 'system.cpu.utilization', 81.2, 'percent', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '1 minute', 'system.cpu.utilization', 76.5, 'percent', 'gauge', '{}'),

    (CURRENT_TIMESTAMP - INTERVAL '5 minutes', 'system.memory.usage', 8589934592, 'bytes', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '3 minutes', 'system.memory.usage', 12884901888, 'bytes', 'gauge', '{}'),
    (CURRENT_TIMESTAMP - INTERVAL '1 minute', 'system.memory.usage', 14495514624, 'bytes', 'gauge', '{}'),

    (CURRENT_TIMESTAMP, 'system.memory.total', 17179869184, 'bytes', 'gauge', '{}');
EOF

# Create the database (requires DuckDB CLI)
if command -v duckdb &> /dev/null; then
    duckdb "$DEMO_DIR/duckdb/agent.db" < "$DEMO_DIR/setup-db.sql"
    echo "✅ Sample database created at $DEMO_DIR/duckdb/agent.db"
else
    echo "⚠️  DuckDB CLI not found. You'll need to create the database manually."
    echo "   Install: https://duckdb.org/docs/installation/"
fi

# Create a simple SDK server mock for demo (Go would be better, but this is quick)
cat > "$DEMO_DIR/sdk-server-mock.ts" <<'EOF'
// Simple SDK Server Mock for Demo
// In production, this would be the Go HTTP server

import { serve } from "https://deno.land/std@0.208.0/http/server.ts";

const dbPath = Deno.env.get("DEMO_DIR") + "/duckdb/agent.db";

async function queryDuckDB(sql: string): Promise<any[]> {
  // In real implementation, this would query DuckDB
  // For demo, we return mock data
  console.log(`[SDK Server] Query: ${sql}`);

  if (sql.includes("otel_spans_local")) {
    return [
      {
        trace_id: "trace-003",
        service_name: "payments",
        duration_ns: 650000000,
        is_error: true,
        http_status: 500,
      },
      {
        trace_id: "trace-005",
        service_name: "payments",
        duration_ns: 710000000,
        is_error: true,
        http_status: 500,
      },
    ];
  }

  if (sql.includes("system.cpu.utilization")) {
    return [{ value: 78.9 }];
  }

  if (sql.includes("system.memory")) {
    return [{ used: 14495514624, total: 17179869184 }];
  }

  return [];
}

serve(async (req) => {
  const url = new URL(req.url);

  if (url.pathname === "/health") {
    return new Response(JSON.stringify({ status: "ok" }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/db/query" && req.method === "POST") {
    const body = await req.json();
    const rows = await queryDuckDB(body.sql);
    return new Response(JSON.stringify({ rows, count: rows.length }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/metrics/percentile") {
    const service = url.searchParams.get("service");
    const p = url.searchParams.get("p");
    console.log(`[SDK Server] Get P${parseFloat(p!) * 100} for ${service}`);
    return new Response(JSON.stringify({ value: 650000000 }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/metrics/error-rate") {
    const service = url.searchParams.get("service");
    console.log(`[SDK Server] Get error rate for ${service}`);
    return new Response(JSON.stringify({ value: 0.4 }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/system/cpu") {
    return new Response(JSON.stringify({ usage_percent: 78.9 }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/system/memory") {
    return new Response(JSON.stringify({
      used: 14495514624,
      total: 17179869184
    }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  if (url.pathname === "/emit" && req.method === "POST") {
    const event = await req.json();
    console.log(`[SDK Server] Event emitted:`, event);
    return new Response(JSON.stringify({ status: "ok" }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  return new Response("Not Found", { status: 404 });
}, { port: 9003 });

console.log("🚀 SDK Server Mock running on http://localhost:9003");
EOF

echo ""
echo "✅ Demo setup complete!"
echo ""
echo "📖 To run the demo:"
echo ""
echo "1. Start the SDK server mock:"
echo "   cd $DEMO_DIR && DEMO_DIR=$DEMO_DIR deno run --allow-net --allow-env sdk-server-mock.ts"
echo ""
echo "2. In another terminal, run the example scripts:"
echo "   cd examples/scripts"
echo "   deno run --allow-net=localhost:9003 high-latency-alert.ts"
echo ""
echo "   or:"
echo "   deno run --allow-net=localhost:9003 correlation-analysis.ts"
echo ""
echo "3. Watch the logs to see the script detect issues!"
echo ""
echo "📁 Demo files location: $DEMO_DIR"
echo ""
