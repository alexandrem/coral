package netobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// procNetTCPPaths lists the kernel socket tables read on Linux.
var procNetTCPPaths = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// tcpEstablished is the /proc/net/tcp state code for TCP_ESTABLISHED.
const tcpEstablished = "01"

// poller collects active outbound TCP connections.
// On Linux it reads the kernel socket tables in /proc/net directly; on other
// platforms it falls back to parsing `netstat -an` output.
// Transport-level metrics (RTT, retransmits) are not available on this path.
type poller struct {
	logger zerolog.Logger
}

// newPoller creates a connection poller.
func newPoller(logger zerolog.Logger) *poller {
	return &poller{logger: logger}
}

// Poll returns one ConnectionEntry per unique established TCP connection to a
// non-loopback remote endpoint.
func (p *poller) Poll(ctx context.Context) ([]ConnectionEntry, error) {
	entries, err := pollProcNetTCP()
	if err != nil {
		// Fallback to netstat when procfs is unavailable (non-Linux).
		p.logger.Debug().Err(err).Msg("procfs socket tables unavailable, falling back to netstat")
		entries, err = p.pollNetstat(ctx)
		if err != nil {
			return nil, fmt.Errorf("both procfs and netstat failed: %w", err)
		}
	}
	return entries, nil
}

// pollProcNetTCP reads established TCP connections from /proc/net/tcp and
// /proc/net/tcp6. It fails only when none of the tables can be read, so hosts
// with IPv6 disabled still report IPv4 connections.
func pollProcNetTCP() ([]ConnectionEntry, error) {
	var (
		entries []ConnectionEntry
		readAny bool
		lastErr error
	)
	for _, path := range procNetTCPPaths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: fixed /proc paths.
		if err != nil {
			lastErr = err
			continue
		}
		readAny = true
		entries = append(entries, parseProcNetTCP(data)...)
	}
	if !readAny {
		return nil, lastErr
	}
	return deduplicateEntries(entries), nil
}

// parseProcNetTCP parses a /proc/net/tcp or /proc/net/tcp6 table and returns
// established connections to non-loopback remote endpoints.
// Format: sl local_address rem_address st ...
// Addresses are "<hex IP>:<hex port>"; the IP is a sequence of 32-bit words
// printed in host byte order, and the port is already in host byte order.
func parseProcNetTCP(data []byte) []ConnectionEntry {
	var entries []ConnectionEntry
	now := time.Now()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Scan() // Skip header line.
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[3] != tcpEstablished {
			continue
		}

		remote, err := parseProcNetAddr(fields[2])
		if err != nil || remote.Port() == 0 || remote.Addr().IsLoopback() {
			continue
		}

		entries = append(entries, ConnectionEntry{
			RemoteIP:      remote.Addr().String(),
			RemotePort:    uint32(remote.Port()),
			Protocol:      "tcp",
			FirstObserved: now,
			LastObserved:  now,
		})
	}

	return entries
}

// parseProcNetAddr decodes a /proc/net/tcp[6] "<hex IP>:<hex port>" address.
// IPv4-mapped IPv6 addresses are unmapped to plain IPv4.
func parseProcNetAddr(s string) (netip.AddrPort, error) {
	ipHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("malformed address %q", s)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("malformed port in %q: %w", s, err)
	}
	raw, err := hex.DecodeString(ipHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, fmt.Errorf("malformed IP in %q", s)
	}
	// Each 32-bit word was printed as a host-order integer; restore the
	// network byte order.
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(raw[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), nil
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
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: callers use a fixed netstat command.
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("command %q failed: %w", name, err)
	}
	return out, nil
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
