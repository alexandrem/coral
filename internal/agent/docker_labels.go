package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	// defaultDockerSocket is where the Docker daemon serves its API unless
	// DOCKER_HOST says otherwise.
	defaultDockerSocket = "/var/run/docker.sock"

	// dockerAPITimeout bounds one container inspection.
	dockerAPITimeout = 2 * time.Second

	// maxCachedContainers bounds the label cache; it is cleared when full.
	maxCachedContainers = 1024
)

// mountinfoContainerPattern finds the container ID in the paths Docker
// bind-mounts into every container, such as
// /var/lib/docker/containers/<id>/hostname. It identifies the container
// when /proc/<pid>/cgroup does not, as in a private cgroup namespace.
var mountinfoContainerPattern = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// errNotDockerContainer reports a process that does not run in a container
// the Docker daemon knows.
var errNotDockerContainer = errors.New("process is not in a Docker container")

// dockerLabelMatcher matches processes against the labels of the Docker
// container they run in. Labels are read once per container through the
// Docker Engine API, since a container's labels never change.
type dockerLabelMatcher struct {
	procRoot string
	baseURL  string
	client   *http.Client

	mu    sync.Mutex
	cache map[string]map[string]string // Container ID to labels; nil labels for a non-Docker container.
}

// newDockerLabelMatcher returns a matcher for the Docker daemon named by
// DOCKER_HOST, or the default local socket. It does not connect until a
// label is first needed.
func newDockerLabelMatcher() (*dockerLabelMatcher, error) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix://" + defaultDockerSocket
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid DOCKER_HOST %q: %w", host, err)
	}

	m := &dockerLabelMatcher{procRoot: "/proc", cache: make(map[string]map[string]string)}
	switch u.Scheme {
	case "unix":
		socket := u.Path
		var dialer net.Dialer
		m.baseURL = "http://docker"
		m.client = &http.Client{
			Timeout: dockerAPITimeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socket)
				},
			},
		}
	case "tcp":
		m.baseURL = "http://" + u.Host
		m.client = &http.Client{Timeout: dockerAPITimeout}
	default:
		return nil, fmt.Errorf("unsupported DOCKER_HOST %q: only unix:// and tcp:// are supported", host)
	}
	return m, nil
}

// Matches reports whether pid runs in a Docker container that has every
// label in want with exactly that value. It returns an error, and false,
// when the container cannot be identified or the Docker API cannot be read.
func (m *dockerLabelMatcher) Matches(pid int, want map[string]string) (bool, error) {
	id, err := m.containerID(pid)
	if err != nil {
		return false, err
	}
	labels, err := m.labels(id)
	if err != nil {
		return false, err
	}
	for key, value := range want {
		if got, ok := labels[key]; !ok || got != value {
			return false, nil
		}
	}
	return true, nil
}

// containerID returns the ID of the container pid runs in, from its cgroup
// path or, failing that, from Docker's bind mounts in its mount table.
func (m *dockerLabelMatcher) containerID(pid int) (string, error) {
	dir := filepath.Join(m.procRoot, strconv.Itoa(pid))
	//nolint:gosec // G304: path is built from the proc root and a numeric PID.
	if data, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		if id := containerIDPattern.FindString(string(data)); id != "" {
			return id, nil
		}
	}
	//nolint:gosec // G304: path is built from the proc root and a numeric PID.
	data, err := os.ReadFile(filepath.Join(dir, "mountinfo"))
	if err != nil {
		return "", fmt.Errorf("failed to read mount table of process %d: %w", pid, err)
	}
	if match := mountinfoContainerPattern.FindStringSubmatch(string(data)); match != nil {
		return match[1], nil
	}
	return "", errNotDockerContainer
}

// labels returns the labels of container id, inspecting it through the
// Docker API on first use. A container the daemon does not know, such as one
// run by another runtime, has no labels.
func (m *dockerLabelMatcher) labels(id string) (map[string]string, error) {
	m.mu.Lock()
	labels, ok := m.cache[id]
	m.mu.Unlock()
	if ok {
		return labels, nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, m.baseURL+"/containers/"+id+"/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query the Docker API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var inspect struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
			return nil, fmt.Errorf("invalid Docker API response for container %s: %w", shortContainerID(id), err)
		}
		labels = inspect.Config.Labels
	case http.StatusNotFound:
		labels = nil
	default:
		return nil, fmt.Errorf("the Docker API returned %s for container %s", resp.Status, shortContainerID(id))
	}

	m.mu.Lock()
	if len(m.cache) >= maxCachedContainers {
		clear(m.cache)
	}
	m.cache[id] = labels
	m.mu.Unlock()
	return labels, nil
}

// shortContainerID abbreviates a container ID the way the Docker CLI does.
func shortContainerID(id string) string {
	return id[:min(12, len(id))]
}
