package correlation

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// pendingA holds an event from source A waiting for a matching source B event.
type pendingA struct {
	event     *agentv1.UprobeEvent
	joinValue string
	arrivedAt time.Time
}

// causalPair fires when an event from source A is followed by an event from
// source B within window T, sharing a common field value via join_on (RFD 091).
type causalPair struct {
	baseEvaluator
	filterA  cel.Program
	filterB  cel.Program
	joinOn   string
	window   time.Duration
	pendingA []pendingA
}

func newCausalPair(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*causalPair, error) {
	if desc.SourceA == nil || desc.SourceB == nil {
		return nil, fmt.Errorf("causal_pair requires source_a and source_b")
	}
	progA, err := CompileCEL(desc.SourceA.FilterExpr)
	if err != nil {
		return nil, fmt.Errorf("source_a filter: %w", err)
	}
	progB, err := CompileCEL(desc.SourceB.FilterExpr)
	if err != nil {
		return nil, fmt.Errorf("source_b filter: %w", err)
	}
	return &causalPair{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filterA: progA,
		filterB: progB,
		joinOn:  desc.JoinOn,
		window:  windowDuration(desc),
	}, nil
}

// OnEvent implements Evaluator.
func (c *causalPair) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	now := time.Now()
	// Prune expired pending A events.
	if c.window > 0 {
		cutoff := now.Add(-c.window)
		start := 0
		for start < len(c.pendingA) && c.pendingA[start].arrivedAt.Before(cutoff) {
			start++
		}
		c.pendingA = c.pendingA[start:]
	}

	switch event.FunctionName {
	case c.desc.SourceA.Probe:
		match, err := EvalCEL(c.filterA, event)
		if err != nil {
			return nil, err
		}
		if !match {
			return nil, nil
		}
		joinVal := c.extractJoin(event)
		c.pendingA = append(c.pendingA, pendingA{
			event:     event,
			joinValue: joinVal,
			arrivedAt: now,
		})
		return nil, nil

	case c.desc.SourceB.Probe:
		match, err := EvalCEL(c.filterB, event)
		if err != nil {
			return nil, err
		}
		if !match {
			return nil, nil
		}
		joinVal := c.extractJoin(event)
		// Find a pending A event with the same join value.
		for i, pa := range c.pendingA {
			if pa.joinValue == joinVal {
				// Remove the matched pending A event.
				c.pendingA = append(c.pendingA[:i], c.pendingA[i+1:]...)
				if c.cooldownElapsed() {
					c.recordFire()
					return c.buildAction(map[string]string{
						"join_on":    c.joinOn,
						"join_value": joinVal,
						"source_a":   c.desc.SourceA.Probe,
						"source_b":   c.desc.SourceB.Probe,
					}), nil
				}
				return nil, nil
			}
		}
	}
	return nil, nil
}

// extractJoin extracts the join field value from an event.
func (c *causalPair) extractJoin(event *agentv1.UprobeEvent) string {
	if c.joinOn == "" {
		return ""
	}
	if v, ok := event.Labels[c.joinOn]; ok {
		return v
	}
	// Check common built-in fields.
	switch c.joinOn {
	case "function_name":
		return event.FunctionName
	case "pid":
		return fmt.Sprintf("%d", event.Pid)
	case "tid":
		return fmt.Sprintf("%d", event.Tid)
	}
	return ""
}
