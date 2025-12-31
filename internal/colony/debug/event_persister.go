// Package debug provides debug session orchestration for the colony.
package debug

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"

	"github.com/coral-mesh/coral/internal/colony/database"
)

// EventPersister handles background event persistence for debug sessions.
type EventPersister struct {
	logger                  zerolog.Logger
	db                      *database.Database
	queryRouter             *QueryRouter
	stopBackgroundPersist   chan struct{}
	timestampsMu            sync.RWMutex
	lastPersistedTimestamps map[string]time.Time // sessionID -> last persisted event timestamp
}

// NewEventPersister creates a new event persister.
func NewEventPersister(
	logger zerolog.Logger,
	db *database.Database,
	queryRouter *QueryRouter,
) *EventPersister {
	return &EventPersister{
		logger:                  logger.With().Str("component", "event_persister").Logger(),
		db:                      db,
		queryRouter:             queryRouter,
		stopBackgroundPersist:   make(chan struct{}),
		lastPersistedTimestamps: make(map[string]time.Time),
	}
}

// Start begins background event persistence for all sessions.
// This ensures events are always in the database, even if DetachUprobe is never called.
func (ep *EventPersister) Start() {
	go ep.runBackgroundEventPersistence()
}

// runBackgroundEventPersistence continuously persists events from all active sessions.
func (ep *EventPersister) runBackgroundEventPersistence() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	ep.logger.Info().Msg("Started background event persistence for all debug sessions")

	for {
		select {
		case <-ticker.C:
			ep.persistEventsFromActiveSessions()
		case <-ep.stopBackgroundPersist:
			ep.logger.Info().Msg("Stopped background event persistence")
			return
		}
	}
}

// persistEventsFromActiveSessions queries and persists events from all active sessions.
func (ep *EventPersister) persistEventsFromActiveSessions() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get all active sessions from database.
	sessions, err := ep.db.ListDebugSessions(database.DebugSessionFilters{
		Status: "active",
	})
	if err != nil {
		ep.logger.Error().Err(err).Msg("Failed to list active sessions for background persistence")
		return
	}

	if len(sessions) == 0 {
		return
	}

	ep.logger.Debug().
		Int("session_count", len(sessions)).
		Msg("Persisting events from active sessions")

	persistedCount := 0
	for _, session := range sessions {
		// Skip expired sessions.
		if time.Now().After(session.ExpiresAt) {
			continue
		}

		// Query new events since last persistence.
		ep.timestampsMu.RLock()
		lastTime := ep.lastPersistedTimestamps[session.SessionID]
		ep.timestampsMu.RUnlock()

		queryReq := connect.NewRequest(&debugpb.QueryUprobeEventsRequest{
			SessionId: session.SessionID,
			StartTime: timestamppb.New(lastTime),
			MaxEvents: 10000,
		})

		queryResp, err := ep.queryRouter.QueryUprobeEvents(ctx, queryReq)
		if err != nil {
			ep.logger.Debug().
				Err(err).
				Str("session_id", session.SessionID).
				Msg("Failed to query events for background persistence")
			continue
		}

		if len(queryResp.Msg.Events) > 0 {
			// Persist new events to database.
			if err := ep.db.InsertDebugEvents(session.SessionID, queryResp.Msg.Events); err != nil {
				ep.logger.Error().
					Err(err).
					Str("session_id", session.SessionID).
					Int("event_count", len(queryResp.Msg.Events)).
					Msg("Failed to persist events in background")
			} else {
				persistedCount += len(queryResp.Msg.Events)
				// Update last persisted timestamp.
				lastEvent := queryResp.Msg.Events[len(queryResp.Msg.Events)-1]
				ep.timestampsMu.Lock()
				ep.lastPersistedTimestamps[session.SessionID] = lastEvent.Timestamp.AsTime()
				ep.timestampsMu.Unlock()
			}
		}
	}

	if persistedCount > 0 {
		ep.logger.Info().
			Int("event_count", persistedCount).
			Int("session_count", len(sessions)).
			Msg("Background event persistence completed")
	}
}

// Stop gracefully stops the event persister's background tasks.
func (ep *EventPersister) Stop() {
	close(ep.stopBackgroundPersist)
}
