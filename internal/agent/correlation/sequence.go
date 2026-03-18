package correlation

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// sequence fires when events matching filter A are followed by events matching
// filter B within window T, in that order (RFD 091).
type sequence struct {
	baseEvaluator
	filterA cel.Program
	filterB cel.Program
	window  time.Duration
	// pendingA holds the times of matched A events within the window.
	pendingA []time.Time
}

func newSequence(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*sequence, error) {
	if desc.SourceA == nil || desc.SourceB == nil {
		return nil, fmt.Errorf("sequence requires source_a and source_b")
	}
	progA, err := CompileCEL(desc.SourceA.FilterExpr)
	if err != nil {
		return nil, fmt.Errorf("source_a filter: %w", err)
	}
	progB, err := CompileCEL(desc.SourceB.FilterExpr)
	if err != nil {
		return nil, fmt.Errorf("source_b filter: %w", err)
	}
	return &sequence{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filterA: progA,
		filterB: progB,
		window:  windowDuration(desc),
	}, nil
}

// OnEvent implements Evaluator.
func (s *sequence) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	now := time.Now()
	// Prune expired A events.
	if s.window > 0 {
		cutoff := now.Add(-s.window)
		start := 0
		for start < len(s.pendingA) && s.pendingA[start].Before(cutoff) {
			start++
		}
		s.pendingA = s.pendingA[start:]
	}

	switch event.FunctionName {
	case s.desc.SourceA.Probe:
		match, err := EvalCEL(s.filterA, event)
		if err != nil {
			return nil, err
		}
		if match {
			s.pendingA = append(s.pendingA, now)
		}
		return nil, nil

	case s.desc.SourceB.Probe:
		match, err := EvalCEL(s.filterB, event)
		if err != nil {
			return nil, err
		}
		if !match {
			return nil, nil
		}
		if len(s.pendingA) > 0 && s.cooldownElapsed() {
			s.pendingA = s.pendingA[:0] // consume
			s.recordFire()
			return s.buildAction(map[string]string{
				"source_a": s.desc.SourceA.Probe,
				"source_b": s.desc.SourceB.Probe,
				"window":   s.window.String(),
			}), nil
		}
	}
	return nil, nil
}
