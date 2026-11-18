// Package types defines core data types for the agent CLI.
package types

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

var (
	// Service name must be alphanumeric with hyphens.
	serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-]*$`)

	// Health path must start with / and contain valid path characters.
	healthPathRegex = regexp.MustCompile(`^/[a-zA-Z0-9\-\_\./]*$`)

	// Service type must be alphanumeric.
	serviceTypeRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
)

// ServiceSpec represents a parsed service specification.
type ServiceSpec struct {
	Name           string
	Port           int32
	HealthEndpoint string
	ServiceType    string
	Labels         map[string]string

	// ExePattern identifies a portless process by executable pattern (RFD
	// 111) instead of a listening port. Mutually exclusive with Port:
	// exactly one of the two must be set.
	ExePattern string

	// ProcessEnv and DockerLabels identify the service's process among
	// several listeners on Port, by environment variables and by the labels
	// of its Docker container. Only valid with Port.
	ProcessEnv   map[string]string
	DockerLabels map[string]string
}

// ParseServiceSpec parses a service specification string.
// Format: name:port[:health][:type]
// Examples:
//   - api:8080
//   - frontend:3000:/health
//   - redis:6379::redis
//   - metrics:9090:/metrics:prometheus
func ParseServiceSpec(spec string) (*ServiceSpec, error) {
	if spec == "" {
		return nil, fmt.Errorf("service spec cannot be empty")
	}

	// Split by colon to get components
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid service spec format: must be name:port[:health][:type]")
	}

	// Parse name
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return nil, fmt.Errorf("service name cannot be empty")
	}
	if !serviceNameRegex.MatchString(name) {
		return nil, fmt.Errorf("invalid service name '%s': must be alphanumeric with hyphens", name)
	}

	// Parse port
	portStr := strings.TrimSpace(parts[1])
	if portStr == "" {
		return nil, fmt.Errorf("service port cannot be empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port '%s': must be a number", portStr)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}

	result := &ServiceSpec{
		Name: name,
		//nolint:gosec // G109: Port is validated to be 1-65535, conversion to int32 is safe.
		Port:   int32(port),
		Labels: make(map[string]string),
	}

	// Parse optional health endpoint (part 2)
	if len(parts) > 2 && parts[2] != "" {
		health := strings.TrimSpace(parts[2])
		if !strings.HasPrefix(health, "/") {
			return nil, fmt.Errorf("invalid health endpoint '%s': must start with /", health)
		}
		if !healthPathRegex.MatchString(health) {
			return nil, fmt.Errorf("invalid health endpoint '%s': contains invalid characters", health)
		}
		result.HealthEndpoint = health
	}

	// Parse optional service type (part 3)
	if len(parts) > 3 && parts[3] != "" {
		serviceType := strings.TrimSpace(parts[3])
		if !serviceTypeRegex.MatchString(serviceType) {
			return nil, fmt.Errorf("invalid service type '%s': must be alphanumeric", serviceType)
		}
		result.ServiceType = serviceType
	}

	// Error on too many parts
	if len(parts) > 4 {
		return nil, fmt.Errorf("invalid service spec format: too many components (expected name:port[:health][:type])")
	}

	return result, nil
}

// ToProto converts a ServiceSpec to the protobuf ServiceInfo message.
func (s *ServiceSpec) ToProto() *meshv1.ServiceInfo {
	return &meshv1.ServiceInfo{
		Name:           s.Name,
		Port:           s.Port,
		HealthEndpoint: s.HealthEndpoint,
		ServiceType:    s.ServiceType,
		Labels:         s.Labels,
		ExePattern:     s.ExePattern,
		ProcessEnv:     s.ProcessEnv,
		DockerLabels:   s.DockerLabels,
	}
}

// ParseSelectors parses KEY=VALUE pairs, as given to --process-env or
// --docker-label, into a map. The value may be empty or contain '='; the
// key may not be empty or repeated.
func ParseSelectors(flag string, pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	selectors := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid %s %q: expected KEY=VALUE", flag, pair)
		}
		if _, dup := selectors[key]; dup {
			return nil, fmt.Errorf("duplicate %s key %q", flag, key)
		}
		selectors[key] = value
	}
	return selectors, nil
}

// ParseMultipleServiceSpecs parses multiple service specifications.
func ParseMultipleServiceSpecs(specs []string) ([]*ServiceSpec, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("at least one service spec is required")
	}

	results := make([]*ServiceSpec, 0, len(specs))
	seenNames := make(map[string]bool)

	for i, spec := range specs {
		parsed, err := ParseServiceSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid service spec at position %d (%s): %w", i+1, spec, err)
		}

		// Check for duplicate service names
		if seenNames[parsed.Name] {
			return nil, fmt.Errorf("duplicate service name '%s' at position %d", parsed.Name, i+1)
		}
		seenNames[parsed.Name] = true

		results = append(results, parsed)
	}

	return results, nil
}

// ValidateServiceSpecs performs additional validation on a list of service specs.
func ValidateServiceSpecs(specs []*ServiceSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("at least one service is required")
	}

	// Exactly one of port or exe_pattern must be set per spec (RFD 111): a
	// service is identified either by the socket it listens on or by its
	// executable path, never both, never neither.
	for _, spec := range specs {
		hasPort := spec.Port != 0
		hasPattern := spec.ExePattern != ""
		if hasPort && hasPattern {
			return fmt.Errorf("service %q cannot specify both a port and --exe-pattern", spec.Name)
		}
		if !hasPort && !hasPattern {
			return fmt.Errorf("service %q needs either a port or --exe-pattern", spec.Name)
		}
		if err := ValidateSelectors(spec.Name, spec.Port, spec.ProcessEnv, spec.DockerLabels); err != nil {
			return err
		}
	}

	// Check for port collisions (warning only, not an error)
	seenPorts := make(map[int32][]string)
	for _, spec := range specs {
		if spec.Port != 0 {
			seenPorts[spec.Port] = append(seenPorts[spec.Port], spec.Name)
		}
	}

	// Note: We don't error on port collisions as they may be intentional
	// (e.g., different containers in the same pod)

	return nil
}

// ValidateSelectors checks a service's process selectors: they identify
// one listener on a port, so they require one, and their keys must be
// non-empty.
func ValidateSelectors(name string, port int32, processEnv, dockerLabels map[string]string) error {
	if len(processEnv) == 0 && len(dockerLabels) == 0 {
		return nil
	}
	if port == 0 {
		return fmt.Errorf("service %q: process_env and docker_labels require a port", name)
	}
	for _, selectors := range []map[string]string{processEnv, dockerLabels} {
		for key := range selectors {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("service %q: process_env and docker_labels keys cannot be empty", name)
			}
		}
	}
	return nil
}

// IsValidServiceName checks if a service name is valid.
func IsValidServiceName(name string) bool {
	return serviceNameRegex.MatchString(name)
}
