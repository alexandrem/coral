package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// procRoot is the root of the /proc filesystem. Defaults to "/proc" via
	// root(); overridable in tests.
	procRoot string
}

// NewEnvVarProvider creates an EnvVarProvider rooted at /proc.
func NewEnvVarProvider() *EnvVarProvider {
	return &EnvVarProvider{procRoot: "/proc"}
}

// Name returns the provider's identifier.
func (p *EnvVarProvider) Name() string { return "envvar" }

// Probe always returns true: reading /proc/<pid>/environ requires no
// external dependency and degrades gracefully (empty result) when a process
// environment is unreadable.
func (p *EnvVarProvider) Probe() bool { return true }

func (p *EnvVarProvider) root() string {
	if p.procRoot != "" {
		return p.procRoot
	}
	return "/proc"
}

// Discover reads OTEL_SERVICE_NAME (falling back to SERVICE_NAME) from
// every running process's environment, returning a name-hint-only candidate
// for each process where one is set.
func (p *EnvVarProvider) Discover(_ context.Context) ([]ProcessCandidate, error) {
	root := p.root()

	pids, err := listPids(root)
	if err != nil {
		return nil, err
	}

	candidates := make([]ProcessCandidate, 0, len(pids))
	for _, pid := range pids {
		name, ok := readServiceNameEnv(root, pid)
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

// readServiceNameEnv reads <root>/<pid>/environ and extracts
// OTEL_SERVICE_NAME, falling back to SERVICE_NAME. The second return value
// is false if the environ file could not be read at all (e.g. the process
// exited between the PID scan and this read); it is true (with an empty
// name) when the file is readable but neither variable is set.
func readServiceNameEnv(root string, pid int) (string, bool) {
	//nolint:gosec // G304: path is built from a fixed /proc root and a numeric PID, not user input.
	data, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "environ"))
	if err != nil {
		return "", false
	}

	var fallback string
	for _, entry := range strings.Split(string(data), "\x00") {
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "=")
		if !found {
			// Malformed entry (no '='); skip it rather than failing the
			// whole scan.
			continue
		}
		switch key {
		case otelServiceNameVar:
			return value, true
		case serviceNameVar:
			fallback = value
		}
	}

	return fallback, true
}
