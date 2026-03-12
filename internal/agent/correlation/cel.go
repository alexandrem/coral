// Package correlation implements agent-side probe correlation evaluation (RFD 091).
// It provides a finite set of temporal pattern strategies evaluated in pure Go
// using CEL filter expressions for per-event matching.
package correlation

import (
	"fmt"

	"github.com/google/cel-go/cel"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// celEnv is the shared CEL environment used across all evaluators.
// It exposes a fixed set of fields from UprobeEvent for filter expressions.
var celEnv *cel.Env

func init() {
	var err error
	celEnv, err = cel.NewEnv(
		cel.Variable("event", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create CEL environment: %v", err))
	}
}

// CompileCEL compiles a CEL filter expression and returns a Program ready for
// evaluation. Returns an error if the expression is syntactically or
// semantically invalid — callers should reject the descriptor at this point.
func CompileCEL(expr string) (cel.Program, error) {
	if expr == "" {
		return nil, nil
	}
	ast, iss := celEnv.Compile(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("CEL compile error: %w", iss.Err())
	}
	prog, err := celEnv.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("CEL program error: %w", err)
	}
	return prog, nil
}

// EvalCEL evaluates a compiled CEL program against a UprobeEvent.
// Returns true if the event matches, false otherwise.
// A nil program (empty filter expression) always returns true.
func EvalCEL(prog cel.Program, event *agentv1.UprobeEvent) (bool, error) {
	if prog == nil {
		return true, nil
	}
	eventMap := eventToMap(event)
	out, _, err := prog.Eval(map[string]any{"event": eventMap})
	if err != nil {
		return false, fmt.Errorf("CEL eval error: %w", err)
	}
	nativeVal := out.Value()
	result, ok := nativeVal.(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression must return bool, got %T", nativeVal)
	}
	return result, nil
}

// eventToMap converts a UprobeEvent to a map[string]any for CEL evaluation.
func eventToMap(e *agentv1.UprobeEvent) map[string]any {
	m := map[string]any{
		"duration_ns":   int64(e.DurationNs),
		"pid":           int64(e.Pid),
		"tid":           int64(e.Tid),
		"function_name": e.FunctionName,
		"event_type":    e.EventType,
		"service_name":  e.ServiceName,
		"agent_id":      e.AgentId,
	}
	// Expose labels as flat string fields (e.g., event.trace_id).
	for k, v := range e.Labels {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return m
}
