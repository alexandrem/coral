package netobs

import (
	"testing"
)

// sampleSSOutput is a representative `ss -tnp` output fragment.
var sampleSSOutput = []byte(`Netid State  Recv-Q Send-Q     Local Address:Port     Peer Address:Port Process
tcp   ESTAB  0      0          10.0.0.1:54321        10.0.0.2:5432   users:(("psql",pid=12,fd=3))
tcp   ESTAB  0      0          10.0.0.1:34000        10.0.0.3:6379
tcp   ESTAB  0      0          10.0.0.1:44000        127.0.0.1:8080
tcp   LISTEN 0      128        0.0.0.0:8080          0.0.0.0:*
`)

func TestParseSS_Established(t *testing.T) {
	entries := parseSS(sampleSSOutput)

	// Loopback and LISTEN entries must be excluded.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (loopback excluded), got %d", len(entries))
	}

	ips := map[string]bool{}
	for _, e := range entries {
		ips[e.RemoteIP] = true
		if e.Protocol != "tcp" {
			t.Errorf("expected protocol=tcp, got %s", e.Protocol)
		}
		if e.RemotePort == 0 {
			t.Errorf("expected non-zero port for %s", e.RemoteIP)
		}
	}

	if !ips["10.0.0.2"] || !ips["10.0.0.3"] {
		t.Errorf("expected 10.0.0.2 and 10.0.0.3 in entries, got %v", ips)
	}
}

// sampleNetstatOutput is a representative `netstat -an` output fragment.
var sampleNetstatOutput = []byte(`Active Internet connections (servers and established)
Proto Recv-Q Send-Q Local Address           Foreign Address         State
tcp        0      0 10.0.0.1:54321          10.0.0.2:5432           ESTABLISHED
tcp        0      0 10.0.0.1:34000          10.0.0.3:6379           ESTABLISHED
tcp        0      0 10.0.0.1:44000          127.0.0.1:8080          ESTABLISHED
tcp        0      0 0.0.0.0:22              0.0.0.0:*               LISTEN
`)

func TestParseNetstat_Established(t *testing.T) {
	entries := parseNetstat(sampleNetstatOutput)

	// Loopback and LISTEN entries must be excluded.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (loopback and LISTEN excluded), got %d", len(entries))
	}

	for _, e := range entries {
		if e.Protocol != "tcp" {
			t.Errorf("expected protocol=tcp, got %s", e.Protocol)
		}
		if isLoopback(e.RemoteIP) {
			t.Errorf("loopback address leaked into results: %s", e.RemoteIP)
		}
	}
}

func TestParseNetstat_Deduplication(t *testing.T) {
	// Two lines for the same remote endpoint.
	data := []byte(`Proto Recv-Q Send-Q Local Address   Foreign Address  State
tcp        0      0 10.0.0.1:111    10.0.0.2:5432    ESTABLISHED
tcp        0      0 10.0.0.1:222    10.0.0.2:5432    ESTABLISHED
`)

	entries := parseNetstat(data)
	if len(entries) != 1 {
		t.Fatalf("expected deduplication to yield 1 entry, got %d", len(entries))
	}
}

func TestIsLoopback(t *testing.T) {
	cases := []struct {
		ip       string
		loopback bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.1", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isLoopback(tc.ip); got != tc.loopback {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.ip, got, tc.loopback)
		}
	}
}
