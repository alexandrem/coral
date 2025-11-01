---
rfd: "010"
title: "DuckDB Storage Initialization"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: false
dependencies: [ "002" ]
database_migrations: [ ]
areas: [ "storage", "database", "colony" ]
---

# RFD 010 - DuckDB Storage Initialization

**Status:** 🎉 Implemented

## Summary

Implement DuckDB storage initialization during colony startup to persist metrics
summaries, events, AI insights, service topology, and learned baselines. The
database file will be named `{colony_id}.duckdb` and stored in the configured
storage path, completing the dual `.coral` directory architecture where
`~/.coral/` holds credentials and `<project>/.coral/` holds runtime data.

## Problem

**Current behavior/limitations:**

- DuckDB storage layer is architecturally designed (see `docs/STORAGE.md`) but
  not implemented.
- TODO comment exists in `internal/cli/colony/colony.go:126`: "Initialize DuckDB
  storage at cfg.StoragePath".
- Storage path configuration is fully resolved (`internal/config/resolver.go`)
  but unused.
- Dual `.coral` directory design incomplete:
    - `~/.coral/` properly stores credentials and configuration.
    - `<project>/.coral/` contains only minimal `config.yaml`, missing runtime
      database.
- No persistence layer for:
    - Aggregated metrics (downsampled time-series data).
    - Event logs (deployments, crashes, alerts).
    - AI-generated insights and recommendations.
    - Service topology (auto-discovered connections).
    - Learned baselines (for anomaly detection).

**Why this matters:**

- **No operational history**: Colony cannot retain data across restarts, losing
  all context.
- **AI features blocked**: Insights, pattern detection, and recommendations
  require persistent storage.
- **Incomplete architecture**: The storage layer is a foundational component;
  its absence blocks multiple features.
- **User confusion**: Dual `.coral` directory structure appears incomplete (
  relative directory seems "useless" without database file).
- **Testing limitations**: Cannot test data retention, query performance, or
  schema migrations without storage implementation.

**Use cases affected:**

- Metric aggregation and downsampling (agents send raw data, colony stores
  summaries).
- Historical trend analysis (compare current metrics to learned baselines).
- AI-powered anomaly detection (requires historical data for pattern learning).
- Service topology visualization (maintain graph of service connections over
  time).
- Post-incident analysis (query event logs to reconstruct incidents).

## Solution

Implement DuckDB initialization during colony startup with a dedicated
`internal/colony/database` package handling schema creation, connection
management, and lifecycle operations.

**Key Design Decisions:**

- **Database filename: `{colony_id}.duckdb`**
    - Makes purpose immediately clear when inspecting storage directory.
    - Allows multiple colonies to coexist in same storage directory (future
      enhancement).
    - Example: `alex-dev-0977e1.duckdb` instead of generic `colony.duckdb`.

- **DuckDB over alternatives**
    - **Embedded database**: No separate server process, simplifies deployment.
    - **Columnar storage**: Optimized for analytical queries (time-series
      aggregations, metric summaries).
    - **SQL interface**: Familiar query language, excellent debugging
      experience.
    - **ACID guarantees**: Reliable persistence for critical operational data.
    - **Small footprint**: Low memory overhead, suitable for developer machines
      and small deployments.

- **Separate `internal/colony/database` package**
    - Encapsulates DuckDB-specific logic (connection, schema, migrations).
    - Provides clean API: `New()`, `Close()`, future query methods.
    - Testable in isolation (unit tests without full colony startup).
    - Preparation for future storage abstraction (if we support alternative
      backends).

- **Initialize early in colony startup**
    - After configuration resolution (need `cfg.StoragePath` and
      `cfg.ColonyID`).
    - Before WireGuard initialization (agents may send data immediately after
      connecting).
    - Before gRPC server startup (handlers will need database access).

- **Schema from `docs/STORAGE.md`**
    - Six core tables: `services`, `metric_summaries`, `events`, `insights`,
      `service_connections`, `baselines`.
    - Idempotent initialization: `CREATE TABLE IF NOT EXISTS` for safe restarts.
    - No migrations initially (fresh schema on first run).
    - Future: Track schema version for migration support.

**Benefits:**

- Completes foundational storage layer for all future features.
- Enables AI-powered insights and anomaly detection.
- Provides operational history and audit trail.
- Clarifies dual `.coral` directory architecture (credentials vs runtime data).
- Fast analytical queries over metrics and events (columnar storage advantage).

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────────────┐
│  User Home: ~/.coral/                                           │
│  (Credentials and Configuration)                                │
│                                                                  │
│  ├── config.yaml                    # Global settings           │
│  │   ├── default_colony: alex-dev-0977e1                        │
│  │   ├── discovery.endpoint                                     │
│  │   └── ai.provider                                            │
│  │                                                               │
│  └── colonies/                      # Colony credentials        │
│      └── alex-dev-0977e1.yaml                                   │
│          ├── colony_id: alex-dev-0977e1                         │
│          ├── colony_secret: [SECRET]                            │
│          ├── wireguard.private_key: [SECRET]                    │
│          └── storage_path: .coral   # Points to relative dir    │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  Project Directory: <project>/.coral/                           │
│  (Runtime Data and Operational State)                           │
│                                                                  │
│  ├── config.yaml                    # Project config (minimal)  │
│  │   └── colony_id: alex-dev-0977e1                             │
│  │                                                               │
│  └── alex-dev-0977e1.duckdb         # Runtime database ← NEW    │
│      ├── services                   # Service registry          │
│      ├── metric_summaries           # Aggregated metrics        │
│      ├── events                     # Event log                 │
│      ├── insights                   # AI recommendations        │
│      ├── service_connections        # Topology graph            │
│      └── baselines                  # Learned normal behavior   │
└─────────────────────────────────────────────────────────────────┘

                              ▲
                              │
                              │ Resolves storage_path
                              │ Constructs: {storage_path}/{colony_id}.duckdb
                              │
┌─────────────────────────────────────────────────────────────────┐
│  Colony Startup (internal/cli/colony/colony.go)                │
│                                                                  │
│  1. Resolve configuration                                       │
│     └─> cfg.StoragePath = <project>/.coral                      │
│     └─> cfg.ColonyID = alex-dev-0977e1                          │
│                                                                  │
│  2. Initialize DuckDB ← NEW                                     │
│     └─> db, err := database.New(cfg.StoragePath, cfg.ColonyID)  │
│     └─> Creates: <project>/.coral/alex-dev-0977e1.duckdb       │
│     └─> Runs: CREATE TABLE IF NOT EXISTS ... (6 tables)        │
│                                                                  │
│  3. Initialize WireGuard mesh                                   │
│  4. Start gRPC server (handlers use db)                         │
│  5. Register with discovery service                             │
└─────────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **New Package: `internal/colony/database`**

    - **`database.go`**: Core database lifecycle management.
        - `Database` struct: Wraps `*sql.DB` connection and metadata.
        -
        `New(storagePath, colonyID string, logger *slog.Logger) (*Database, error)`:
        Initialize database.
            - Construct path:
              `filepath.Join(storagePath, colonyID + ".duckdb")`.
            - Ensure storage directory exists: `os.MkdirAll(storagePath, 0755)`.
            - Open DuckDB connection: `sql.Open("duckdb", dbPath)`.
            - Call `initSchema()` to create tables.
            - Return `Database` instance.
        - `Close() error`: Close database connection gracefully.
        - `Ping(ctx context.Context) error`: Health check for database
          connection.

    - **`schema.go`**: Schema initialization and migrations.
        - `initSchema(db *sql.DB) error`: Execute DDL statements.
            - Create six tables: `services`, `metric_summaries`, `events`,
              `insights`, `service_connections`, `baselines`.
            - Use `CREATE TABLE IF NOT EXISTS` for idempotency.
            - Create indexes for common queries (service_id, timestamp, status).
            - Wrap in transaction for atomicity.

2. **Colony Startup** (`internal/cli/colony/colony.go`):

    - Import new `internal/colony/database` package.
    - After configuration resolution (line 123), initialize database:
      ```go
      db, err := database.New(cfg.StoragePath, cfg.ColonyID, logger)
      if err != nil {
          return fmt.Errorf("failed to initialize database: %w", err)
      }
      defer db.Close()
      ```
    - Log successful initialization:
      `logger.Info("database initialized", "path", dbPath)`.
    - Pass `db` to colony server constructor (may require updating server
      struct).

3. **Dependencies** (`go.mod`):

    - Add DuckDB driver: `github.com/marcboeker/go-duckdb`.
    - Pin version: `v1.7.0` (latest stable as of January 2025).

4. **Configuration** (`internal/config/schema.go`):

    - No changes needed; `StoragePath` field already exists.
    - Documentation update: Clarify that `storage_path` is resolved relative to
      project directory.

**Configuration Example:**

```yaml
# ~/.coral/colonies/my-shop-production.yaml (from RFD 002)
colony_id: my-shop-production
application_name: my-shop
environment: production
colony_secret: [ SECRET ]

# Storage configuration
storage_path: .coral  # Relative to project directory

# Results in database path:
# <project>/.coral/my-shop-production.duckdb
```

**Environment Variable Override:**

```bash
# Override storage path via environment variable
export CORAL_STORAGE_PATH=/var/lib/coral/storage

coral colony start
# Results in: /var/lib/coral/storage/my-shop-production.duckdb
```

## Implementation Plan

### Phase 1: Database Package Foundation

- [ ] Add `github.com/marcboeker/go-duckdb` to `go.mod`.
- [ ] Create `internal/colony/database/` directory structure.
- [ ] Implement `database.go`: `Database` struct, `New()`, `Close()`, `Ping()`.
- [ ] Add unit tests for `New()` (valid/invalid paths, directory creation).
- [ ] Add unit tests for connection lifecycle (open, ping, close).

### Phase 2: Schema Initialization

- [ ] Implement `schema.go`: `initSchema()` function.
- [ ] Define DDL statements for six tables (from `docs/STORAGE.md`).
- [ ] Add indexes for common query patterns (service_id, timestamp).
- [ ] Wrap schema creation in transaction for atomicity.
- [ ] Add unit tests for schema initialization (idempotency, table existence).
- [ ] Add unit tests for schema validation (query tables, check columns).

### Phase 3: Colony Integration

- [ ] Update `internal/cli/colony/colony.go`: Import database package.
- [ ] Add database initialization after config resolution (replace TODO at line
  126).
- [ ] Add defer statement for database cleanup: `defer db.Close()`.
- [ ] Add logging for database initialization (info level: success, error level:
  failures).
- [ ] Update colony server struct to accept `*database.Database` parameter (if
  needed).
- [ ] Pass database instance to server constructor.

### Phase 4: Testing & Validation

- [ ] Add integration test: Colony startup creates database file.
- [ ] Add integration test: Database file named `{colony_id}.duckdb`.
- [ ] Add integration test: Schema tables exist after startup.
- [ ] Add integration test: Colony restart reuses existing database (
  idempotency).
- [ ] Add integration test: Storage directory created if missing.
- [ ] Add integration test: Multiple colonies in same storage directory (
  different filenames).
- [ ] Manual test: Inspect database with DuckDB CLI (`duckdb <path>`).
- [ ] Update documentation: Add storage layer to architecture diagrams.

## Database Schema

**Complete schema as specified in `docs/STORAGE.md`:**

```sql
-- Services in the mesh
CREATE TABLE IF NOT EXISTS services
(
    id
    TEXT
    PRIMARY
    KEY,
    name
    TEXT
    NOT
    NULL,
    app_id
    TEXT
    NOT
    NULL,
    version
    TEXT,
    agent_id
    TEXT
    NOT
    NULL,
    labels
    TEXT, -- JSON string
    last_seen
    TIMESTAMP
    NOT
    NULL,
    status
    TEXT
    NOT
    NULL  -- running, stopped, error
);
CREATE INDEX IF NOT EXISTS idx_services_agent_id ON services(agent_id);
CREATE INDEX IF NOT EXISTS idx_services_status ON services(status);
CREATE INDEX IF NOT EXISTS idx_services_last_seen ON services(last_seen);

-- Aggregated metrics (downsampled)
CREATE TABLE IF NOT EXISTS metric_summaries
(
    timestamp
    TIMESTAMP
    NOT
    NULL,
    service_id
    TEXT
    NOT
    NULL,
    metric_name
    TEXT
    NOT
    NULL,
    interval
    TEXT
    NOT
    NULL, -- '5m', '15m', '1h', '1d'
    p50
    DOUBLE,
    p95
    DOUBLE,
    p99
    DOUBLE,
    mean
    DOUBLE,
    max
    DOUBLE,
    count
    INTEGER,
    PRIMARY
    KEY
(
    timestamp,
    service_id,
    metric_name,
    interval
)
    );
CREATE INDEX IF NOT EXISTS idx_metric_summaries_service_id ON metric_summaries(service_id);
CREATE INDEX IF NOT EXISTS idx_metric_summaries_metric_name ON metric_summaries(metric_name);

-- Event log (important events only)
CREATE TABLE IF NOT EXISTS events
(
    id
    INTEGER
    PRIMARY
    KEY,
    timestamp
    TIMESTAMP
    NOT
    NULL,
    service_id
    TEXT
    NOT
    NULL,
    event_type
    TEXT
    NOT
    NULL, -- deploy, crash, restart, alert, connection
    details
    TEXT, -- JSON string
    correlation_group
    TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_service_id ON events(service_id);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_correlation ON events(correlation_group);

-- AI-generated insights
CREATE TABLE IF NOT EXISTS insights
(
    id
    INTEGER
    PRIMARY
    KEY,
    created_at
    TIMESTAMP
    NOT
    NULL,
    insight_type
    TEXT
    NOT
    NULL, -- anomaly, pattern, recommendation
    priority
    TEXT
    NOT
    NULL, -- high, medium, low
    title
    TEXT
    NOT
    NULL,
    summary
    TEXT
    NOT
    NULL,
    details
    TEXT, -- JSON string
    affected_services
    TEXT, -- JSON array
    status
    TEXT
    NOT
    NULL, -- active, dismissed, resolved
    confidence
    DOUBLE,
    expires_at
    TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_insights_status ON insights(status);
CREATE INDEX IF NOT EXISTS idx_insights_priority ON insights(priority);
CREATE INDEX IF NOT EXISTS idx_insights_created_at ON insights(created_at);

-- Service topology (auto-discovered)
CREATE TABLE IF NOT EXISTS service_connections
(
    from_service
    TEXT
    NOT
    NULL,
    to_service
    TEXT
    NOT
    NULL,
    protocol
    TEXT
    NOT
    NULL,
    first_observed
    TIMESTAMP
    NOT
    NULL,
    last_observed
    TIMESTAMP
    NOT
    NULL,
    connection_count
    INTEGER
    NOT
    NULL,
    PRIMARY
    KEY
(
    from_service,
    to_service,
    protocol
)
    );
CREATE INDEX IF NOT EXISTS idx_service_connections_from ON service_connections(from_service);
CREATE INDEX IF NOT EXISTS idx_service_connections_to ON service_connections(to_service);

-- Learned baselines
CREATE TABLE IF NOT EXISTS baselines
(
    service_id
    TEXT
    NOT
    NULL,
    metric_name
    TEXT
    NOT
    NULL,
    time_window
    TEXT
    NOT
    NULL, -- '1h', '1d', '7d'
    mean
    DOUBLE,
    stddev
    DOUBLE,
    p50
    DOUBLE,
    p95
    DOUBLE,
    p99
    DOUBLE,
    sample_count
    INTEGER,
    last_updated
    TIMESTAMP
    NOT
    NULL,
    PRIMARY
    KEY
(
    service_id,
    metric_name,
    time_window
)
    );
CREATE INDEX IF NOT EXISTS idx_baselines_service_id ON baselines(service_id);
CREATE INDEX IF NOT EXISTS idx_baselines_metric_name ON baselines(metric_name);
```

## Testing Strategy

### Unit Tests

**`internal/colony/database/database_test.go`:**

- `TestNew_ValidPath`: Database creation succeeds with valid storage path.
- `TestNew_CreatesDirectory`: Storage directory created if missing.
- `TestNew_InvalidPath`: Error returned for invalid path (e.g.,
  `/nonexistent/readonly/`).
- `TestNew_DatabaseFilename`: Database file named `{colony_id}.duckdb`.
- `TestClose`: Connection closes without errors.
- `TestPing`: Health check succeeds after initialization.

**`internal/colony/database/schema_test.go`:**

- `TestInitSchema_TablesCreated`: All six tables exist after initialization.
- `TestInitSchema_Idempotency`: Running `initSchema()` twice succeeds without
  errors.
- `TestInitSchema_Indexes`: Indexes created for common query patterns.
- `TestInitSchema_ColumnTypes`: Verify column types match schema (TEXT,
  TIMESTAMP, DOUBLE).
- `TestInitSchema_PrimaryKeys`: Verify primary key constraints exist.

### Integration Tests

**`internal/cli/colony/colony_test.go`:**

- `TestColonyStart_InitializesDatabase`: Colony startup creates database file at
  correct path.
- `TestColonyStart_DatabaseFilename`: Database file named `{colony_id}.duckdb`.
- `TestColonyStart_SchemaCreated`: Query tables to verify schema exists.
- `TestColonyStart_Idempotency`: Restarting colony reuses existing database
  without errors.
- `TestColonyStart_StorageDirectoryCreation`: Storage directory created if
  missing.
- `TestColonyStart_EnvironmentOverride`: `CORAL_STORAGE_PATH` overrides config.

**Multi-Colony Test:**

- Create two colonies with different IDs in same storage directory.
- Verify two separate database files created (`colony-a.duckdb`,
  `colony-b.duckdb`).
- Verify no conflicts or data corruption.

### Manual Validation

**Inspect Database with DuckDB CLI:**

```bash
# Start colony
coral colony start

# Inspect database in another terminal
duckdb ~/.coral/storage/alex-dev-0977e1.duckdb

# Verify schema
D SHOW TABLES;
D DESCRIBE services;
D DESCRIBE metric_summaries;
D DESCRIBE events;
D DESCRIBE insights;
D DESCRIBE service_connections;
D DESCRIBE baselines;

# Verify indexes
D SELECT * FROM duckdb_indexes();

# Exit
D .quit
```

## Security Considerations

### Database File Permissions

- **File creation**: Database files created with default Go permissions (0644).
- **Directory permissions**: Storage directory created with 0755 (readable by
  others, writable by owner).
- **Mitigation**: Colony secrets and WireGuard keys stored separately in
  `~/.coral/colonies/` with stricter permissions (0600).
- **Recommendation**: Database files can be world-readable (contain only
  operational data, no secrets).

### Data Exposure

- **Stored data**: Metrics, events, service names, topology.
- **No secrets**: Database contains no passwords, API keys, or cryptographic
  material.
- **Service IDs**: May leak internal service names (e.g., "payment-service", "
  auth-api").
- **Mitigation**: Storage path typically in project directory (not shared across
  users).

### SQL Injection

- **Risk**: SQL injection if user input concatenated into queries.
- **Mitigation**: Use parameterized queries exclusively (prepared statements).
- **Validation**: Sanitize service IDs, metric names before storage.

### Backup and Recovery

- **Database portability**: DuckDB uses single-file format (easy backup).
- **Backup strategy**: Users can copy `{colony_id}.duckdb` for backups.
- **Restoration**: Copy database file back to storage path.
- **Future enhancement**: Add `coral colony backup` and `coral colony restore`
  commands.

## Migration Strategy

**New Installations:**

1. `coral init` generates colony configuration (existing behavior).
2. `coral colony start` automatically creates database on first run.
3. No user intervention required; storage layer transparent.

**Existing Installations:**

- Storage layer currently not implemented; no existing databases to migrate.
- First startup after this RFD implementation creates database.
- Backward compatible: Colony configuration unchanged.

**Testing During Development:**

- Feature flag not required (no existing behavior to preserve).
- Development testing: Use separate storage paths for test colonies.
- CI/CD: Run tests in isolated temporary directories.

**Rollback Plan:**

- If database initialization fails, colony startup fails with clear error
  message.
- User can delete database file and restart (schema recreated).
- No cascading failures: Other components (WireGuard, gRPC) independent of
  storage.

## Future Enhancements

### Query API

**Expose database queries via internal package:**

```go
// internal/colony/database/queries.go

func (db *Database) GetRecentEvents(ctx context.Context, serviceID string, limit int) ([]Event, error)
func (db *Database) InsertMetricSummary(ctx context.Context, summary *MetricSummary) error
func (db *Database) GetActiveInsights(ctx context.Context) ([]Insight, error)
func (db *Database) GetServiceTopology(ctx context.Context) (*Topology, error)
```

**Benefits:**

- Encapsulate SQL queries within database package.
- Type-safe API for colony components.
- Easier testing with mock database interface.

### Schema Migrations

**Track schema version for future changes:**

```sql
CREATE TABLE IF NOT EXISTS schema_version
(
    version
    INTEGER
    PRIMARY
    KEY,
    applied_at
    TIMESTAMP
    NOT
    NULL
);
```

**Migration system:**

- Store migrations as numbered SQL files (`001_initial.sql`,
  `002_add_index.sql`).
- Apply migrations sequentially on startup if schema version outdated.
- Record applied migrations in `schema_version` table.

### Data Retention Policies

**Automatic cleanup of old data:**

```sql
-- Delete metric summaries older than 30 days
DELETE
FROM metric_summaries
WHERE timestamp < NOW() - INTERVAL 30 DAYS;

-- Delete resolved insights older than 7 days
DELETE
FROM insights
WHERE status = 'resolved'
  AND expires_at < NOW();
```

**Configuration:**

```yaml
storage:
    retention:
        metric_summaries: 30d
        events: 90d
        insights: 7d
```

### Multi-Colony Support

**Store multiple colonies in same database:**

- Add `colony_id` column to all tables.
- Single database file: `coral.duckdb` (not per-colony).
- Benefits: Cross-colony queries, centralized storage.
- Trade-off: More complex schema, larger database file.

**Decision**: Start with per-colony databases (current design). Consolidation
can be added later if needed.

### Backup and Restore CLI Commands

```bash
# Backup database
coral colony backup my-shop-prod --output /backups/my-shop-prod-2025-01-15.duckdb

# Restore database
coral colony restore my-shop-prod --input /backups/my-shop-prod-2025-01-15.duckdb

# Export to CSV for external analysis
coral colony export metrics --format csv --output metrics.csv
```

### DuckDB Analytics Features

**Leverage DuckDB's analytical capabilities:**

- **Window functions**: Calculate moving averages over metric summaries.
- **Time-series functions**: `range()`, `generate_series()` for time bucketing.
- **JSON functions**: Query nested JSON fields in `details` columns.
- **Parquet export**: Export large datasets for external analysis.

**Example query:**

```sql
-- Calculate 7-day moving average for response time
SELECT
    timestamp, service_id, metric_name, mean, AVG (mean) OVER (
    PARTITION BY service_id, metric_name
    ORDER BY timestamp
    ROWS BETWEEN 6 PRECEDING AND CURRENT ROW
    ) AS moving_avg_7d
FROM metric_summaries
WHERE metric_name = 'http_response_time'
ORDER BY timestamp DESC;
```

## Appendix

### DuckDB Go Driver Details

**Library:** `github.com/marcboeker/go-duckdb`

**Connection String:**

```go
// In-memory database (for testing)
db, err := sql.Open("duckdb", ":memory:")

// File-based database
db, err := sql.Open("duckdb", "/path/to/database.duckdb")

// Read-only mode
db, err := sql.Open("duckdb", "/path/to/database.duckdb?access_mode=read_only")
```

**Connection Pooling:**

```go
db, err := sql.Open("duckdb", dbPath)
if err != nil {
return nil, err
}

// Configure connection pool
db.SetMaxOpenConns(10)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(time.Hour)
```

**Transaction Example:**

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
return err
}
defer tx.Rollback()

// Execute statements
_, err = tx.ExecContext(ctx, "INSERT INTO services ...")
if err != nil {
return err
}

// Commit transaction
return tx.Commit()
```

### Alternative Databases Considered

| Database   | Type          | Pros                                       | Cons                                   |
|------------|---------------|--------------------------------------------|----------------------------------------|
| DuckDB     | Embedded      | Columnar, analytical, SQL, small footprint | Less mature Go driver than SQLite      |
| SQLite     | Embedded      | Mature, ubiquitous, excellent Go support   | Row-oriented (slower for aggregations) |
| PostgreSQL | Client-Server | Production-ready, rich features            | Requires separate server process       |
| BadgerDB   | Embedded      | Pure Go, fast key-value operations         | No SQL, manual indexing required       |

**Decision**: DuckDB chosen for analytical workload optimization (metric
summaries, time-series queries) and SQL familiarity.

### Storage Path Resolution Examples

**Example 1: Default Configuration**

```yaml
# ~/.coral/colonies/alex-dev-0977e1.yaml
storage_path: .coral
```

**Result**: `<current-directory>/.coral/alex-dev-0977e1.duckdb`

**Example 2: Absolute Path**

```yaml
# ~/.coral/colonies/alex-dev-0977e1.yaml
storage_path: /var/lib/coral/storage
```

**Result**: `/var/lib/coral/storage/alex-dev-0977e1.duckdb`

**Example 3: Environment Variable Override**

```bash
export CORAL_STORAGE_PATH=/tmp/coral-test
coral colony start
```

**Result**: `/tmp/coral-test/alex-dev-0977e1.duckdb`

**Example 4: Project-Specific Override**

```yaml
# <project>/.coral/config.yaml
colony_id: alex-dev-0977e1
storage:
    path: ./data/coral
```

**Result**: `<project>/data/coral/alex-dev-0977e1.duckdb`

### Reference Implementations

**DuckDB in Go Projects:**

- **Steampipe**: Analytics tool using DuckDB for query caching.
    - Reference: https://github.com/turbot/steampipe
    - Pattern: DuckDB for analytical queries over API data.

- **Benthos**: Stream processor using DuckDB for aggregations.
    - Reference: https://github.com/benthosdev/benthos
    - Pattern: DuckDB for real-time metric aggregation.

**Similar Storage Architectures:**

- **Prometheus**: TSDB (time-series database) for metrics.
    - Reference: https://github.com/prometheus/prometheus
    - Pattern: Local storage, downsampling, retention policies.

- **Grafana Loki**: Log aggregation with embedded storage.
    - Reference: https://github.com/grafana/loki
    - Pattern: Embedded database, per-tenant storage files.

### Database File Structure

**DuckDB file format:**

- Single file containing all tables, indexes, and metadata.
- Binary format (not human-readable).
- Portable across platforms (can copy between macOS/Linux/Windows).

**Typical file sizes:**

- Empty database: ~100 KB (schema only).
- 1 day of data (100 services, 1000 metrics): ~10 MB.
- 30 days of data: ~300 MB (with downsampling).

**Compression:**

- DuckDB uses automatic compression (dictionary encoding, RLE).
- Typically 5-10x smaller than equivalent CSV files.

### Testing Configuration Example

```yaml
# Test configuration for integration tests
storage_path: /tmp/coral-test-storage

# Results in:
# /tmp/coral-test-storage/test-colony-12345.duckdb

# Cleanup after tests:
# rm -rf /tmp/coral-test-storage
```

---

## Notes

**Why Now:**

- Storage layer is foundational; delaying it blocks AI features, metrics
  aggregation, and operational history.
- Configuration infrastructure already complete (RFD 002); database
  initialization is natural next step.
- Current TODO comment at `colony.go:126` marks exact integration point; minimal
  refactoring needed.

**Implementation Complexity:**

- **Low to Medium**: Schema straightforward, DuckDB driver mature, no complex
  migrations.
- **Risk**: Database file corruption (mitigated by ACID guarantees, periodic
  backups).

**Relationship to Other RFDs:**

- **RFD 002**: Depends on colony configuration schema and storage path
  resolution.
- **Future RFDs**: Enables AI insights, metrics aggregation, topology
  visualization.

**Alternative Considered: SQLite**

- **Pros**: More mature Go driver, ubiquitous, better concurrency (WAL mode).
- **Cons**: Row-oriented storage suboptimal for analytical queries (metric
  aggregations).
- **Decision**: DuckDB's columnar storage worth the trade-off for time-series
  workloads.
