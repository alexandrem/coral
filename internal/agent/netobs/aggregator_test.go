package netobs

import (
	"testing"
	"time"
)

func TestAggregator_RecordNewEntry(t *testing.T) {
	agg := newAggregator()
	now := time.Now()

	agg.Record(ConnectionEntry{
		RemoteIP:      "10.0.0.2",
		RemotePort:    5432,
		Protocol:      "tcp",
		BytesSent:     100,
		BytesReceived: 200,
		Retransmits:   1,
		RTTUS:         500,
		FirstObserved: now,
		LastObserved:  now,
	})

	entries := agg.Flush()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.BytesSent != 100 || e.BytesReceived != 200 || e.Retransmits != 1 || e.RTTUS != 500 {
		t.Errorf("unexpected values: %+v", e)
	}
}

func TestAggregator_AccumulatesMetrics(t *testing.T) {
	agg := newAggregator()
	now := time.Now()
	later := now.Add(10 * time.Second)

	// Two observations of the same edge.
	agg.Record(ConnectionEntry{
		RemoteIP:      "10.0.0.2",
		RemotePort:    5432,
		Protocol:      "tcp",
		BytesSent:     100,
		BytesReceived: 200,
		Retransmits:   1,
		LastObserved:  now,
	})
	agg.Record(ConnectionEntry{
		RemoteIP:      "10.0.0.2",
		RemotePort:    5432,
		Protocol:      "tcp",
		BytesSent:     50,
		BytesReceived: 75,
		Retransmits:   2,
		RTTUS:         300,
		LastObserved:  later,
	})

	entries := agg.Flush()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d", len(entries))
	}

	e := entries[0]
	if e.BytesSent != 150 {
		t.Errorf("expected BytesSent=150, got %d", e.BytesSent)
	}
	if e.BytesReceived != 275 {
		t.Errorf("expected BytesReceived=275, got %d", e.BytesReceived)
	}
	if e.Retransmits != 3 {
		t.Errorf("expected Retransmits=3, got %d", e.Retransmits)
	}
	if e.RTTUS != 300 {
		t.Errorf("expected RTTUS=300 (updated from non-zero), got %d", e.RTTUS)
	}
	if !e.LastObserved.Equal(later) {
		t.Errorf("expected LastObserved=%v, got %v", later, e.LastObserved)
	}
}

func TestAggregator_DifferentEdgesKeptSeparate(t *testing.T) {
	agg := newAggregator()
	now := time.Now()

	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.2", RemotePort: 5432, Protocol: "tcp", LastObserved: now})
	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.3", RemotePort: 6379, Protocol: "tcp", LastObserved: now})
	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.2", RemotePort: 80, Protocol: "tcp", LastObserved: now})

	entries := agg.Flush()
	if len(entries) != 3 {
		t.Fatalf("expected 3 distinct entries, got %d", len(entries))
	}
}

func TestAggregator_FlushResetsState(t *testing.T) {
	agg := newAggregator()
	now := time.Now()

	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.2", RemotePort: 5432, Protocol: "tcp", LastObserved: now})
	_ = agg.Flush()

	entries := agg.Flush()
	if len(entries) != 0 {
		t.Fatalf("expected empty flush after reset, got %d entries", len(entries))
	}
}

func TestAggregator_RTTUSNotOverwrittenByZero(t *testing.T) {
	agg := newAggregator()
	now := time.Now()

	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.2", RemotePort: 5432, Protocol: "tcp", RTTUS: 200, LastObserved: now})
	// Second observation has no RTT (netstat fallback).
	agg.Record(ConnectionEntry{RemoteIP: "10.0.0.2", RemotePort: 5432, Protocol: "tcp", RTTUS: 0, LastObserved: now})

	entries := agg.Flush()
	if entries[0].RTTUS != 200 {
		t.Errorf("expected RTTUS to be preserved as 200, got %d", entries[0].RTTUS)
	}
}
