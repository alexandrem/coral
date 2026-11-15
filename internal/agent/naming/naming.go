// Package naming derives stable, human-readable service names for processes
// observed by the agent (RFD 104).
package naming

import (
	"fmt"
	"path/filepath"
	"sync"
)

// ServiceNameAdaptor derives a name for a process observed on a given port.
// Implementations return ok=false to defer to the next adaptor in the chain
// (e.g. a Kubernetes adaptor that only handles pods it recognizes).
type ServiceNameAdaptor interface {
	Resolve(port int32, pid int32, binaryPath string) (name string, ok bool)
}

// Chain runs a priority-ordered list of adaptors, returning the first
// successful resolution. A Chain is itself a ServiceNameAdaptor, so it can be
// nested or passed anywhere a single adaptor is expected.
type Chain []ServiceNameAdaptor

// Resolve tries each adaptor in order and returns the first one that
// resolves a name.
func (c Chain) Resolve(port int32, pid int32, binaryPath string) (string, bool) {
	for _, adaptor := range c {
		if name, ok := adaptor.Resolve(port, pid, binaryPath); ok {
			return name, true
		}
	}
	return "", false
}

// ProcessNameAdaptor is the built-in ServiceNameAdaptor. It names a process
// after its binary's base name, disambiguating with a `-<port>` suffix once
// two or more ports share the same binary name. Processes with no known
// binary path fall back to `port-<port>`.
type ProcessNameAdaptor struct {
	mu          sync.Mutex
	portsByName map[string]map[int32]struct{}
}

// NewProcessNameAdaptor creates a ProcessNameAdaptor.
func NewProcessNameAdaptor() *ProcessNameAdaptor {
	return &ProcessNameAdaptor{
		portsByName: make(map[string]map[int32]struct{}),
	}
}

// Resolve implements ServiceNameAdaptor. It always succeeds (ok is always
// true), so it is meant to sit last in a chain as the final fallback.
func (a *ProcessNameAdaptor) Resolve(port int32, _ int32, binaryPath string) (string, bool) {
	base := filepath.Base(binaryPath)
	if binaryPath == "" || base == "." || base == string(filepath.Separator) {
		return fmt.Sprintf("port-%d", port), true
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	ports, ok := a.portsByName[base]
	if !ok {
		ports = make(map[int32]struct{})
		a.portsByName[base] = ports
	}
	ports[port] = struct{}{}

	if len(ports) > 1 {
		return fmt.Sprintf("%s-%d", base, port), true
	}
	return base, true
}
