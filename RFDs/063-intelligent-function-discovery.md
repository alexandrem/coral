---
rfd: "063"
title: "Intelligent Function Discovery for AI Debugging"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "059", "060" ]
database_migrations: [ ]
areas: [ "ai", "discovery", "search", "observability" ]
---

# RFD 063 - Intelligent Function Discovery for AI Debugging

**Status:** 🚧 Draft

## Summary

Enable AI agents to efficiently discover relevant functions from applications
with 10,000+ functions by combining metrics-driven pre-filtering, semantic
search, and call graph navigation. This solves the "needle in a haystack"
problem where LLMs need to debug specific functions without overwhelming
context.

## Problem

**Current behavior/limitations:**

- Applications have 10,000-50,000+ functions (including dependencies)
- Listing all functions exceeds LLM context limits (~200,000 tokens for 10k
  functions)
- Users don't know exact function names during debugging
- Pattern matching (`.*checkout.*`) returns too many irrelevant results
- No way to prioritize functions that are actually problematic

**Why this matters:**

- AI-driven debugging (via `coral ask`) requires knowing which functions to
  probe
- Manual function discovery is slow and error-prone
- Without smart filtering, the LLM either:
    - Fails to find relevant functions (too many false negatives)
    - Gets overwhelmed with irrelevant results (context overflow)

**Use cases affected:**

- **Performance investigation**: "Why is checkout slow?" → Need to find
  handleCheckout, processPayment, validateCard
- **Error debugging**: "Why are users seeing 500 errors?" → Need to find
  error-prone functions
- **Unknown codebase**: Developer unfamiliar with service needs to debug issue

## Solution

Implement a **multi-tier discovery system** that progressively narrows down from
50,000 functions to the relevant 5-10:

1. **Tier 1: Metrics-Driven Pre-Filtering** - Automatically surface functions
   related to current performance anomalies
2. **Tier 2: Semantic Search** - Keyword-based search across function names,
   files, and comments
3. **Tier 3: Call Graph Navigation** - Navigate from entry points to bottlenecks
4. **Tier 4: Auto-Context Injection** - Automatically add relevant context to
   LLM based on query intent

### Key Design Decisions

- **Metrics-first approach**: Start with problems (slow endpoints), not
  functions
- **Progressive refinement**: LLM narrows search space iteratively, not all at
  once
- **Call graph as navigation**: Functions are nodes in a graph, not a flat list
- **Smart context budget**: Only show relevant functions (target: <50 per query)
- **No embeddings in V1**: Use simple keyword matching (fast, no ML
  dependencies)

### Benefits

- **Efficient discovery**: Find relevant functions in 2-3 tool calls instead of
  guessing
- **Context budget management**: Stay under 5,000 tokens for discovery context
- **Works on unknown codebases**: No prior knowledge of function names required
- **Metrics-driven**: Focuses on functions that are actually problematic
- **Scalable**: Works with 10,000+ function codebases

## Architecture Overview

```
┌────────────────────────────────────────────────────────────────┐
│ User Query: "Why is checkout slow?"                           │
└────────────────┬───────────────────────────────────────────────┘
                 │
                 ▼
┌────────────────────────────────────────────────────────────────┐
│ Tier 1: Auto-Context Injection (Colony)                       │
│                                                                │
│  1. Parse query intent: performance_investigation              │
│  2. Extract keywords: ["checkout", "slow"]                     │
│  3. Query metrics DB for anomalies:                            │
│     → POST /api/checkout: P95 245ms (baseline 80ms) +206%      │
│  4. Inject context into LLM                                    │
└────────────────┬───────────────────────────────────────────────┘
                 │
                 ▼
┌────────────────────────────────────────────────────────────────┐
│ Tier 2: Semantic Function Search (LLM → Colony)               │
│                                                                │
│  LLM calls: coral_search_functions(service="api",              │
│                                    query="checkout")           │
│                                                                │
│  Colony:                                                       │
│  1. Tokenize query: ["checkout"]                              │
│  2. Search function registry (DuckDB):                         │
│     - Match function names: handleCheckout (score: 1.0)        │
│     - Match file paths: checkout.go (score: 0.5)               │
│  3. Rank by score, return top 20                               │
│                                                                │
│  Returns: [handleCheckout, validateCheckout, ...]             │
└────────────────┬───────────────────────────────────────────────┘
                 │
                 ▼
┌────────────────────────────────────────────────────────────────┐
│ Tier 3: Call Graph Navigation (LLM → Colony)                  │
│                                                                │
│  LLM calls: coral_get_function_context(                        │
│               service="api",                                   │
│               function="main.handleCheckout")                  │
│                                                                │
│  Colony:                                                       │
│  1. Query call graph (from static analysis or runtime data)    │
│  2. Get callers: [ServeHTTP, apiRouter]                        │
│  3. Get callees: [validateCart (5%), processPayment (94%)]     │
│  4. Estimate contribution from historical metrics              │
│                                                                │
│  Returns: processPayment is 94% of time → LLM focuses there    │
└────────────────┬───────────────────────────────────────────────┘
                 │
                 ▼
┌────────────────────────────────────────────────────────────────┐
│ Result: LLM found relevant function in 2 tool calls            │
│                                                                │
│  handleCheckout (entry) → processPayment (bottleneck)          │
│                                                                │
│  LLM can now: coral_attach_uprobe(function="processPayment")   │
└────────────────────────────────────────────────────────────────┘
```

## Component Changes

### 1. Colony - Function Registry (DuckDB)

**Extend service registry with function metadata:**

```sql
-- New table: Function registry for all services
CREATE TABLE functions
(
    function_id   VARCHAR PRIMARY KEY,
    service_name  VARCHAR     NOT NULL,
    function_name VARCHAR     NOT NULL,                           -- e.g., "main.handleCheckout"
    package_name  VARCHAR,                                        -- e.g., "handlers"
    file_path     VARCHAR,                                        -- e.g., "handlers/checkout.go"
    line_number   INTEGER,
    offset        BIGINT,                                         -- Memory offset for uprobe

    -- Searchability
    name_tokens   VARCHAR[],                                      -- ["handle", "checkout"] for search
    file_tokens   VARCHAR[],                                      -- ["handlers", "checkout", "go"]

    -- Metadata
    is_exported   BOOLEAN DEFAULT false,
    has_dwarf     BOOLEAN DEFAULT false,

    -- Timestamps
    discovered_at TIMESTAMPTZ NOT NULL,
    last_seen     TIMESTAMPTZ NOT NULL,

    INDEX         idx_functions_service (service_name),
    INDEX         idx_functions_name (function_name),
    INDEX         idx_functions_tokens (name_tokens, file_tokens) -- GIN index for array search
);

-- Call graph edges (static or dynamic)
CREATE TABLE call_graph
(
    caller_id            VARCHAR     NOT NULL, -- Function ID
    callee_id            VARCHAR     NOT NULL, -- Function ID
    call_type            VARCHAR     NOT NULL, -- 'static' (from AST) or 'dynamic' (observed)

    -- Contribution estimates (from metrics)
    avg_contribution_pct FLOAT,                -- % of caller's time spent in callee
    p95_contribution_pct FLOAT,

    -- Metadata
    observed_at          TIMESTAMPTZ NOT NULL,
    last_updated         TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (caller_id, callee_id)
);

-- Function performance metrics (aggregated)
CREATE TABLE function_metrics
(
    function_id      VARCHAR     NOT NULL,
    timestamp        TIMESTAMPTZ NOT NULL,

    -- Performance
    p50_latency_ms   FLOAT,
    p95_latency_ms   FLOAT,
    p99_latency_ms   FLOAT,
    calls_per_minute INTEGER,
    error_rate       FLOAT,

    PRIMARY KEY (function_id, timestamp)
);
```

### 2. Colony - Semantic Search Implementation

**V1: Keyword-based search (no ML required):**

```go
// internal/colony/discovery/search.go
package discovery

import (
    "strings"
    "sort"
)

type FunctionMatch struct {
    FunctionID   string
    FunctionName string
    FilePath     string
    Score        float64
    Reason       string
}

// SearchFunctions performs keyword-based semantic search.
func SearchFunctions(service, query string, limit int) ([]FunctionMatch, error) {
    // 1. Tokenize query
    keywords := tokenize(query)

    // 2. Query DuckDB for candidate functions
    rows := db.Query(`
        SELECT
            function_id,
            function_name,
            file_path,
            name_tokens,
            file_tokens
        FROM functions
        WHERE service_name = $1
    `, service)

    // 3. Score each function
    var matches []FunctionMatch
    for rows.Next() {
        var fn Function
        rows.Scan(&fn.ID, &fn.Name, &fn.FilePath, &fn.NameTokens, &fn.FileTokens)

        score := calculateScore(keywords, fn)
        if score > 0 {
            matches = append(matches, FunctionMatch{
                FunctionID:   fn.ID,
                FunctionName: fn.Name,
                FilePath:     fn.FilePath,
                Score:        score,
                Reason:       explainScore(keywords, fn),
            })
        }
    }

    // 4. Sort by score, return top N
    sort.Slice(matches, func(i, j int) bool {
        return matches[i].Score > matches[j].Score
    })

    if len(matches) > limit {
        matches = matches[:limit]
    }

    return matches, nil
}

// calculateScore computes relevance score (0.0 - 1.0).
func calculateScore(keywords []string, fn Function) float64 {
    score := 0.0

    for _, keyword := range keywords {
        // Exact match in function name: +1.0
        if strings.Contains(strings.ToLower(fn.Name), keyword) {
            score += 1.0
        }

        // Match in name tokens (word boundary): +0.8
        for _, token := range fn.NameTokens {
            if strings.EqualFold(token, keyword) {
                score += 0.8
            }
        }

        // Match in file path: +0.5
        if strings.Contains(strings.ToLower(fn.FilePath), keyword) {
            score += 0.5
        }

        // Match in file tokens: +0.3
        for _, token := range fn.FileTokens {
            if strings.EqualFold(token, keyword) {
                score += 0.3
            }
        }
    }

    // Normalize by number of keywords
    if len(keywords) > 0 {
        score /= float64(len(keywords))
    }

    return score
}

// tokenize splits query into searchable keywords.
func tokenize(query string) []string {
    query = strings.ToLower(query)
    // Remove common words
    stopWords := map[string]bool{
        "the": true, "a": true, "an": true, "is": true, "are": true,
        "why": true, "what": true, "how": true,
    }

    words := strings.Fields(query)
    var tokens []string
    for _, word := range words {
        if !stopWords[word] {
            tokens = append(tokens, word)
        }
    }
    return tokens
}

// explainScore generates human-readable explanation.
func explainScore(keywords []string, fn Function) string {
    matches := []string{}
    for _, keyword := range keywords {
        if strings.Contains(strings.ToLower(fn.Name), keyword) {
            matches = append(matches, fmt.Sprintf("'%s' in name", keyword))
        } else if strings.Contains(strings.ToLower(fn.FilePath), keyword) {
            matches = append(matches, fmt.Sprintf("'%s' in file", keyword))
        }
    }
    return strings.Join(matches, ", ")
}
```

### 3. Colony - Call Graph Analysis

**Build call graph from static analysis + runtime observations:**

```go
// internal/colony/discovery/callgraph.go
package discovery

// GetFunctionContext retrieves call graph neighborhood.
func GetFunctionContext(service, functionName string) (*FunctionContext, error) {
    // 1. Get function metadata
    fn := db.QueryRow(`
        SELECT function_id, function_name, file_path, line_number
        FROM functions
        WHERE service_name = $1 AND function_name = $2
    `, service, functionName)

    // 2. Get callers (who calls this function)
    callers := db.Query(`
        SELECT
            f.function_name,
            f.file_path,
            cg.avg_contribution_pct
        FROM call_graph cg
        JOIN functions f ON cg.caller_id = f.function_id
        WHERE cg.callee_id = $1
        ORDER BY cg.avg_contribution_pct DESC
    `, fn.ID)

    // 3. Get callees (what this function calls)
    callees := db.Query(`
        SELECT
            f.function_name,
            f.file_path,
            cg.avg_contribution_pct,
            fm.p95_latency_ms
        FROM call_graph cg
        JOIN functions f ON cg.callee_id = f.function_id
        LEFT JOIN function_metrics fm ON f.function_id = fm.function_id
        WHERE cg.caller_id = $1
        ORDER BY cg.avg_contribution_pct DESC
    `, fn.ID)

    // 4. Get recent performance metrics
    metrics := db.QueryRow(`
        SELECT
            p50_latency_ms,
            p95_latency_ms,
            p99_latency_ms,
            calls_per_minute,
            error_rate
        FROM function_metrics
        WHERE function_id = $1
        ORDER BY timestamp DESC
        LIMIT 1
    `, fn.ID)

    return &FunctionContext{
        Function:    fn,
        CalledBy:    callers,
        Calls:       callees,
        Performance: metrics,
    }, nil
}
```

### 4. Colony - Auto-Context Injection

**Automatically add relevant context to LLM based on query:**

```go
// internal/colony/mcp/context.go
package mcp

// BuildContextForQuery injects context based on user query.
func BuildContextForQuery(query string) map[string]interface{} {
    ctx := make(map[string]interface{})

    // 1. Detect intent
    intent := detectIntent(query)
    ctx["intent"] = intent

    // 2. Extract service names
    services := extractServiceNames(query)
    ctx["services"] = services

    // 3. If performance-related, add anomalies
    if intent == "performance_investigation" {
        anomalies := getRecentPerformanceAnomalies(services, last15Minutes)
        ctx["performance_anomalies"] = anomalies
    }

    // 4. Extract keywords for suggested search
    keywords := extractKeywords(query)
    ctx["keywords"] = keywords

    // 5. Suggest tools
    ctx["suggested_tools"] = suggestTools(intent, services, keywords)

    return ctx
}

// detectIntent classifies query type.
func detectIntent(query string) string {
    q := strings.ToLower(query)

    if strings.Contains(q, "slow") || strings.Contains(q, "latency") || strings.Contains(q, "performance") {
        return "performance_investigation"
    }
    if strings.Contains(q, "error") || strings.Contains(q, "failing") || strings.Contains(q, "500") {
        return "error_investigation"
    }
    return "general"
}

// getRecentPerformanceAnomalies queries metrics for problems.
func getRecentPerformanceAnomalies(services []string, since time.Duration) []Anomaly {
    rows := db.Query(`
        SELECT
            service_name,
            endpoint,
            p95_latency_ms AS current,
            baseline_p95_latency_ms AS baseline,
            (p95_latency_ms - baseline_p95_latency_ms) / baseline_p95_latency_ms AS regression_pct
        FROM endpoint_metrics
        WHERE service_name = ANY($1)
          AND timestamp > NOW() - $2
          AND p95_latency_ms > baseline_p95_latency_ms * 1.2  -- 20% regression
        ORDER BY regression_pct DESC
        LIMIT 10
    `, services, since)

    var anomalies []Anomaly
    for rows.Next() {
        var a Anomaly
        rows.Scan(&a.Service, &a.Endpoint, &a.Current, &a.Baseline, &a.RegressionPct)
        anomalies = append(anomalies, a)
    }
    return anomalies
}
```

## API Changes

### New MCP Tools

**Tool: `coral_search_functions`**

```protobuf
// proto/coral/colony/v1/discovery.proto

service DiscoveryService {
    // Semantic search for functions
    rpc SearchFunctions(SearchFunctionsRequest) returns (SearchFunctionsResponse);

    // Get call graph context for function
    rpc GetFunctionContext(GetFunctionContextRequest) returns (GetFunctionContextResponse);
}

message SearchFunctionsRequest {
    string service_name = 1;
    string query = 2;               // Natural language keywords
    uint32 limit = 3;               // Max results (default: 20, max: 50)
}

message SearchFunctionsResponse {
    string service_name = 1;
    string query = 2;
    repeated FunctionMatch results = 3;
}

message FunctionMatch {
    string function_id = 1;
    string function_name = 2;
    string file_path = 3;
    uint32 line_number = 4;
    uint64 offset = 5;
    double score = 6;               // Relevance score (0.0 - 1.0)
    string reason = 7;              // Human-readable explanation
}

message GetFunctionContextRequest {
    string service_name = 1;
    string function_name = 2;
    bool include_callers = 3;
    bool include_callees = 4;
    bool include_metrics = 5;
}

message GetFunctionContextResponse {
    FunctionInfo function = 1;
    repeated CallerInfo called_by = 2;
    repeated CalleeInfo calls = 3;
    FunctionMetrics performance = 4;
    string recommendation = 5;      // AI-generated next step
}

message CallerInfo {
    string function_name = 1;
    string file_path = 2;
    string call_frequency = 3;      // "always", "sometimes", "rarely"
}

message CalleeInfo {
    string function_name = 1;
    string file_path = 2;
    double estimated_contribution = 3;  // % of parent's time
    double avg_duration_ms = 4;
}

message FunctionMetrics {
    double p50_latency_ms = 1;
    double p95_latency_ms = 2;
    double p99_latency_ms = 3;
    uint32 calls_per_minute = 4;
    double error_rate = 5;
}
```

### Agent → Colony: Function Registration

**Agents report discovered functions to Colony:**

```protobuf
// Extend agent.proto

message ReportFunctionsRequest {
    string agent_id = 1;
    string service_name = 2;
    repeated FunctionInfo functions = 3;
}

message FunctionInfo {
    string function_name = 1;
    string package_name = 2;
    string file_path = 3;
    uint32 line_number = 4;
    uint64 offset = 5;
    bool is_exported = 6;
    bool has_dwarf = 7;
}

message ReportFunctionsResponse {
    uint32 registered_count = 1;
    uint32 updated_count = 2;
}
```

**Agent behavior:**

```go
// On SDK discovery, agent reports to Colony
func (a *Agent) onSDKDiscovered(service string, functions []FunctionInfo) {
resp, err := a.colonyClient.ReportFunctions(ctx, &ReportFunctionsRequest{
AgentId:     a.ID,
ServiceName: service,
Functions:   functions,
})

log.Printf("Registered %d functions for service %s", resp.RegisteredCount, service)
}
```

## Configuration Changes

### Colony Configuration

```yaml
# colony-config.yaml
colony:
    discovery:
        enabled: true

        # Function registry
        function_registry:
            max_functions_per_service: 50000
            retention_days: 30           # Keep function metadata for 30 days

        # Semantic search
        search:
            default_limit: 20
            max_limit: 50
            min_score: 0.1               # Filter out low-score matches

        # Call graph
        call_graph:
            enabled: true
            static_analysis: true        # Parse AST for call graph
            dynamic_analysis: true       # Observe calls via uprobes
            contribution_window: 1h      # Window for contribution estimates

        # Auto-context
        auto_context:
            enabled: true
            anomaly_window: 15m          # Look back 15 minutes for anomalies
            anomaly_threshold: 1.2       # 20% regression triggers anomaly
```

## Implementation Plan

### Phase 1: Function Registry

- [ ] Create DuckDB schema (functions, call_graph, function_metrics tables)
- [ ] Implement agent → colony function registration RPC
- [ ] Build function ingestion pipeline
- [ ] Add tokenization for searchability
- [ ] Create indexes for fast lookup

### Phase 2: Semantic Search

- [ ] Implement keyword-based search algorithm
- [ ] Add scoring and ranking logic
- [ ] Implement `coral_search_functions` MCP tool
- [ ] Add search result caching (5-minute TTL)
- [ ] Unit tests for search accuracy

### Phase 3: Call Graph Analysis

- [ ] Implement static call graph extraction (Go AST parsing)
- [ ] Implement dynamic call graph from uprobe data
- [ ] Build contribution estimation (% of time in callees)
- [ ] Implement `coral_get_function_context` MCP tool
- [ ] Add call graph visualization (optional)

### Phase 4: Auto-Context Injection

- [ ] Implement intent detection (performance vs error vs general)
- [ ] Implement service name extraction
- [ ] Build anomaly detection query
- [ ] Integrate context injection into MCP server
- [ ] Add context to LLM system prompt

### Phase 5: Testing & Optimization

- [ ] Load test with 50,000 function registry
- [ ] Validate search accuracy (precision/recall)
- [ ] Performance test: search latency <100ms
- [ ] Test call graph contribution estimates
- [ ] E2E test: AI discovers function in <3 tool calls

## Testing Strategy

### Search Accuracy Tests

**Precision & Recall:**

```go
func TestSearchAccuracy(t *testing.T) {
// Test data: Known queries and expected results
testCases := []struct {
query    string
expected []string
}{
{
query:    "checkout",
expected: []string{"handleCheckout", "processCheckout", "validateCheckout"},
},
{
query:    "payment processing",
expected: []string{"processPayment", "handlePayment", "validatePayment"},
},
}

for _, tc := range testCases {
results := SearchFunctions("api", tc.query, 10)

// Check precision: Are top results relevant?
precision := calculatePrecision(results, tc.expected)
assert.Greater(t, precision, 0.8, "Precision should be >80%")

// Check recall: Did we find all expected functions?
recall := calculateRecall(results, tc.expected)
assert.Greater(t, recall, 0.9, "Recall should be >90%")
}
}
```

### Performance Tests

**Search latency:**

```go
func BenchmarkSearch(b *testing.B) {
// Load 50,000 functions
loadFunctions(50000)

b.ResetTimer()
for i := 0; i < b.N; i++ {
SearchFunctions("api", "checkout payment", 20)
}

// Target: <100ms per search
}
```

### E2E Discovery Tests

**Simulate AI workflow:**

```go
func TestAIDiscoveryWorkflow(t *testing.T) {
// Simulate: "Why is checkout slow?"

// Step 1: Auto-context (Colony adds anomalies)
ctx := BuildContextForQuery("Why is checkout slow?")
assert.Contains(t, ctx["performance_anomalies"], "POST /api/checkout")

// Step 2: Semantic search
results := SearchFunctions("api", "checkout", 10)
assert.Contains(t, results[0].FunctionName, "handleCheckout")

// Step 3: Call graph
callCtx := GetFunctionContext("api", "main.handleCheckout")
assert.Greater(t, callCtx.Calls[0].EstimatedContribution, 0.8)

// Validation: Found bottleneck in ≤3 tool calls
assert.Equal(t, "main.processPayment", callCtx.Calls[0].FunctionName)
}
```

## Security Considerations

### Function Metadata Privacy

* **No sensitive data**: Function names, file paths, line numbers are not
  sensitive.
* **Access control**: Function registry inherits service RBAC (if user can't
  access service, can't search its functions).
* **No code exposure**: Registry only stores metadata, not function bodies.

### Search Query Logging

* **Audit logging**: All search queries logged with user identity.
* **Rate limiting**: Max 100 searches per user per minute (prevent abuse).
* **No PII in queries**: Queries are keywords, not data.

## Future Enhancements

### Vector Embeddings with DuckDB VSS (V2)

Replace keyword matching with semantic embeddings using **DuckDB's VSS extension
**.

**Why DuckDB VSS:**

- ✅ **No separate vector DB**: Everything stays in DuckDB
- ✅ **Fast ANN search**: HNSW indexing for approximate nearest neighbor
- ✅ **Built-in similarity functions**: `array_cosine_similarity()`,
  `array_distance()`
- ✅ **Transactional consistency**: Vector search + SQL queries in same DB
- ✅ **Cost-effective**: No external vector DB service (Pinecone, Weaviate, etc.)

#### Implementation with VSS Extension

**1. Schema with vector column:**

```sql
-- Install VSS extension
INSTALL
vss;
LOAD
vss;

-- Add embedding column to functions table
ALTER TABLE functions
    ADD COLUMN embedding FLOAT[384];
-- 384-dim for sentence-transformers/all-MiniLM-L6-v2

-- Create HNSW index for fast ANN search
CREATE INDEX idx_functions_embedding ON functions
    USING HNSW (embedding)
    WITH (metric = 'cosine');
```

**2. Generate embeddings (one-time + incremental):**

```go
// Generate embeddings using local model (no API costs)
import "github.com/nlpodyssey/spago/pkg/nlp/transformers/bert"

func generateFunctionEmbedding(fn Function) []float64 {
// Combine function metadata for rich embedding
text := fmt.Sprintf("%s %s %s %s",
fn.Name,        // "handleCheckout"
fn.PackageName, // "handlers"
fn.FilePath,       // "handlers/checkout.go"
fn.Comment,        // "// Handles checkout flow"
)

// Use local sentence-transformers model
// Model: sentence-transformers/all-MiniLM-L6-v2 (384 dims, 80MB)
embedding := embeddingModel.Encode(text)

return embedding // []float64 with 384 elements
}

// Batch insert embeddings
func UpdateFunctionEmbeddings(service string) error {
functions := getFunctionsForService(service)

for _, fn := range functions {
embedding := generateFunctionEmbedding(fn)

db.Exec(`
            UPDATE functions
            SET embedding = $1
            WHERE function_id = $2
        `, embedding, fn.ID)
}
}
```

**3. Vector search using DuckDB VSS:**

```go
func SearchWithEmbeddings(service, query string, limit int) ([]FunctionMatch, error) {
// 1. Generate query embedding
queryEmbedding := embeddingModel.Encode(query)

// 2. Use DuckDB VSS for ANN search
rows := db.Query(`
        SELECT
            function_id,
            function_name,
            file_path,
            array_cosine_similarity(embedding, $1::FLOAT[384]) AS similarity
        FROM functions
        WHERE service_name = $2
          AND embedding IS NOT NULL
        ORDER BY similarity DESC
        LIMIT $3
    `, queryEmbedding, service, limit)

// 3. Parse results
var matches []FunctionMatch
for rows.Next() {
var m FunctionMatch
rows.Scan(&m.FunctionID, &m.FunctionName, &m.FilePath, &m.Score)
matches = append(matches, m)
}

return matches, nil
}
```

**4. Hybrid search (keywords + vectors):**

```go
// Combine keyword matching (fast, reliable) with vector search (semantic)
func SearchFunctionsHybrid(service, query string, limit int) ([]FunctionMatch, error) {
// Run both searches in parallel
keywordResultsChan := make(chan []FunctionMatch)
vectorResultsChan := make(chan []FunctionMatch)

go func () {
keywordResultsChan <- SearchWithKeywords(service, query, limit*2)
}()

go func () {
vectorResultsChan <- SearchWithEmbeddings(service, query, limit*2)
}()

keywordResults := <-keywordResultsChan
vectorResults := <-vectorResultsChan

// Merge results using reciprocal rank fusion (RRF)
merged := reciprocalRankFusion(keywordResults, vectorResults, limit)

return merged, nil
}

// reciprocalRankFusion combines ranked lists
// RRF score = sum(1 / (rank + k)) where k=60 is typical
func reciprocalRankFusion(list1, list2 []FunctionMatch, limit int) []FunctionMatch {
const k = 60
scores := make(map[string]float64)

// Add RRF scores from first list
for rank, match := range list1 {
scores[match.FunctionID] += 1.0 / float64(rank+k)
}

// Add RRF scores from second list
for rank, match := range list2 {
scores[match.FunctionID] += 1.0 / float64(rank+k)
}

// Sort by combined score
var merged []FunctionMatch
for id, score := range scores {
merged = append(merged, FunctionMatch{
FunctionID: id,
Score:      score,
})
}

sort.Slice(merged, func (i, j int) bool {
return merged[i].Score > merged[j].Score
})

if len(merged) > limit {
merged = merged[:limit]
}

return merged
}
```

#### Performance Characteristics

**DuckDB VSS with HNSW:**

| Metric              | Value                                    |
|:--------------------|:-----------------------------------------|
| Index build time    | ~10s for 50,000 functions                |
| Query latency (ANN) | <50ms (vs >500ms brute-force)            |
| Storage overhead    | +150KB per 1,000 functions (384-dim)     |
| Precision@20        | ~95% (HNSW finds 19/20 true top results) |

**Embedding generation:**

| Model                         | Dimensions | Model Size | Latency/Query |
|:------------------------------|:-----------|:-----------|:--------------|
| all-MiniLM-L6-v2              | 384        | 80MB       | ~50ms (CPU)   |
| all-mpnet-base-v2             | 768        | 420MB      | ~100ms (CPU)  |
| OpenAI text-embedding-3-small | 1536       | API        | ~200ms + cost |

**Recommendation**: Use `all-MiniLM-L6-v2` (local, fast, good quality)

#### Example Queries

**Semantic matching (better than keywords):**

```
Query: "payment processing"

Keyword results:              Vector results (better):
1. processPayment (1.0)       1. processPayment (0.95)
2. paymentHandler (0.8)       2. handlePayment (0.93)
3. ??? (keyword miss)         3. validateCard (0.87)    ← semantic match!
                              4. chargeCustomer (0.85)   ← semantic match!
                              5. recordTransaction (0.82)← semantic match!
```

**Cross-language understanding:**

```
Query: "authentication"

Vector results:
1. handleAuth (0.94)
2. validateToken (0.91)       ← No "auth" in name, but semantically related
3. checkPermissions (0.88)    ← No "auth" in name, but semantically related
4. verifyCredentials (0.86)   ← Synonym understanding
```

#### Migration Path

**Phase 1 (V1)**: Keyword search only

- No embeddings needed
- Fast to implement
- Works immediately

**Phase 2 (V1.5)**: Pre-compute embeddings (background)

- Generate embeddings for all functions overnight
- Store in DuckDB but don't use yet
- Validate embedding quality

**Phase 3 (V2)**: Hybrid search (keywords + vectors)

- Use RRF to combine both approaches
- Best of both worlds (precision + semantic understanding)
- Fallback to keywords if embeddings unavailable

**Phase 4 (V2.5)**: Vector-first (optional)

- Use vectors as primary search
- Keywords only for exact matches
- Requires high confidence in embedding quality

#### Configuration

```yaml
# colony-config.yaml
colony:
    discovery:
        search:
            # Search strategy
            strategy: "hybrid"  # "keyword", "vector", "hybrid"

            # Vector search (requires VSS extension)
            vector:
                enabled: true
                model: "sentence-transformers/all-MiniLM-L6-v2"
                model_path: "/models/all-MiniLM-L6-v2"
                dimensions: 384

                # HNSW index parameters
                hnsw:
                    ef_construction: 200  # Build quality (higher = better but slower)
                    ef_search: 100        # Search quality (higher = better but slower)
                    m: 16                 # Connections per node

                # Hybrid search weights
                hybrid:
                    keyword_weight: 0.4
                    vector_weight: 0.6

            # Embedding generation
            embeddings:
                batch_size: 1000
                refresh_interval: 24h  # Re-generate embeddings daily
```

#### Benefits

✅ **Better semantic matching**: "payment processing" finds `validateCard`, not
just exact matches
✅ **Synonym understanding**: "authentication" finds `verifyCredentials`
✅ **Language-agnostic**: Works across Go, Python, JavaScript
✅ **No external dependencies**: Everything in DuckDB (no Pinecone, no Weaviate)
✅ **Fast ANN search**: HNSW index provides <50ms queries on 50,000 functions
✅ **Cost-effective**: Local embeddings (no OpenAI API costs)
✅ **Hybrid fallback**: Combine keywords + vectors for best precision

#### Trade-offs

⚠️ **Storage cost**: +150KB per 1,000 functions (manageable: 50K functions =
7.5MB)
⚠️ **Embedding latency**: Initial generation takes ~1-2min for 50K functions (
one-time cost)
⚠️ **Model deployment**: Need to deploy 80MB model with Colony (acceptable)
⚠️ **Reindexing**: When functions change, need to regenerate embeddings (
background job)

### Historical Trend Analysis

Track function performance over time:

```sql
-- Detect regressions
SELECT function_name,
       AVG(p95_latency_ms) OVER (ORDER BY timestamp ROWS BETWEEN 24 PRECEDING AND CURRENT ROW) AS avg_p95_24h, p95_latency_ms AS current_p95
FROM function_metrics
WHERE current_p95 > avg_p95_24h * 1.5 -- 50% slower than 24h average
```

**Use case:** Auto-suggest functions that recently regressed.

### Cross-Service Call Graph

Extend call graph across service boundaries using distributed tracing:

```sql
-- Trace calls across services
CREATE TABLE cross_service_calls
(
    caller_service  VARCHAR NOT NULL,
    caller_function VARCHAR NOT NULL,
    callee_service  VARCHAR NOT NULL,
    callee_endpoint VARCHAR NOT NULL,
    avg_latency_ms  FLOAT,
    p95_latency_ms  FLOAT
);
```

**Use case:** "Why is checkout slow?" → Discover it's waiting on external
payment service.

## Dependencies

- **RFD 059**: Live Debugging Architecture (provides call graph data)
- **RFD 060**: SDK Runtime Monitoring (provides function metadata)

## References

- Go AST parsing: https://pkg.go.dev/go/ast
- DuckDB full-text search: https://duckdb.org/docs/sql/functions/text
- Semantic search best practices: https://www.pinecone.io/learn/semantic-search/
