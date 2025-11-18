---
rfd: "041"
title: "Shell Session Audit and Recording"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: false
dependencies: [ "026" ]
database_migrations: [ "shell_audit" ]
areas: [ "security", "compliance", "audit" ]
---

# RFD 041 - Shell Session Audit and Recording

**Status:** 🚧 Draft

## Summary

Implement comprehensive audit logging and session recording for
`coral agent shell` sessions to meet security compliance requirements. All shell
sessions with elevated agent privileges must be fully recorded (input/output),
stored durably, and queryable for security investigations and compliance audits.

## Problem

**Current limitations:**

RFD 026 implemented the `coral agent shell` command, which provides interactive
shell access to the agent's environment with elevated privileges (CRI socket,
eBPF, WireGuard mesh). However, there is currently no audit trail:

- **No session recording**: Input/output not captured, making security
  investigations impossible
- **No audit database**: Cannot query who accessed which agent, when, or what
  they did
- **No transcript playback**: Cannot review historical sessions for compliance
  or incident response
- **No retention policy**: No mechanism to enforce data retention or cleanup

**Why this matters:**

- **Security compliance**: SOC2, ISO 27001, and PCI-DSS require audit logging
  for privileged access
- **Incident response**: Security teams need to investigate suspicious shell
  activity
- **Forensics**: Post-incident analysis requires complete session transcripts
- **Deterrence**: Users behave more responsibly when they know sessions are
  recorded
- **Legal requirements**: Some industries require full audit trails for
  compliance

**Use cases affected:**

- Production shell access (SRE debugging infrastructure issues)
- Security incident investigations
- Compliance audits
- Training and review (what did the SRE do to fix the outage?)

## Solution

Implement a transparent session recording system that captures all shell I/O,
stores it in DuckDB with compression, and provides query/playback capabilities.

**Key Design Decisions:**

1. **Transparent recording**: Users see a warning, but recording happens
   automatically - no opt-out
2. **Local + central storage**: Agent stores locally (30 days), Colony
   aggregates long-term (90 days)
3. **Compressed storage**: Use gzip to reduce storage footprint for long
   sessions
4. **Timestamped entries**: Every I/O event includes nanosecond timestamp for
   accurate playback
5. **Metadata separation**: Session metadata (user, agent, duration) separate
   from transcript blobs

**Benefits:**

- **Full accountability**: Complete audit trail of all privileged shell access
- **Incident response**: Quickly identify what commands were run during an
  incident
- **Compliance**: Meet regulatory requirements for privileged access monitoring
- **Playback capability**: Review sessions with accurate timing (future
  enhancement)

**Architecture Overview:**

```
┌─────────────────────────────────────────────────────────┐
│ Shell Session (RFD 026)                                 │
└────────────┬────────────────────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────────────────────┐
│ Transcript Recorder (RFD 034)                           │
│ - Capture all I/O (stdin, stdout)                       │
│ - Timestamp each entry (nanosecond precision)           │
│ - Buffer in memory during session                       │
└────────────┬────────────────────────────────────────────┘
             │
             ▼ (on session end)
┌─────────────────────────────────────────────────────────┐
│ Compression & Storage                                   │
│ - Compress transcript (gzip)                            │
│ - Store in agent DuckDB (shell_audit table)             │
│ - Send to Colony for central aggregation                │
└─────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Agent Shell Handler** (`internal/agent/shell_handler.go`):
    - Add `TranscriptRecorder` to `ShellSession`
    - Record all PTY I/O with timestamps
    - Compress and store on session end

2. **Agent Database** (`internal/agent/database`):
    - New `shell_audit` table for session metadata + transcripts
    - Cleanup task for 30-day retention
    - Compression utilities (gzip)

3. **Colony Database** (`internal/colony/database`):
    - Aggregate `shell_audit` table (90-day retention)
    - Query endpoints for compliance audits
    - Transcript retrieval by session ID

4. **CLI** (`internal/cli/agent`):
    - Display audit session ID on exit
    - Optional: `coral agent shell-audit` command to query local sessions

## Database Schema

### Agent DuckDB Schema

```sql
CREATE TABLE shell_audit
(
    session_id      VARCHAR PRIMARY KEY,
    user_id         VARCHAR   NOT NULL,
    agent_id        VARCHAR   NOT NULL,
    started_at      TIMESTAMP NOT NULL,
    finished_at     TIMESTAMP,
    duration_ms     BIGINT,
    exit_code       INTEGER,
    transcript_size BIGINT, -- Compressed size in bytes
    transcript      BLOB,   -- gzip-compressed transcript
    approved        BOOLEAN   DEFAULT false,
    approver_id     VARCHAR,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_shell_audit_user ON shell_audit (user_id);
CREATE INDEX idx_shell_audit_started ON shell_audit (started_at DESC);
CREATE INDEX idx_shell_audit_agent ON shell_audit (agent_id);
```

### Colony DuckDB Schema

```sql
-- Same schema as agent, but aggregates from all agents
CREATE TABLE shell_audit
(
    session_id      VARCHAR PRIMARY KEY,
    user_id         VARCHAR   NOT NULL,
    agent_id        VARCHAR   NOT NULL,
    started_at      TIMESTAMP NOT NULL,
    finished_at     TIMESTAMP,
    duration_ms     BIGINT,
    exit_code       INTEGER,
    transcript_size BIGINT,
    transcript      BLOB,
    approved        BOOLEAN   DEFAULT false,
    approver_id     VARCHAR,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    synced_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- When received by Colony
);

CREATE INDEX idx_shell_audit_user ON shell_audit (user_id);
CREATE INDEX idx_shell_audit_started ON shell_audit (started_at DESC);
CREATE INDEX idx_shell_audit_agent ON shell_audit (agent_id);
CREATE INDEX idx_shell_audit_synced ON shell_audit (synced_at DESC);
```

## Transcript Format

### In-Memory Structure (During Session)

```go
type TranscriptEntry struct {
Timestamp time.Time // Nanosecond precision
Direction string    // "input" or "output"
Data      []byte // Raw bytes
}

type TranscriptRecorder struct {
entries   []TranscriptEntry
startTime time.Time
mu        sync.Mutex
}
```

### Compressed Storage Format (On Disk)

**Binary format (before gzip compression):**

```
[Header: 16 bytes]
  Magic:       4 bytes  (0x434F5348 = "COSH")
  Version:     2 bytes  (0x0001 = version 1)
  EntryCount:  4 bytes  (uint32, number of entries)
  Reserved:    6 bytes  (future use)

[Entries: variable length]
  For each entry:
    TimestampNs: 8 bytes  (int64, nanoseconds since session start)
    Direction:   1 byte   (0x01 = input, 0x02 = output)
    DataLength:  4 bytes  (uint32)
    Data:        N bytes  (actual I/O data)
```

Then entire binary format is gzip-compressed before storage.

## Implementation Plan

### Phase 1: Basic Recording Infrastructure

- [ ] Add `TranscriptRecorder` struct to agent package
- [ ] Integrate recorder into `ShellSession`
- [ ] Capture stdin/stdout in `streamOutput` and `processInput`
- [ ] Store entries with timestamps in memory

### Phase 2: Compression and Storage

- [ ] Implement binary transcript serialization
- [ ] Add gzip compression on session end
- [ ] Create `shell_audit` table in agent DuckDB
- [ ] Store compressed transcript and metadata

### Phase 3: Retention and Cleanup

- [ ] Add background cleanup task (delete sessions > 30 days)
- [ ] Add configuration for retention period
- [ ] Add disk usage monitoring/warnings

### Phase 4: Colony Synchronization

- [ ] Agent sends audit logs to Colony on session end
- [ ] Colony stores in aggregated `shell_audit` table
- [ ] Colony cleanup task (90-day retention)

### Phase 5: Query Interface

- [ ] CLI command: `coral agent shell-audit list`
- [ ] CLI command: `coral agent shell-audit show <session-id>`
- [ ] Colony RPC for audit queries

## Configuration Example

```yaml
agent:
    shell:
        audit:
            enabled: true                # Enable session recording (default: true)
            retention_days: 30           # Local retention (default: 30)
            compress: true               # Compress transcripts (default: true)
            sync_to_colony: true         # Send to Colony (default: true)
            max_transcript_size_mb: 100  # Warn if session exceeds (default: 100)
```

## Testing Strategy

### Unit Tests

- Transcript recording (capture I/O correctly)
- Compression/decompression round-trip
- Database insert/query operations
- Retention cleanup logic

### Integration Tests

- Full session: start → record → compress → store
- Verify transcript accuracy (input matches output)
- Test large sessions (> 10 MB of I/O)
- Test retention cleanup (delete old records)

### E2E Tests

- Run shell session → exit → verify audit record exists
- Query audit logs via CLI
- Verify Colony receives audit logs from agent

## Security Considerations

### Data Protection

- **Encryption at rest**: DuckDB files should be on encrypted volumes
- **Encryption in transit**: Agent → Colony sync uses TLS (WireGuard mesh)
- **Access control**: Only authorized users can query audit logs (RFD 035)

### Sensitive Data

- **PII/secrets in transcripts**: May contain passwords typed at prompts
    - **Mitigation**: Store as-is (encrypted at rest), warn users to avoid
      typing secrets
    - **Future enhancement**: Pattern-based redaction (detect `password:`
      prompts)

### Audit Trail Integrity

- **Tamper resistance**: Use DuckDB transaction isolation, consider checksums
- **Write-once**: Once written, audit logs should not be modifiable
- **Immutability**: No DELETE operations on audit table (only automated cleanup)

## Migration Strategy

1. **Deployment**:
    - Deploy agent with recording enabled (default on)
    - Create `shell_audit` table on agent startup (auto-migration)
    - Colony receives audit logs and stores centrally

2. **Backward compatibility**:
    - Old agents without recording continue to work (no audit)
    - Colony gracefully handles missing audit data from old agents

## Future Enhancements

**Deferred to later work:**

- **Playback functionality**: `coral agent shell-audit replay <session-id>`
    - Replay with accurate timing (like `asciinema`)
    - Export to asciinema format for sharing
- **Pattern-based redaction**: Detect `password:` prompts and redact input
- **Real-time streaming**: Stream audit logs to SIEM (Splunk, Elasticsearch)
- **Compliance reports**: Generate compliance reports (who accessed prod, when)
- **Alerting**: Alert on suspicious patterns (long sessions, unusual commands)

---

## Audit Query Examples

### Find all shell sessions by user

```sql
SELECT session_id,
       agent_id,
       started_at,
       duration_ms / 1000.0 AS duration_seconds,
       exit_code
FROM shell_audit
WHERE user_id = 'sre@company.com'
ORDER BY started_at DESC LIMIT 100;
```

### Find sessions in production

```sql
SELECT session_id,
       user_id,
       agent_id,
       started_at,
       duration_ms / 1000.0 AS duration_seconds
FROM shell_audit
WHERE agent_id LIKE 'prod-%'
ORDER BY started_at DESC;
```

### Find long-running sessions (> 1 hour)

```sql
SELECT session_id,
       user_id,
       agent_id,
       started_at,
       duration_ms / 3600000.0 AS duration_hours
FROM shell_audit
WHERE duration_ms > 3600000
ORDER BY duration_ms DESC;
```

### Get transcript for specific session

```sql
SELECT session_id,
       user_id,
       started_at,
       transcript
FROM shell_audit
WHERE session_id = 'sh-abc123';
-- Decompress transcript externally to view contents
```
