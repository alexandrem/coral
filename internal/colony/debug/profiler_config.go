// Package debug provides debug session orchestration for the colony.
package debug

import (
	"time"

	debugpb "github.com/coral-mesh/coral/coral/colony/v1"
)

// Profiling configuration constants.
const (
	defaultMaxFunctions         = 20
	maxFunctionsLimit           = 50
	defaultDuration             = 60 * time.Second
	maxDuration                 = 5 * time.Minute
	sessionBuffer               = 30 * time.Second
	pollInterval                = 5 * time.Second
	defaultStrategy             = "critical_path"
	bottleneckMinorThreshold    = 100 * time.Millisecond
	bottleneckMajorThreshold    = 500 * time.Millisecond
	bottleneckCriticalThreshold = 1 * time.Second
)

// profileConfig holds validated configuration for a profiling session.
type profileConfig struct {
	ServiceName  string
	Query        string
	MaxFunctions int
	Duration     time.Duration
	Strategy     string
	SampleRate   float64
	Async        bool
}

// profileState holds mutable state during a profiling session.
type profileState struct {
	SessionIDs   []string
	Results      []*debugpb.ProfileResult
	SuccessCount int
	FailCount    int
}

// parseProfileConfig validates and applies defaults to the request.
func parseProfileConfig(req *debugpb.ProfileFunctionsRequest) *profileConfig {
	cfg := &profileConfig{
		ServiceName:  req.ServiceName,
		Query:        req.Query,
		MaxFunctions: int(req.MaxFunctions),
		Strategy:     req.Strategy,
		SampleRate:   req.SampleRate,
		Async:        req.Async,
	}

	// Apply defaults for MaxFunctions.
	if cfg.MaxFunctions <= 0 {
		cfg.MaxFunctions = defaultMaxFunctions
	}
	if cfg.MaxFunctions > maxFunctionsLimit {
		cfg.MaxFunctions = maxFunctionsLimit
	}

	// Apply defaults for Duration.
	if req.Duration == nil || req.Duration.AsDuration() > maxDuration {
		cfg.Duration = defaultDuration
	} else {
		cfg.Duration = req.Duration.AsDuration()
	}

	// Apply defaults for Strategy.
	if cfg.Strategy == "" {
		cfg.Strategy = defaultStrategy
	}

	return cfg
}

// severityFromDuration returns the bottleneck severity based on P95 duration.
func severityFromDuration(p95 time.Duration) string {
	switch {
	case p95 > bottleneckCriticalThreshold:
		return "critical"
	case p95 > bottleneckMajorThreshold:
		return "major"
	default:
		return "minor"
	}
}
