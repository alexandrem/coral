package correlation

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// edgeTrigger fires once when the filter transitions from not matching to
// matching. Resets after a configurable cooldown period (RFD 091).
type edgeTrigger struct {
	baseEvaluator
	filter      cel.Program
	lastMatched bool
}

func newEdgeTrigger(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*edgeTrigger, error) {
	if desc.Source == nil {
		return nil, fmt.Errorf("edge_trigger requires source")
	}
	prog, err := CompileCEL(desc.Source.FilterExpr)
	if err != nil {
		return nil, err
	}
	return &edgeTrigger{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filter: prog,
	}, nil
}

// OnEvent implements Evaluator.
func (e *edgeTrigger) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	if event.FunctionName != e.desc.Source.Probe {
		return nil, nil
	}
	match, err := EvalCEL(e.filter, event)
	if err != nil {
		return nil, err
	}

	// Rising edge: previous evaluation did not match, this one does.
	wasMatching := e.lastMatched
	e.lastMatched = match

	if match && !wasMatching && e.cooldownElapsed() {
		e.recordFire()
		return e.buildAction(map[string]string{
			"function": e.desc.Source.Probe,
			"edge":     "rising",
			"duration": fmt.Sprintf("%d", event.DurationNs),
		}), nil
	}
	return nil, nil
}

// OnTick is called by the engine when the cooldown expires to allow the
// edge trigger to reset its seen state.
func (e *edgeTrigger) OnTick(now time.Time) {
	if !e.lastFired.IsZero() && now.Sub(e.lastFired) >= e.cooldown {
		// After cooldown elapses, allow the trigger to fire again by resetting
		// the last-matched state.
		e.lastMatched = false
	}
}
