package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

func TestFilterEvents(t *testing.T) {
	now := time.Now()
	events := []*meshv1.EbpfEvent{
		{Timestamp: timestamppb.New(now.Add(-10 * time.Minute))}, // Oldest
		{Timestamp: timestamppb.New(now.Add(-5 * time.Minute))},
		{Timestamp: timestamppb.New(now.Add(-1 * time.Minute))}, // Newest
	}

	tests := []struct {
		name      string
		req       *agentv1.QueryUprobeEventsRequest
		events    []*meshv1.EbpfEvent
		wantCount int
		wantFirst *timestamppb.Timestamp
	}{
		{
			name:      "No limits",
			req:       &agentv1.QueryUprobeEventsRequest{},
			events:    events,
			wantCount: 3,
			wantFirst: events[0].Timestamp,
		},
		{
			name: "Max events (current behavior - returns oldest)",
			req: &agentv1.QueryUprobeEventsRequest{
				MaxEvents: 2,
			},
			events:    events,
			wantCount: 2,
			wantFirst: events[0].Timestamp, // Currently returns oldest
		},
		{
			name: "Max events with StartTime (streaming)",
			req: &agentv1.QueryUprobeEventsRequest{
				MaxEvents: 2,
				StartTime: timestamppb.New(now.Add(-6 * time.Minute)),
			},
			events:    events,
			wantCount: 2,
			wantFirst: events[1].Timestamp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterEvents(tt.events, tt.req)
			assert.Len(t, got, tt.wantCount)
			if len(got) > 0 {
				assert.Equal(t, tt.wantFirst, got[0].Timestamp)
			}
		})
	}
}

// Copy of the logic from debug_service.go for testing purposes before refactoring
func filterEvents(events []*meshv1.EbpfEvent, req *agentv1.QueryUprobeEventsRequest) []*meshv1.EbpfEvent {
	var filteredEvents []*meshv1.EbpfEvent
	for _, event := range events {
		// Check time range
		if req.StartTime != nil && event.Timestamp.AsTime().Before(req.StartTime.AsTime()) {
			continue
		}
		if req.EndTime != nil && event.Timestamp.AsTime().After(req.EndTime.AsTime()) {
			continue
		}

		filteredEvents = append(filteredEvents, event)

		// Check max events limit
		if req.MaxEvents > 0 && len(filteredEvents) >= int(req.MaxEvents) {
			break
		}
	}
	return filteredEvents
}

func TestUpdateProbeFilter(t *testing.T) {
	agentCfg := Config{
		AgentID: "test-agent",
		Context: context.Background(),
	}
	a, err := New(agentCfg)
	assert.NoError(t, err)

	svc := NewDebugService(a, a.logger)

	tests := []struct {
		name        string
		req         *agentv1.UpdateProbeFilterRequest
		wantSuccess bool
	}{
		{
			name:        "Nil filter returns quickly",
			req:         &agentv1.UpdateProbeFilterRequest{CollectorId: "some-id", Filter: nil},
			wantSuccess: true, // Should succeed with no-op
		},
		{
			name: "Fails gracefully when collector not found",
			req: &agentv1.UpdateProbeFilterRequest{
				CollectorId: "non-existent",
				Filter: &agentv1.UprobeFilter{
					SampleRate: 42,
				},
			},
			wantSuccess: false, // ebpfManager will return collector not found
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.UpdateProbeFilter(context.Background(), tt.req)
			if tt.wantSuccess {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "collector not found")
			}
		})
	}
}
