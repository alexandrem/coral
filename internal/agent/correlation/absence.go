package correlation

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// absence fires when no event matching the filter occurs within window T.
// Resets on each matching event (RFD 091).
type absence struct {
	baseEvaluator
	filter    cel.Program
	window    time.Duration
	lastSeen  time.Time
	mu        sync.Mutex
	stopTimer chan struct{}
}

func newAbsence(desc *agentv1.CorrelationDescriptor, agentID string, logger zerolog.Logger) (*absence, error) {
	if desc.Source == nil {
		return nil, fmt.Errorf("absence requires source")
	}
	if desc.Window == nil || desc.Window.AsDuration() == 0 {
		return nil, fmt.Errorf("absence requires window")
	}
	prog, err := CompileCEL(desc.Source.FilterExpr)
	if err != nil {
		return nil, err
	}
	a := &absence{
		baseEvaluator: baseEvaluator{
			desc:     desc,
			agentID:  agentID,
			cooldown: cooldownDuration(desc),
			logger:   logger,
		},
		filter:    prog,
		window:    windowDuration(desc),
		lastSeen:  time.Now(),
		stopTimer: make(chan struct{}),
	}
	go a.runTimer()
	return a, nil
}

// runTimer fires the absence action when the inactivity window expires.
// It resets each time an event is received.
func (a *absence) runTimer() {
	for {
		select {
		case <-a.stopTimer:
			return
		default:
		}
		a.mu.Lock()
		elapsed := time.Since(a.lastSeen)
		remaining := a.window - elapsed
		a.mu.Unlock()

		if remaining <= 0 {
			a.mu.Lock()
			if a.cooldownElapsed() {
				a.recordFire()
				a.mu.Unlock()
				action := a.buildAction(map[string]string{
					"function":       a.desc.Source.Probe,
					"inactivity_for": a.window.String(),
				})
				a.logger.Info().
					Str("id", a.desc.Id).
					Str("function", a.desc.Source.Probe).
					Msg("Absence strategy fired.")
				_ = action // action is returned via a channel in a full implementation;
				// for now it is handled through the action callback registered at construction.
			} else {
				a.mu.Unlock()
			}
			// Back off for cooldown before checking again.
			time.Sleep(a.window)
			continue
		}
		time.Sleep(remaining)
	}
}

// OnEvent implements Evaluator — resets the inactivity timer on a match.
func (a *absence) OnEvent(event *agentv1.UprobeEvent) (*Action, error) {
	if event.FunctionName != a.desc.Source.Probe {
		return nil, nil
	}
	match, err := EvalCEL(a.filter, event)
	if err != nil {
		return nil, err
	}
	if match {
		a.mu.Lock()
		a.lastSeen = time.Now()
		a.mu.Unlock()
	}
	return nil, nil
}

// SetActionCallback sets the callback invoked when the absence condition fires.
// This allows the engine to receive asynchronous actions from the timer goroutine.
func (a *absence) SetActionCallback(cb func(*Action)) {
	// Restart timer goroutine with callback support.
	close(a.stopTimer)
	a.stopTimer = make(chan struct{})
	go func() {
		for {
			select {
			case <-a.stopTimer:
				return
			default:
			}
			a.mu.Lock()
			elapsed := time.Since(a.lastSeen)
			remaining := a.window - elapsed
			a.mu.Unlock()

			if remaining <= 0 {
				a.mu.Lock()
				if a.cooldownElapsed() {
					a.recordFire()
					a.mu.Unlock()
					cb(a.buildAction(map[string]string{
						"function":       a.desc.Source.Probe,
						"inactivity_for": a.window.String(),
					}))
				} else {
					a.mu.Unlock()
				}
				time.Sleep(a.window)
				continue
			}
			time.Sleep(remaining)
		}
	}()
}

// Stop terminates the background timer goroutine.
func (a *absence) Stop() {
	close(a.stopTimer)
}
