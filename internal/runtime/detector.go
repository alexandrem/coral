package runtime

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Detector detects runtime context information.
type Detector struct {
	logger  zerolog.Logger
	version string // Agent version
}

// NewDetector creates a new runtime detector.
func NewDetector(logger zerolog.Logger, version string) *Detector {
	return &Detector{
		logger:  logger.With().Str("component", "runtime_detector").Logger(),
		version: version,
	}
}

// Detect performs runtime context detection.
func (d *Detector) Detect(ctx context.Context) (*agentv1.RuntimeContextResponse, error) {
	d.logger.Info().Msg("Detecting runtime context")

	// Detect platform information.
	platform, err := d.detectPlatform()
	if err != nil {
		return nil, fmt.Errorf("failed to detect platform: %w", err)
	}

	// Detect runtime type.
	runtimeType, err := d.detectRuntimeType()
	if err != nil {
		return nil, fmt.Errorf("failed to detect runtime type: %w", err)
	}

	// Detect sidecar mode (if K8s).
	sidecarMode := d.detectSidecarMode(runtimeType)

	// Detect CRI socket.
	criSocket := d.detectCRISocket()

	// Determine capabilities.
	capabilities := d.determineCapabilities(runtimeType, sidecarMode, criSocket)

	// Calculate visibility scope.
	visibility := d.calculateVisibility(runtimeType, sidecarMode, criSocket)

	response := &agentv1.RuntimeContextResponse{
		Platform:     platform,
		RuntimeType:  runtimeType,
		SidecarMode:  sidecarMode,
		CriSocket:    criSocket,
		Capabilities: capabilities,
		Visibility:   visibility,
		DetectedAt:   timestamppb.New(time.Now()),
		Version:      d.version,
	}

	d.logDetectionResults(response)

	return response, nil
}

// detectPlatform detects platform information.
func (d *Detector) detectPlatform() (*agentv1.PlatformInfo, error) {
	platform := &agentv1.PlatformInfo{
		Os:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	// Detect OS version and kernel.
	osVersion, kernel := detectOSVersion()
	platform.OsVersion = osVersion
	platform.Kernel = kernel

	d.logger.Debug().
		Str("os", platform.Os).
		Str("arch", platform.Arch).
		Str("os_version", platform.OsVersion).
		Str("kernel", platform.Kernel).
		Msg("Platform detected")

	return platform, nil
}

// detectRuntimeType detects the runtime type.
func (d *Detector) detectRuntimeType() (agentv1.RuntimeContext, error) {
	// Check for Kubernetes environment.
	if isKubernetes() {
		// Determine if sidecar or DaemonSet.
		if isSidecar() {
			d.logger.Debug().Msg("Runtime type: Kubernetes Sidecar")
			return agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR, nil
		}
		d.logger.Debug().Msg("Runtime type: Kubernetes DaemonSet")
		return agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET, nil
	}

	// Check for Docker environment.
	if isDocker() {
		d.logger.Debug().Msg("Runtime type: Docker")
		return agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER, nil
	}

	// Default to native.
	d.logger.Debug().Msg("Runtime type: Native")
	return agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE, nil
}

// detectSidecarMode detects sidecar mode for Kubernetes sidecars.
func (d *Detector) detectSidecarMode(runtimeType agentv1.RuntimeContext) agentv1.SidecarMode {
	// Only applicable for K8s sidecars.
	if runtimeType != agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR {
		return agentv1.SidecarMode_SIDECAR_MODE_UNKNOWN
	}

	// Check for CRI socket availability.
	criSocket := d.detectCRISocket()
	if criSocket != nil {
		d.logger.Debug().Msg("Sidecar mode: CRI (recommended)")
		return agentv1.SidecarMode_SIDECAR_MODE_CRI
	}

	// Check for shared process namespace.
	if hasSharedProcessNamespace() {
		d.logger.Debug().Msg("Sidecar mode: Shared NS")
		return agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS
	}

	// Passive mode (limited capabilities).
	d.logger.Debug().Msg("Sidecar mode: Passive")
	return agentv1.SidecarMode_SIDECAR_MODE_PASSIVE
}

// detectCRISocket detects and probes CRI socket.
func (d *Detector) detectCRISocket() *agentv1.CRISocketInfo {
	// Common CRI socket paths.
	socketPaths := []string{
		"/var/run/containerd/containerd.sock",
		"/var/run/crio/crio.sock",
		"/var/run/docker.sock",
		"/run/containerd/containerd.sock",
		"/run/crio/crio.sock",
	}

	for _, path := range socketPaths {
		if _, err := os.Stat(path); err == nil {
			// Socket exists, probe for type and version.
			criType, version := probeCRISocket(path)
			if criType != "" {
				d.logger.Debug().
					Str("path", path).
					Str("type", criType).
					Str("version", version).
					Msg("CRI socket detected")

				return &agentv1.CRISocketInfo{
					Path:    path,
					Type:    criType,
					Version: version,
				}
			}
		}
	}

	d.logger.Debug().Msg("No CRI socket detected")
	return nil
}

// determineCapabilities determines agent capabilities based on runtime context.
func (d *Detector) determineCapabilities(
	runtimeType agentv1.RuntimeContext,
	sidecarMode agentv1.SidecarMode,
	criSocket *agentv1.CRISocketInfo,
) *agentv1.Capabilities {
	capabilities := &agentv1.Capabilities{
		CanConnect: true, // Always supported
	}

	switch runtimeType {
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE:
		// Native has full capabilities.
		capabilities.CanRun = true
		capabilities.CanExec = true
		capabilities.CanShell = true

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER:
		// Docker has full capabilities.
		capabilities.CanRun = true
		capabilities.CanExec = true
		capabilities.CanShell = true

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR:
		// Sidecar capabilities depend on mode.
		switch sidecarMode {
		case agentv1.SidecarMode_SIDECAR_MODE_CRI:
			capabilities.CanRun = false // Sidecar can't launch new containers
			capabilities.CanExec = true
			capabilities.CanShell = true
		case agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS:
			capabilities.CanRun = false
			capabilities.CanExec = true
			capabilities.CanShell = true
		case agentv1.SidecarMode_SIDECAR_MODE_PASSIVE:
			capabilities.CanRun = false
			capabilities.CanExec = false
			capabilities.CanShell = false
		}

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET:
		// DaemonSet has exec/shell but not run.
		capabilities.CanRun = false
		capabilities.CanExec = true
		capabilities.CanShell = true
	}

	d.logger.Debug().
		Bool("can_run", capabilities.CanRun).
		Bool("can_exec", capabilities.CanExec).
		Bool("can_shell", capabilities.CanShell).
		Bool("can_connect", capabilities.CanConnect).
		Msg("Capabilities determined")

	return capabilities
}

// calculateVisibility calculates visibility scope.
func (d *Detector) calculateVisibility(
	runtimeType agentv1.RuntimeContext,
	sidecarMode agentv1.SidecarMode,
	criSocket *agentv1.CRISocketInfo,
) *agentv1.VisibilityScope {
	visibility := &agentv1.VisibilityScope{}

	switch runtimeType {
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE:
		// Native can see all host PIDs and containers (if CRI available).
		visibility.AllPids = true
		visibility.AllContainers = criSocket != nil
		visibility.PodScope = false
		visibility.Namespace = "host"

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER:
		// Docker container can see all containers.
		visibility.AllPids = false
		visibility.AllContainers = true
		visibility.PodScope = false
		visibility.Namespace = "container"

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR:
		// Sidecar is limited to pod scope.
		visibility.AllPids = false
		visibility.AllContainers = false
		visibility.PodScope = true
		visibility.Namespace = "pod"
		// Container IDs would be discovered via CRI API if available.
		if sidecarMode == agentv1.SidecarMode_SIDECAR_MODE_CRI && criSocket != nil {
			visibility.ContainerIds = discoverPodContainers(criSocket)
		}

	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET:
		// DaemonSet can see all node PIDs and containers.
		visibility.AllPids = true
		visibility.AllContainers = true
		visibility.PodScope = false
		visibility.Namespace = "node"
	}

	d.logger.Debug().
		Bool("all_pids", visibility.AllPids).
		Bool("all_containers", visibility.AllContainers).
		Bool("pod_scope", visibility.PodScope).
		Str("namespace", visibility.Namespace).
		Int("container_count", len(visibility.ContainerIds)).
		Msg("Visibility calculated")

	return visibility
}

// logDetectionResults logs the complete detection results.
func (d *Detector) logDetectionResults(response *agentv1.RuntimeContextResponse) {
	d.logger.Info().
		Str("platform", fmt.Sprintf("%s (%s) %s", response.Platform.Os, response.Platform.OsVersion, response.Platform.Arch)).
		Str("kernel", response.Platform.Kernel).
		Str("runtime_type", response.RuntimeType.String()).
		Str("sidecar_mode", response.SidecarMode.String()).
		Bool("cri_available", response.CriSocket != nil).
		Bool("can_run", response.Capabilities.CanRun).
		Bool("can_exec", response.Capabilities.CanExec).
		Bool("can_shell", response.Capabilities.CanShell).
		Str("visibility", response.Visibility.Namespace).
		Msg("Runtime context detected")
}

// isKubernetes checks if running in Kubernetes.
func isKubernetes() bool {
	// Check for Kubernetes environment variables.
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// isSidecar determines if running as a sidecar vs DaemonSet.
func isSidecar() bool {
	// Check for sidecar-specific environment variable or annotation.
	// This is a simplified heuristic - in practice, you might check pod labels/annotations.
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return true // Default to sidecar if unsure
	}

	// DaemonSet pods typically have predictable naming patterns.
	// This is a heuristic and may need adjustment based on deployment patterns.
	return !strings.Contains(podName, "-daemonset-")
}

// isDocker checks if running in Docker container.
func isDocker() bool {
	// Check for Docker-specific files.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check cgroup for docker.
	data, err := os.ReadFile("/proc/1/cgroup")
	if err == nil && strings.Contains(string(data), "docker") {
		return true
	}

	return false
}

// hasSharedProcessNamespace checks if pod has shared process namespace.
func hasSharedProcessNamespace() bool {
	// In a shared process namespace, we can see processes from other containers.
	// Check if there are multiple different cgroup paths in /proc.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}

	cgroupPaths := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if it's a numeric directory (PID).
		if len(entry.Name()) > 0 && entry.Name()[0] >= '1' && entry.Name()[0] <= '9' {
			cgroupPath := fmt.Sprintf("/proc/%s/cgroup", entry.Name())
			data, err := os.ReadFile(cgroupPath)
			if err == nil {
				cgroupPaths[string(data)] = true
			}
		}

		// If we find multiple different cgroup paths, we have shared namespace.
		if len(cgroupPaths) > 1 {
			return true
		}
	}

	return false
}

// probeCRISocket probes a CRI socket to determine type and version.
func probeCRISocket(path string) (string, string) {
	// Determine type from path.
	var criType string
	if strings.Contains(path, "containerd") {
		criType = "containerd"
	} else if strings.Contains(path, "crio") {
		criType = "crio"
	} else if strings.Contains(path, "docker") {
		criType = "docker"
	} else {
		criType = "unknown"
	}

	// TODO: Query actual version via CRI API.
	// For now, return type with empty version.
	// This would require CRI client implementation.
	version := ""

	return criType, version
}

// discoverPodContainers discovers containers in the pod via CRI API.
func discoverPodContainers(criSocket *agentv1.CRISocketInfo) []string {
	// TODO: Implement CRI API query to list containers in pod.
	// This would use the CRI ListContainers API with pod namespace filter.
	// For now, return empty list.
	return []string{}
}
