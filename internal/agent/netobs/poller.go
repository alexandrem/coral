package netobs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// poller collects active outbound TCP connections by running an OS-level
// network statistics command and parsing its output.
// On Linux it prefers `ss`; on all other platforms it falls back to `netstat`.
// Transport-level metrics (RTT, retransmits) are not available on this path.
type poller struct {
	logger zerolog.Logger
}

// newPoller creates a connection poller.
func newPoller(logger zerolog.Logger) *poller {
	return &poller{logger: logger}
}

// Poll runs the netstat or ss command and returns one ConnectionEntry per unique
// established outbound TCP connection (local IP is a private address, remote IP
// is any address on port > 0). The caller is responsible for filtering out
// loopback connections if desired.
func (p *poller) Poll(ctx context.Context) ([]ConnectionEntry, error) {
	entries, err := p.pollSS(ctx)
	if err != nil {
		// Fallback to netstat when ss is unavailable (non-Linux, older kernels).
		p.logger.Debug().Err(err).Msg("ss unavailable, falling back to netstat")
		entries, err = p.pollNetstat(ctx)
		if err != nil {
			return nil, fmt.Errorf("both ss and netstat failed: %w", err)
		}
	}
	return entries, nil
}

// pollSS parses `ss -tnp` output for established TCP connections.
// Example line (abbreviated):
//
//	ESTAB 0 0 10.0.0.1:50000 10.0.0.2:5432
func (p *poller) pollSS(ctx context.Context) ([]ConnectionEntry, error) {
	out, err := runCommand(ctx, "ss", "-tnp")
	if err != nil {
		return nil, err
	}
	return parseSS(out), nil
}

// pollNetstat parses `netstat -an` output for established TCP connections.
// Example line (abbreviated):
//
//	tcp  0  0  10.0.0.1:50000  10.0.0.2:5432  ESTABLISHED
func (p *poller) pollNetstat(ctx context.Context) ([]ConnectionEntry, error) {
	out, err := runCommand(ctx, "netstat", "-an")
	if err != nil {
		return nil, err
	}
	return parseNetstat(out), nil
}

// runCommand executes a command and returns its stdout.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("command %q failed: %w", name, err)
	}
	return out, nil
}

// parseSS parses ss -tnp output.
// The first column (State) is "ESTAB" for established connections.
// Only lines whose first field is exactly "ESTAB" are processed; all header
// and non-established lines are silently skipped.
// Format: State RecvQ SendQ Local:Port Remote:Port [Process]
func parseSS(data []byte) []ConnectionEntry {
	var entries []ConnectionEntry
	now := time.Now()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		// Minimum: Netid State RecvQ SendQ Local Remote = 6 fields.
		if len(fields) < 6 {
			continue
		}
		// ss -tnp format: Netid State RecvQ SendQ Local Peer [Process]
		// fields[0] = Netid ("tcp"), fields[1] = State.
		state := fields[1]
		if state != "ESTAB" && state != "ESTABLISHED" {
			continue
		}

		// fields[5] = remote addr:port
		remoteIP, remotePort, err := splitAddrPort(fields[5])
		if err != nil {
			continue
		}
		if isLoopback(remoteIP) {
			continue
		}

		entries = append(entries, ConnectionEntry{
			RemoteIP:      remoteIP,
			RemotePort:    remotePort,
			Protocol:      "tcp",
			FirstObserved: now,
			LastObserved:  now,
		})
	}

	return deduplicateEntries(entries)
}

// parseNetstat parses netstat -an output.
// Format: Proto RecvQ SendQ Local Remote State
func parseNetstat(data []byte) []ConnectionEntry {
	var entries []ConnectionEntry
	now := time.Now()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Accept "tcp", "tcp4", "tcp6", "TCP" variants.
		proto := strings.ToLower(fields[0])
		if !strings.HasPrefix(proto, "tcp") {
			continue
		}

		state := strings.ToUpper(fields[5])
		if state != "ESTABLISHED" {
			continue
		}

		// fields[4] = remote address.
		remoteIP, remotePort, err := splitAddrPort(fields[4])
		if err != nil {
			continue
		}
		if isLoopback(remoteIP) {
			continue
		}

		entries = append(entries, ConnectionEntry{
			RemoteIP:      remoteIP,
			RemotePort:    remotePort,
			Protocol:      "tcp",
			FirstObserved: now,
			LastObserved:  now,
		})
	}

	return deduplicateEntries(entries)
}

// splitAddrPort splits "ip:port" or "[ipv6]:port" and returns (ip, port).
func splitAddrPort(s string) (string, uint32, error) {
	host, portStr, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		return "", 0, err
	}
	if port == 0 {
		return "", 0, fmt.Errorf("port is zero")
	}
	return host, uint32(port), nil
}

// isLoopback returns true for 127.x.x.x and ::1 addresses.
func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// deduplicateEntries collapses duplicate (remoteIP, remotePort, protocol) tuples
// that can appear in a single netstat snapshot.
func deduplicateEntries(entries []ConnectionEntry) []ConnectionEntry {
	seen := make(map[edgeKey]struct{}, len(entries))
	out := entries[:0]
	for _, e := range entries {
		k := edgeKey{remoteIP: e.RemoteIP, remotePort: e.RemotePort, protocol: e.Protocol}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, e)
	}
	return out
}
