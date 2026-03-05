package correlation

import (
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// Action represents a fired correlation action returned to the engine caller.
type Action struct {
	// CorrelationID is the descriptor that triggered this action.
	CorrelationID string
	// Kind is the action to perform.
	Kind agentv1.ActionKind
	// ServiceName is the service this action targets.
	ServiceName string
	// ProfileDurationMs is only set for CPU_PROFILE actions.
	ProfileDurationMs uint32
	// TriggerEvent is set for EMIT_EVENT actions.
	TriggerEvent *agentv1.TriggerEvent
}

// Evaluator is the strategy state machine interface (RFD 091).
// Each implementation evaluates a specific temporal pattern.
type Evaluator interface {
	// OnEvent feeds an event into the strategy state machine.
	// Returns a non-nil Action when the strategy condition fires, nil otherwise.
	OnEvent(event *agentv1.UprobeEvent) (*Action, error)

	// Descriptor returns the installed CorrelationDescriptor.
	Descriptor() *agentv1.CorrelationDescriptor
}

// Engine manages active Evaluators, routes events from collectors, and
// dispatches actions (RFD 091).
type Engine struct {
	mu         sync.RWMutex
	evaluators map[string]Evaluator // keyed by correlation ID
	agentID    string
	logger     zerolog.Logger
}

// absenceEvaluator is the interface subset used to set the action callback on
// absence strategies.
type absenceEvaluator interface {
	Evaluator
	SetActionCallback(func(*Action))
	Stop()
}

// NewEngine creates a new correlation Engine.
func NewEngine(agentID string, logger zerolog.Logger) *Engine {
	return &Engine{
		evaluators: make(map[string]Evaluator),
		agentID:    agentID,
		logger:     logger.With().Str("component", "correlation_engine").Logger(),
	}
}

// ActionCallback is a function called when an absence evaluator fires
// asynchronously (outside the OnEvent call path).
type ActionCallback func(*Action)

// Deploy installs a CorrelationDescriptor on the engine.
// It compiles CEL expressions, instantiates the appropriate evaluator, and
// starts any background timers required by the strategy. Returns an error if
// the descriptor is invalid.
func (e *Engine) Deploy(desc *agentv1.CorrelationDescriptor, cb ActionCallback) error {
	ev, err := newEvaluator(desc, e.agentID, e.logger)
	if err != nil {
		return fmt.Errorf("deploy correlation %s: %w", desc.Id, err)
	}

	// Wire callback for absence strategies that fire asynchronously.
	if ae, ok := ev.(absenceEvaluator); ok && cb != nil {
		ae.SetActionCallback(cb)
	}

	e.mu.Lock()
	e.evaluators[desc.Id] = ev
	e.mu.Unlock()

	e.logger.Info().
		Str("id", desc.Id).
		Str("strategy", desc.Strategy.String()).
		Str("service", desc.ServiceName).
		Msg("Correlation descriptor deployed.")
	return nil
}

// Remove uninstalls an active correlation descriptor.
func (e *Engine) Remove(id string) bool {
	e.mu.Lock()
	ev, ok := e.evaluators[id]
	if ok {
		delete(e.evaluators, id)
	}
	e.mu.Unlock()
	if ok {
		// Stop any background goroutines for absence strategies.
		if ae, isAbsence := ev.(absenceEvaluator); isAbsence {
			ae.Stop()
		}
		e.logger.Info().Str("id", id).Msg("Correlation descriptor removed.")
	}
	return ok
}

// List returns all currently active CorrelationDescriptors.
func (e *Engine) List() []*agentv1.CorrelationDescriptor {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*agentv1.CorrelationDescriptor, 0, len(e.evaluators))
	for _, ev := range e.evaluators {
		out = append(out, ev.Descriptor())
	}
	return out
}

// OnEvent routes an event to all evaluators whose source probe matches the
// event's function name and returns any fired actions.
func (e *Engine) OnEvent(event *agentv1.UprobeEvent) []*Action {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var actions []*Action
	for _, ev := range e.evaluators {
		desc := ev.Descriptor()
		if !probeMatches(desc, event) {
			continue
		}
		action, err := ev.OnEvent(event)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("id", desc.Id).
				Msg("Evaluator error.")
			continue
		}
		if action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// probeMatches returns true if the event's function name matches any source
// probe in the descriptor.
func probeMatches(desc *agentv1.CorrelationDescriptor, event *agentv1.UprobeEvent) bool {
	if desc.Source != nil && desc.Source.Probe == event.FunctionName {
		return true
	}
	if desc.SourceA != nil && desc.SourceA.Probe == event.FunctionName {
		return true
	}
	if desc.SourceB != nil && desc.SourceB.Probe == event.FunctionName {
		return true
	}
	return false
}

// newEvaluator constructs an Evaluator for the given CorrelationDescriptor.
func newEvaluator(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (Evaluator, error) {
	switch desc.Strategy {
	case agentv1.StrategyKind_RATE_GATE:
		return newRateGate(desc, agentID, logger)
	case agentv1.StrategyKind_EDGE_TRIGGER:
		return newEdgeTrigger(desc, agentID, logger)
	case agentv1.StrategyKind_CAUSAL_PAIR:
		return newCausalPair(desc, agentID, logger)
	case agentv1.StrategyKind_ABSENCE:
		return newAbsence(desc, agentID, logger)
	case agentv1.StrategyKind_PERCENTILE_ALARM:
		return newPercentileAlarm(desc, agentID, logger)
	case agentv1.StrategyKind_SEQUENCE:
		return newSequence(desc, agentID, logger)
	default:
		return nil, fmt.Errorf("unknown strategy: %v", desc.Strategy)
	}
}

// baseEvaluator holds fields shared by all strategy implementations.
type baseEvaluator struct {
	desc      *agentv1.CorrelationDescriptor
	agentID   string
	cooldown  time.Duration
	lastFired time.Time
	logger    zerolog.Logger
}

// Descriptor returns the CorrelationDescriptor.
func (b *baseEvaluator) Descriptor() *agentv1.CorrelationDescriptor {
	return b.desc
}

// cooldownElapsed returns true if the cooldown period has elapsed since the
// last firing.
func (b *baseEvaluator) cooldownElapsed() bool {
	if b.cooldown == 0 {
		return true
	}
	return time.Since(b.lastFired) >= b.cooldown
}

// recordFire records the current time as the last firing time.
func (b *baseEvaluator) recordFire() {
	b.lastFired = time.Now()
}

// buildAction builds an Action from the descriptor action spec.
func (b *baseEvaluator) buildAction(ctx map[string]string) *Action {
	action := &Action{
		CorrelationID: b.desc.Id,
		ServiceName:   b.desc.ServiceName,
	}
	if b.desc.Action != nil {
		action.Kind = b.desc.Action.Kind
		action.ProfileDurationMs = b.desc.Action.ProfileDurationMs
	}
	if action.Kind == agentv1.ActionKind_EMIT_EVENT {
		action.TriggerEvent = &agentv1.TriggerEvent{
			CorrelationId: b.desc.Id,
			Strategy:      b.desc.Strategy.String(),
			AgentId:       b.agentID,
			ServiceName:   b.desc.ServiceName,
			Context:       ctx,
		}
	}
	return action
}

// windowDuration returns the window duration from the descriptor (default 0).
func windowDuration(desc *agentv1.CorrelationDescriptor) time.Duration {
	if desc.Window == nil {
		return 0
	}
	return desc.Window.AsDuration()
}

// cooldownDuration returns the cooldown duration from the descriptor.
func cooldownDuration(desc *agentv1.CorrelationDescriptor) time.Duration {
	if desc.CooldownMs == 0 {
		return 0
	}
	return time.Duration(desc.CooldownMs) * time.Millisecond
}
