package correlation

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// percentileAlarm fires when the rolling percentile of a numeric event field
// exceeds a threshold, evaluated over a sliding window (RFD 091).
type percentileAlarm struct {
	baseEvaluator
	filter     cel.Program
	field      string
	percentile float64
	threshold  float64
	window     time.Duration
	// samples holds (time, value) pairs within the window.
	samples []timedSample
}

type timedSample struct {
	at    time.Time
	value float64
}

func newPercentileAlarm(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*percentileAlarm, error) {
	if desc.Source == nil {
		return nil, fmt.Errorf("percentile_alarm requires source")
	}
	if desc.Field == "" {
		return nil, fmt.Errorf("percentile_alarm requires field")
	}
	if desc.Percentile <= 0 || desc.Percentile > 1 {
		return nil, fmt.Errorf("percentile_alarm requires percentile in (0,1]")
	}
	prog, err := CompileCEL(desc.Source.FilterExpr)
	if err != nil {
		return nil, err
	}
	return &percentileAlarm{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filter:     prog,
		field:      desc.Field,
		percentile: desc.Percentile,
		threshold:  desc.Threshold,
		window:     windowDuration(desc),
	}, nil
}

// OnEvent implements Evaluator.
func (p *percentileAlarm) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	if event.FunctionName != p.desc.Source.Probe {
		return nil, nil
	}
	match, err := EvalCEL(p.filter, event)
	if err != nil {
		return nil, err
	}
	if !match {
		return nil, nil
	}

	value, err := p.extractField(event)
	if err != nil {
		return nil, nil // ignore events that don't have the field
	}

	now := time.Now()
	p.samples = append(p.samples, timedSample{at: now, value: value})

	// Prune old samples.
	if p.window > 0 {
		cutoff := now.Add(-p.window)
		start := 0
		for start < len(p.samples) && p.samples[start].at.Before(cutoff) {
			start++
		}
		p.samples = p.samples[start:]
	}

	if len(p.samples) == 0 {
		return nil, nil
	}

	pct := computePercentile(p.samples, p.percentile)
	if pct >= p.threshold && p.cooldownElapsed() {
		p.recordFire()
		return p.buildAction(map[string]string{
			"field":      p.field,
			"percentile": fmt.Sprintf("%.2f", p.percentile),
			"value":      fmt.Sprintf("%.2f", pct),
			"threshold":  fmt.Sprintf("%.2f", p.threshold),
			"samples":    fmt.Sprintf("%d", len(p.samples)),
		}), nil
	}
	return nil, nil
}

// extractField extracts the numeric value of p.field from the event.
func (p *percentileAlarm) extractField(event *agentv1.UprobeEvent) (float64, error) {
	switch p.field {
	case "duration_ns":
		return float64(event.DurationNs), nil
	case "pid":
		return float64(event.Pid), nil
	case "tid":
		return float64(event.Tid), nil
	}
	if v, ok := event.Labels[p.field]; ok {
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		if err != nil {
			return 0, fmt.Errorf("field %q: %w", p.field, err)
		}
		return f, nil
	}
	return 0, fmt.Errorf("field %q not found in event", p.field)
}

// computePercentile computes the given percentile (0.0–1.0) over the sample set.
func computePercentile(samples []timedSample, pct float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	vals := make([]float64, len(samples))
	for i, s := range samples {
		vals[i] = s.value
	}
	sort.Float64s(vals)
	idx := pct * float64(len(vals)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return vals[lo]
	}
	frac := idx - float64(lo)
	return vals[lo]*(1-frac) + vals[hi]*frac
}
