package discovery

import (
	"context"

	"github.com/coral-mesh/coral/internal/sys/proc"
)

// otelServiceNameVar is the primary environment variable EnvVarProvider
// looks for.
const otelServiceNameVar = "OTEL_SERVICE_NAME"

// serviceNameVar is the fallback environment variable, checked when
// otelServiceNameVar is absent.
const serviceNameVar = "SERVICE_NAME"

// EnvVarProvider discovers service name hints by reading
// OTEL_SERVICE_NAME (or SERVICE_NAME) from each running process's
// environment (/proc/<pid>/environ). It reports name hints only — no port
// data — so it always ranks by PID when merged against ProcFSProvider's
// port-carrying candidates.
type EnvVarProvider struct {
	// fs is the procfs mount to read; the zero value reads the host's /proc.
	fs proc.FS
}

// NewEnvVarProvider creates an EnvVarProvider reading the host's /proc.
func NewEnvVarProvider() *EnvVarProvider {
	return &EnvVarProvider{}
}

// Name returns the provider's identifier.
func (p *EnvVarProvider) Name() string { return "envvar" }

// Probe always returns true: reading /proc/<pid>/environ requires no
// external dependency and degrades gracefully (empty result) when a process
// environment is unreadable.
func (p *EnvVarProvider) Probe() bool { return true }

// Discover reads OTEL_SERVICE_NAME (falling back to SERVICE_NAME) from
// every running process's environment, returning a name-hint-only candidate
// for each process where one is set.
func (p *EnvVarProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	pids, err := p.fs.ListPids()
	if err != nil {
		return nil, err
	}

	candidates := make([]ProcessCandidate, 0, len(pids))
	for _, pid := range pids {
		name, ok := readServiceNameEnv(p.fs, pid)
		if !ok || name == "" {
			continue
		}
		candidates = append(candidates, ProcessCandidate{
			PID:  pid,
			Name: name,
		})
	}

	return candidates, nil
}

// readServiceNameEnv reads the environment of pid and extracts
// OTEL_SERVICE_NAME, falling back to SERVICE_NAME. The second return value
// is false if the environment could not be read at all (e.g. the process
// exited between the PID scan and this read); it is true (with an empty
// name) when it is readable but neither variable is set.
func readServiceNameEnv(fs proc.FS, pid int) (string, bool) {
	environ, err := fs.Environ(pid)
	if err != nil {
		return "", false
	}
	if name, ok := proc.LookupEnv(environ, otelServiceNameVar); ok {
		return name, true
	}
	name, _ := proc.LookupEnv(environ, serviceNameVar)
	return name, true
}
