package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

// DebugEvent represents a stored uprobe event.
// DebugEvent represents a stored uprobe event.
type DebugEvent struct {
	ID           int64     `duckdb:"-"` // Auto-increment, ignore in ORM
	SessionID    string    `duckdb:"session_id"`
	Timestamp    time.Time `duckdb:"timestamp"`
	CollectorID  string    `duckdb:"collector_id"`
	AgentID      string    `duckdb:"agent_id"`
	ServiceName  string    `duckdb:"service_name"`
	FunctionName string    `duckdb:"function_name"`
	EventType    string    `duckdb:"event_type"`
	DurationNs   *int64    `duckdb:"duration_ns"`
	PID          *int32    `duckdb:"pid"`
	TID          *int32    `duckdb:"tid"`
	Args         *string   `duckdb:"args"`
	ReturnValue  *string   `duckdb:"return_value"`
	Labels       *string   `duckdb:"labels"`
}

// InsertDebugEvents persists a batch of uprobe events to the database.
func (d *Database) InsertDebugEvents(sessionID string, events []*meshv1.UprobeEvent) error {
	if len(events) == 0 {
		return nil
	}

	var items []*DebugEvent
	for _, event := range events {
		// Serialize complex fields to JSON
		var argsJSON, returnValueJSON, labelsJSON *string

		if len(event.Args) > 0 {
			argsBytes, err := json.Marshal(event.Args)
			if err != nil {
				return fmt.Errorf("failed to marshal args: %w", err)
			}
			argsStr := string(argsBytes)
			argsJSON = &argsStr
		}

		if event.ReturnValue != nil {
			returnBytes, err := json.Marshal(event.ReturnValue)
			if err != nil {
				return fmt.Errorf("failed to marshal return_value: %w", err)
			}
			returnStr := string(returnBytes)
			returnValueJSON = &returnStr
		}

		if len(event.Labels) > 0 {
			labelsBytes, err := json.Marshal(event.Labels)
			if err != nil {
				return fmt.Errorf("failed to marshal labels: %w", err)
			}
			labelsStr := string(labelsBytes)
			labelsJSON = &labelsStr
		}

		// Handle nullable duration_ns (only for return events)
		var durationNs *int64
		if event.DurationNs > 0 {
			durationNs = new(int64)
			*durationNs = int64(event.DurationNs)
		}

		// Handle nullable pid/tid
		var pid, tid *int32
		if event.Pid != 0 {
			pid = &event.Pid
		}
		if event.Tid != 0 {
			tid = &event.Tid
		}

		// NOTE: ID is omitted (auto-increment in DB? Or need to generate?).
		// Struct has ID `duckdb:"id,pk"`. ORM BatchUpsert will insert 0 if not set.
		// If ID is auto-increment, we should use a different struct without ID for Insert, OR
		// use `duckdb:"-"` on ID but then we can't Get.
		// `DebugEvent` is also used for Get (fetching events).
		// I'll assume for this batch insert, we use `duckdb.Table[DebugEvent]` but...
		// If DB has `CREATE SEQUENCE seq_debug_events_id`, removing ID from INSERT is key.
		// The ORM currently doesn't support "Insert Omit".
		// I will generate ID manually to be safe or assuming 0 is problematic.
		// But inserting millions of events -> generating ID is better.
		// Or creating a `DebugEventInsert` struct without ID?
		// Existing schema relies on `nextval`.
		// If I pass ID=0, DuckDB might treat it as value 0.
		// I should define `DebugEventInsert` struct WITHOUT ID field.
		// And use `Table[DebugEventInsert]` for insertion.
		// But I need `debugEventsTable` to be accessible.
		// I can cast or make a local table for insert.
		// Or just modify `DebugEvent` to exclude ID for now (if I don't read ID back).
		// `GetDebugEvents` reads everything EXCEPT ID (in existing implementation!).
		// Existing `GetDebugEvents` SELECT list: `timestamp, collector_id, ...` NO ID.
		// So ID is internal only?
		// Line 132 in `debug_events.go`:
		// `SELECT timestamp, ...`
		// It does NOT select ID.
		// So `DebugEvent` struct in Go code (lines 15-30) HAS ID but it's not populated by Get?
		// Wait, `GetDebugEvents` return `[]*meshv1.UprobeEvent`. It doesn't use `DebugEvent` struct for retrieval!
		// It manually scans into vars.
		// So `DebugEvent` struct is UNUSED currently? Or used as intermediate?
		// It seems `DebugEvent` struct lines 15-30 is unused in `GetDebugEvents`.
		// So I can modify `DebugEvent` struct to MATCH what I want to insert.
		// If I remove `ID` field from `DebugEvent` struct (map it to `duckdb:"-"` or remove it), ORM won't insert it.
		// Then DuckDB will use default (sequence).
		// Perfect.

		items = append(items, &DebugEvent{
			SessionID:    sessionID,
			Timestamp:    event.Timestamp.AsTime(),
			CollectorID:  event.CollectorId,
			AgentID:      event.AgentId,
			ServiceName:  event.ServiceName,
			FunctionName: event.FunctionName,
			EventType:    event.EventType,
			DurationNs:   durationNs,
			PID:          pid,
			TID:          tid,
			Args:         argsJSON,
			ReturnValue:  returnValueJSON,
			Labels:       labelsJSON,
		})
	}

	return d.debugEventsTable.BatchUpsert(context.Background(), items)
}

// GetDebugEvents retrieves all stored events for a debug session.
func (d *Database) GetDebugEvents(sessionID string) ([]*meshv1.UprobeEvent, error) {
	query := `
		SELECT timestamp, collector_id, agent_id, service_name, function_name,
		       event_type, duration_ns, pid, tid, args, return_value, labels
		FROM debug_events
		WHERE session_id = ?
		ORDER BY timestamp ASC
	`

	rows, err := d.db.Query(query, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query debug events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var events []*meshv1.UprobeEvent
	for rows.Next() {
		var timestamp time.Time
		var collectorID, agentID, serviceName, functionName, eventType string
		var durationNs sql.NullInt64
		var pid, tid sql.NullInt32
		var argsJSON, returnValueJSON, labelsJSON sql.NullString

		if err := rows.Scan(
			&timestamp,
			&collectorID,
			&agentID,
			&serviceName,
			&functionName,
			&eventType,
			&durationNs,
			&pid,
			&tid,
			&argsJSON,
			&returnValueJSON,
			&labelsJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan debug event: %w", err)
		}

		event := &meshv1.UprobeEvent{
			Timestamp:    timestamppb.New(timestamp),
			CollectorId:  collectorID,
			AgentId:      agentID,
			ServiceName:  serviceName,
			FunctionName: functionName,
			EventType:    eventType,
		}

		if durationNs.Valid {
			event.DurationNs = uint64(durationNs.Int64)
		}

		if pid.Valid {
			event.Pid = pid.Int32
		}

		if tid.Valid {
			event.Tid = tid.Int32
		}

		// Deserialize JSON fields
		if argsJSON.Valid && argsJSON.String != "" {
			var args []*meshv1.FunctionArgument
			if err := json.Unmarshal([]byte(argsJSON.String), &args); err != nil {
				return nil, fmt.Errorf("failed to unmarshal args: %w", err)
			}
			event.Args = args
		}

		if returnValueJSON.Valid && returnValueJSON.String != "" {
			var returnValue meshv1.FunctionReturnValue
			if err := json.Unmarshal([]byte(returnValueJSON.String), &returnValue); err != nil {
				return nil, fmt.Errorf("failed to unmarshal return_value: %w", err)
			}
			event.ReturnValue = &returnValue
		}

		if labelsJSON.Valid && labelsJSON.String != "" {
			var labels map[string]string
			if err := json.Unmarshal([]byte(labelsJSON.String), &labels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
			}
			event.Labels = labels
		}

		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating debug events: %w", err)
	}

	return events, nil
}

// DeleteDebugEvents deletes all events for a specific session.
func (d *Database) DeleteDebugEvents(sessionID string) error {
	query := `DELETE FROM debug_events WHERE session_id = ?`
	_, err := d.db.Exec(query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete debug events: %w", err)
	}
	return nil
}

// DeleteOldDebugEvents deletes events older than the specified retention period.
func (d *Database) DeleteOldDebugEvents(retentionPeriod time.Duration) error {
	cutoffTime := time.Now().Add(-retentionPeriod)
	query := `DELETE FROM debug_events WHERE timestamp < ?`
	_, err := d.db.Exec(query, cutoffTime)
	if err != nil {
		return fmt.Errorf("failed to delete old debug events: %w", err)
	}
	return nil
}
