package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeProvider is a ProcessDiscoveryProvider stub for tests.
type fakeProvider struct {
	name       string
	probe      bool
	candidates []ProcessCandidate
	err        error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Probe() bool  { return f.probe }
func (f *fakeProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidates, nil
}

func TestMergeTwoProvidersHigherPriorityNameWins(t *testing.T) {
	ctx := context.Background()

	high := &fakeProvider{
		name:  "high",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 100, Name: "high-name"},
		},
	}
	low := &fakeProvider{
		name:  "low",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 100, Name: "low-name", Ports: []int{8080}},
		},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(high)
	mgr.RegisterProvider(low)

	mgr.Poll(ctx)

	if len(got) != 1 {
		t.Fatalf("expected 1 merged candidate, got %d", len(got))
	}
	c := got[0]
	if c.Name != "high-name" {
		t.Errorf("Name = %q, want %q (higher priority provider should win)", c.Name, "high-name")
	}
	// Ports should still be backfilled from the lower-priority provider,
	// since the higher-priority provider reported none.
	if len(c.Ports) != 1 || c.Ports[0] != 8080 {
		t.Errorf("Ports = %v, want [8080] backfilled from lower-priority provider", c.Ports)
	}
	if c.IsClientOnly {
		t.Errorf("IsClientOnly = true, want false since Ports is non-empty")
	}
}

func TestStaticCandidateWinsOverProviderForSamePID(t *testing.T) {
	ctx := context.Background()

	provider := &fakeProvider{
		name:  "procfs",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 42, Name: "binary-name", Ports: []int{9090}},
		},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(provider)
	mgr.SetStaticCandidates([]ProcessCandidate{
		{PID: 42, Name: "connect-name", Ports: []int{9090}},
	})

	mgr.Poll(ctx)

	if len(got) != 1 {
		t.Fatalf("expected 1 merged candidate, got %d", len(got))
	}
	if got[0].Name != "connect-name" {
		t.Errorf("Name = %q, want %q (static candidate must win)", got[0].Name, "connect-name")
	}
	if got[0].Source != "static" {
		t.Errorf("Source = %q, want %q", got[0].Source, "static")
	}
}

// TestStaticExePathPatternSurvivesMerge verifies that an explicit
// `coral connect --exe-pattern` rule reaches Beyla unchanged. Static
// candidates are merged before configuration generation, so losing this
// field here would silently fall back to a name-derived exe_path rule.
func TestStaticExePathPatternSurvivesMerge(t *testing.T) {
	ctx := context.Background()

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.SetStaticCandidates([]ProcessCandidate{{
		Name:           "worker-explicit",
		IsClientOnly:   true,
		ExePathPattern: "worker-app",
	}})

	mgr.Poll(ctx)

	if len(got) != 1 {
		t.Fatalf("expected 1 merged candidate, got %d", len(got))
	}
	if got[0].ExePathPattern != "worker-app" {
		t.Errorf("ExePathPattern = %q, want literal explicit pattern %q", got[0].ExePathPattern, "worker-app")
	}
}

// TestStaticCandidateJoinsProviderCandidateByPort exercises the case
// central to RFD 102: a `coral connect` registration is made before the
// underlying process has been observed (so it carries a port but no PID).
// Once a provider (e.g. ProcFSProvider) discovers the real process on that
// same port, the two must collapse into ONE merged candidate — not two
// separate entries that would otherwise produce two conflicting Beyla rules
// for the same port.
func TestStaticCandidateJoinsProviderCandidateByPort(t *testing.T) {
	ctx := context.Background()

	provider := &fakeProvider{
		name:  "procfs",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 777, Name: "coral-agent", Ports: []int{8080}},
		},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(provider)
	mgr.SetStaticCandidates([]ProcessCandidate{
		{Name: "otel-app", Ports: []int{8080}}, // No PID known yet.
	})

	mgr.Poll(ctx)

	if len(got) != 1 {
		t.Fatalf("expected static and provider candidates for the same port to collapse into 1 entry, got %d: %+v", len(got), got)
	}
	c := got[0]
	if c.Name != "otel-app" {
		t.Errorf("Name = %q, want %q (static registration must win the name)", c.Name, "otel-app")
	}
	if c.PID != 777 {
		t.Errorf("PID = %d, want 777 (real PID backfilled from provider)", c.PID)
	}
	if len(c.Ports) != 1 || c.Ports[0] != 8080 {
		t.Errorf("Ports = %v, want [8080]", c.Ports)
	}
}

func TestStaticCandidateSurvivesPollWithNoMatchingProvider(t *testing.T) {
	ctx := context.Background()

	// Provider never reports the static candidate's process.
	provider := &fakeProvider{
		name:       "procfs",
		probe:      true,
		candidates: []ProcessCandidate{},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(provider)
	mgr.SetStaticCandidates([]ProcessCandidate{
		{Name: "pinned-service", Ports: []int{7000}},
	})

	mgr.Poll(ctx)
	if len(got) != 1 || got[0].Name != "pinned-service" {
		t.Fatalf("expected static candidate present after first poll, got %v", got)
	}

	// Second poll: provider still reports nothing relevant. The static
	// candidate must still be present (not aged out).
	got = nil
	mgr.Poll(ctx)
	if len(got) != 0 {
		t.Fatalf("expected no callback on unchanged poll, got %v", got)
	}
	if len(mgr.Current()) != 1 || mgr.Current()[0].Name != "pinned-service" {
		t.Fatalf("expected static candidate to survive poll cycle, got %v", mgr.Current())
	}
}

func TestChangeDetectionFiresOnAddAndRemove(t *testing.T) {
	ctx := context.Background()

	provider := &fakeProvider{name: "procfs", probe: true}

	callCount := 0
	var lastCandidates []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) {
		callCount++
		lastCandidates = c
	})
	mgr.RegisterProvider(provider)

	// Poll with no candidates: no prior state, no candidates -> no change.
	mgr.Poll(ctx)
	if callCount != 0 {
		t.Fatalf("expected no callback on empty->empty poll, got %d calls", callCount)
	}

	// Add a candidate.
	provider.candidates = []ProcessCandidate{{PID: 1, Name: "svc-a", Ports: []int{80}}}
	mgr.Poll(ctx)
	if callCount != 1 {
		t.Fatalf("expected callback on add, got %d calls", callCount)
	}
	if len(lastCandidates) != 1 {
		t.Fatalf("expected 1 candidate after add, got %d", len(lastCandidates))
	}

	// Remove the candidate.
	provider.candidates = nil
	mgr.Poll(ctx)
	if callCount != 2 {
		t.Fatalf("expected callback on remove, got %d calls", callCount)
	}
	if len(lastCandidates) != 0 {
		t.Fatalf("expected 0 candidates after remove, got %d", len(lastCandidates))
	}
}

func TestNoCallbackWhenMapUnchanged(t *testing.T) {
	ctx := context.Background()

	provider := &fakeProvider{
		name:  "procfs",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 1, Name: "svc-a", Ports: []int{80}},
		},
	}

	callCount := 0
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(_ []ProcessCandidate) { callCount++ })
	mgr.RegisterProvider(provider)

	mgr.Poll(ctx)
	if callCount != 1 {
		t.Fatalf("expected 1 callback on first poll, got %d", callCount)
	}

	// Poll again with identical candidates: no callback expected.
	mgr.Poll(ctx)
	if callCount != 1 {
		t.Fatalf("expected no additional callback for unchanged poll, got %d total calls", callCount)
	}
}

func TestProbeFalseSkipsProvider(t *testing.T) {
	ctx := context.Background()

	unavailable := &fakeProvider{
		name:  "unavailable",
		probe: false,
		candidates: []ProcessCandidate{
			{PID: 1, Name: "should-not-appear"},
		},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(unavailable)

	mgr.Poll(ctx)
	if len(got) != 0 {
		t.Fatalf("expected provider with Probe()==false to be skipped, got %v", got)
	}
}

func TestProviderErrorIsSkippedGracefully(t *testing.T) {
	ctx := context.Background()

	failing := &fakeProvider{name: "failing", probe: true, err: context.DeadlineExceeded}
	ok := &fakeProvider{
		name:  "ok",
		probe: true,
		candidates: []ProcessCandidate{
			{PID: 1, Name: "svc-a", Ports: []int{80}},
		},
	}

	var got []ProcessCandidate
	mgr := NewDiscoveryManager(zerolog.Nop(), time.Second, func(c []ProcessCandidate) { got = c })
	mgr.RegisterProvider(failing)
	mgr.RegisterProvider(ok)

	mgr.Poll(ctx)
	if len(got) != 1 || got[0].Name != "svc-a" {
		t.Fatalf("expected failing provider to be skipped without affecting others, got %v", got)
	}
}
