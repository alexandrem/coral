package correlation

import (
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// rateGate fires when N or more events matching the filter occur within a
// sliding window of duration T (RFD 091).
type rateGate struct {
	baseEvaluator
	filter    cel.Program
	threshold int
	window    time.Duration
	// timestamps holds the arrival times of matched events within the window.
	timestamps []time.Time
}

func newRateGate(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*rateGate, error) {
	if desc.Source == nil {
		return nil, fmt.Errorf("rate_gate requires source")
	}
	if desc.Threshold <= 0 {
		return nil, fmt.Errorf("rate_gate requires threshold > 0")
	}
	prog, err := CompileCEL(desc.Source.FilterExpr)
	if err != nil {
		return nil, err
	}
	return &rateGate{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filter:    prog,
		threshold: int(desc.Threshold),
		window:    windowDuration(desc),
	}, nil
}

// OnEvent implements Evaluator.
func (r *rateGate) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	if event.FunctionName != r.desc.Source.Probe {
		return nil, nil
	}
	match, err := EvalCEL(r.filter, event)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, nil
	}

	now := time.Now()
	// Append current event time and prune timestamps outside the window.
	r.timestamps = append(r.timestamps, now)
	if r.window > 0 {
		cutoff := now.Add(-r.window)
		start := 0
		for start < len(r.timestamps) && r.timestamps[start].Before(cutoff) {
			start++
		}
		r.timestamps = r.timestamps[start:]
	}

	if len(r.timestamps) >= r.threshold && r.cooldownElapsed() {
		r.recordFire()
		r.timestamps = r.timestamps[:0] // reset window after firing
		return r.buildAction(map[string]string{
			"count":    fmt.Sprintf("%d", r.threshold),
			"window":   r.window.String(),
			"function": r.desc.Source.Probe,
		}), nil
	}
	return nil, nil
}
