---
rfd: "063"
title: "Function Registry and Indexing Infrastructure"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "059", "060" ]
database_migrations: [ ]
areas: [ "infrastructure", "discovery", "search", "database" ]
---

# RFD 063 - Function Registry and Indexing Infrastructure

**Status:** 🚧 Draft

## Summary

Implement a centralized function registry in Colony that stores metadata for all
functions across monitored services. Agents discover functions via DWARF
introspection and report them to Colony, which indexes them in DuckDB with
support for semantic search and performance metrics correlation. This provides
the foundational infrastructure for AI-assisted debugging tools (defined in RFD 068).

## Problem

**Current behavior/limitations:**

- **No centralized function catalog**: Colony knows about services and endpoints
  but has no visibility into individual functions within those services
- **Cannot discover probe targets**: Users must know exact function names to
  attach uprobes, making debugging impossible on unfamiliar codebases
- **Cannot correlate metrics with code**: Have HTTP endpoint metrics but no way
  to map slow endpoints to the specific functions causing slowness
- **Scalability challenge**: Applications have 10,000-50,000+ functions;
  need efficient indexing and search
- **Multiple data sources**: Function metadata comes from DWARF info and runtime
  observations (uprobes) - need unified storage

**Why this matters:**

- **Enables AI debugging**: LLMs need to discover relevant functions from natural
  language queries ("find checkout functions")
- **Foundation for tooling**: Discovery tools, profiling orchestration, and
  bottleneck analysis all require a function registry
- **Performance correlation**: Connect endpoint-level metrics (RFD 025) with
  function-level behavior
- **Scalability**: Must handle 10,000+ functions per service efficiently

**Use cases affected:**
fastreverse

- **Function discovery**: "Find all functions related to checkout"
- **Performance attribution**: "Which function makes endpoint X slow?"
- **Uprobe targeting**: "What functions can I attach probes to?"
- **Metrics correlation**: "Show me functions with high latency"

## Solution

Build a **centralized function registry** in Colony with two main components:

### 1. Function Registry (DuckDB Storage)

Store function metadata in two tables:

**`functions` table:**
- Function identity (name, package, file, line, offset)
- Searchability tokens (tokenized name, file path for keyword search)
- DWARF availability (for uprobe targeting)
- Discovery timestamp (when first seen)

**`function_metrics` table:**
- Time-series performance data (P50/P95/P99, calls/min, error rate)
- Populated by uprobe sessions (RFD 059)
- Enables correlation with endpoint metrics (RFD 025)
- Supports baseline comparison for anomaly detection

**Agent → Colony Sync:**
- Agents introspect binaries using SDK metadata (RFD 060)
- Extract function list from DWARF debug info
- Report to Colony via `ReportFunctions` RPC on startup + periodically
- Uprobe sessions collect timing data, Colony stores in `function_metrics`

### 2. Query Infrastructure

**Semantic search:**
- Keyword-based search (V1, no ML required)
- Tokenize queries: "checkout payment" → ["checkout", "payment"]
- Score functions by matches in name (1.0), file path (0.5)
- Support regex patterns for advanced users
- Rank results by relevance + recent activity

**Unified query API:**
- Single SQL query with JOIN fetches:
    - Function metadata (name, file, line, offset, DWARF availability)
    - Metrics (if available from previous uprobe sessions)
    - Active probe status
- Optimized with indexes on function_name, service_name, token arrays
- Sub-100ms latency for 50,000 function registry

### Key Design Decisions

- **DuckDB for everything**: No separate vector database or search engine
- **DWARF-based discovery**: Extract function metadata from binary debug info (realistic for production)
- **Data availability tiers**: Function metadata always available, metrics conditional on instrumentation
- **Tokenized search**: Simple keyword matching (fast, deterministic, no ML)
- **Future-proof schema**: Designed to support vector embeddings (V2) via DuckDB VSS extension
- **Single-query retrieval**: All data fetched in one optimized SQL query

### Benefits

- **Foundation for AI debugging**: Enables semantic function discovery for LLMs
- **Scalable indexing**: Handles 10,000+ functions per service
- **Fast queries**: Sub-100ms search with proper indexes
- **Unified storage**: One source of truth for function metadata
- **Flexible querying**: Supports semantic search and regex patterns
- **Performance correlation**: Links endpoint metrics to specific functions
- **Extensible**: Schema supports future enhancements (embeddings, more metrics)

## Architecture Overview

### Data Flow: Function Registration

```
┌─────────────────────────────────────────────────────────────┐
│ Agent Startup / Service Deployment                          │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Agent: Function Discovery                                   │
│  1. Read binary DWARF debug info                            │
│  2. Extract function list (name, file, line, offset)        │
│  3. Check which functions have debug symbols                │
│  4. Tokenize names and file paths for search                │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Agent → Colony: ReportFunctions RPC                         │
│  Payload: [                                                 │
│    {name: "main.handleCheckout", file: "handlers/checkout.go",│
│     line: 45, offset: 0x4f8a20, has_dwarf: true},           │
│    ...                                                      │
│  ]                                                          │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony: Store in DuckDB functions table                     │
│  INSERT INTO functions (function_name, file_path, ...)      │
│  - Generate function_id (UUID)                              │
│  - Tokenize for search: "handleCheckout" → ["handle", "checkout"]│
│  - Index by service_name, function_name                     │
└─────────────────────────────────────────────────────────────┘

Registry now ready for queries!
```

### Data Flow: Runtime Metrics Collection

```
┌─────────────────────────────────────────────────────────────┐
│ Uprobe Session Completes (RFD 059)                          │
│  Function: processPayment, Duration: 60s                    │
│  Captured: 245 calls, P95: 850ms, P99: 1200ms              │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony: Aggregate and Store Metrics                         │
│  INSERT INTO function_metrics (                             │
│    function_id, timestamp, p50_latency_ms, p95_latency_ms,  │
│    calls_per_minute, error_rate                             │
│  )                                                          │
└─────────────────────────────────────────────────────────────┘

Metrics now available for discovery queries!
```

### Query API Usage

```
┌─────────────────────────────────────────────────────────────┐
│ Consumer (RFD 068 tools): Search Functions                  │
│  QueryFunctions(service="api", query="checkout", limit=20)  │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Colony: Execute Unified Query                               │
│  SELECT f.*, fm.*                                           │
│  FROM functions f                                           │
│  LEFT JOIN function_metrics fm ON f.function_id = fm.function_id│
│  WHERE service_name = 'api'                                 │
│    AND (function_name ILIKE '%checkout%'                    │
│         OR file_path ILIKE '%checkout%')                    │
│  ORDER BY [relevance score], [recent activity]              │
│  LIMIT 20                                                   │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│ Returns: Functions with Metadata and Metrics                │
│  [                                                          │
│    {                                                        │
│      function: "main.handleCheckout",                       │
│      file: "handlers/checkout.go:45",                       │
│      offset: "0x4f8a20",                                    │
│      has_dwarf: true,                                       │
│      metrics: {p95_ms: 900, ...} // if available           │
│    },                                                       │
│    ...                                                      │
│  ]                                                          │
└─────────────────────────────────────────────────────────────┘
```

## Database Schema

### DuckDB Tables

**`functions` table** - Stores function metadata for all services:
- Identity: `function_id` (PK), `service_name`, `function_name`, `package_name`
- Source location: `file_path`, `line_number`, `offset` (for uprobe targeting)
- Searchability: `name_tokens[]`, `file_tokens[]` (tokenized for keyword search)
- Metadata: `is_exported`, `has_dwarf`, `discovered_at`, `last_seen`
- Indexes: `service_name`, `function_name`, GIN index on token arrays

**`call_graph` table** - Stores caller/callee relationships:
- Edge: `caller_id`, `callee_id` (composite PK, references functions.function_id)
- Type: `call_type` (static from AST, dynamic from runtime observation)
- Contribution: `avg_contribution_pct`, `p95_contribution_pct` (% of caller's time)
- Timestamps: `observed_at`, `last_updated`

**`function_metrics` table** - Time-series performance data:
- Composite PK: `function_id`, `timestamp`
- Metrics: `p50_latency_ms`, `p95_latency_ms`, `p99_latency_ms`, `calls_per_minute`, `error_rate`
- Populated by uprobe sessions (RFD 059)
- Enables baseline comparison for anomaly detection

### Data Flow

**Function Registration:**
1. Agent discovers functions via DWARF introspection
2. Agent extracts call graph via AST parsing (Go: `go/packages`, `go/callgraph`)
3. Agent reports to Colony via `ReportFunctions` and `ReportCallGraph` RPCs
4. Colony tokenizes names/paths and stores in DuckDB

**Metrics Collection:**
1. Uprobe sessions (RFD 059) collect function timing data
2. Colony aggregates and inserts into `function_metrics`
3. Colony calculates contribution percentages and updates `call_graph`

## Query Infrastructure

### Semantic Search

**Approach:** Keyword-based matching (V1, no ML)
- Tokenize query: remove stop words, split on whitespace
- Score functions by matches:
  - Function name exact match: 1.0
  - Function name token match: 0.8
  - File path match: 0.5
  - File token match: 0.3
- Normalize by keyword count, sort by score
- Support regex patterns for advanced users

**Performance:** Sub-100ms for 50,000 functions with proper indexes

**Future (V2):** Vector embeddings via DuckDB VSS extension (see Future Enhancements)

### Unified Query API

**Design:** Single SQL query fetches all data via JOINs:
```sql
SELECT f.*, cg.*, fm.*
FROM functions f
LEFT JOIN call_graph cg ON ...
LEFT JOIN function_metrics fm ON ...
WHERE service_name = ? AND (function_name ILIKE ? OR file_path ILIKE ?)
ORDER BY [relevance_score], [recent_activity]
LIMIT ?
```

**Features:**
- Returns function metadata + call graph + metrics in one round-trip
- Clearly indicates data availability (static vs dynamic call graph, metrics presence)
- Optimized with indexes on `service_name`, `function_name`, token arrays

## API Changes

### Agent → Colony RPCs

New RPCs for agents to register function metadata with Colony:

**`ReportFunctions`** - Agent reports discovered functions
- Request: `agent_id`, `service_name`, array of `FunctionInfo` (name, file, line, offset, has_dwarf)
- Response: `registered_count`, `updated_count`
- Called on agent startup and periodically


### Internal Query API

Colony exposes internal API for querying the registry (consumed by RFD 071 tools):

**`QueryFunctions`** - Search functions and retrieve metadata
- Parameters: `service_name`, `query` (keyword or regex), `limit`, `include_metrics`
- Returns: Array of functions with optional performance metrics
- Implementation: Single SQL query with JOIN across functions and function_metrics tables

**`UpdateFunctionMetrics`** - Store metrics from uprobe sessions
- Parameters: `function_id`, `timestamp`, performance metrics (P50/P95/P99, calls/min, error rate)
- Called by uprobe session completion handler (RFD 059)

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

### Phase 1: Database Schema & Agent Sync

- Create DuckDB tables (functions, function_metrics)
- Implement Agent → Colony RPC (ReportFunctions)
- Build function metadata ingestion pipeline (DWARF introspection)
- Add tokenization logic for searchability
- Create indexes for performance

### Phase 2: Query Infrastructure

- Implement keyword-based search algorithm (tokenization, scoring, ranking)
- Build QueryFunctions API (unified query with JOIN)
- Implement UpdateFunctionMetrics API (called by uprobe sessions)
- Add query result caching for performance

### Phase 3: Testing & Optimization

- Load test with 50,000 function registry
- Validate search accuracy (precision/recall on known queries)
- Performance test: query latency <100ms
- Test metrics storage and retrieval

## Testing Strategy

### Unit Tests

**Search algorithm:**
- Tokenization correctness (stop word removal, case handling)
- Scoring accuracy (matches in function name vs file path)
- Ranking correctness (higher scores appear first)
- Edge cases (empty query, special characters, very long queries)


### Integration Tests

**Registry operations:**
- Function registration (agents → Colony sync)
- Duplicate handling (same function reported multiple times)
- Metrics storage and retrieval from uprobe sessions

### Performance Tests

**Search latency:** Target <100ms for 50,000 function registry
- Measure query execution time across varying database sizes
- Test with different query complexities (single keyword vs multiple)

**Indexing performance:** Verify indexes provide expected speedup
- Compare query times with/without indexes
- Measure impact of tokenization on insert performance

### Accuracy Tests

**Search precision/recall:** Target >80% precision, >90% recall
- Test with known queries and expected results
- Example: query "checkout" should return handleCheckout, processCheckout, validateCheckout

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

## Consumers

This RFD provides infrastructure that is consumed by:

- **RFD 068**: Function Discovery and Profiling Tools
    - Uses query API for semantic function search
    - Stores profiling results in function_metrics table
    - Updates call_graph with dynamic contribution percentages
    - Provides MCP tools and CLI commands for end users

Future consumers may include:
- Automated performance regression detection
- IDE integration for function-level observability
- Documentation generation with call graph visualization

## References

- Go AST parsing: https://pkg.go.dev/go/ast
- DuckDB full-text search: https://duckdb.org/docs/sql/functions/text
- Semantic search best practices: https://www.pinecone.io/learn/semantic-search/
