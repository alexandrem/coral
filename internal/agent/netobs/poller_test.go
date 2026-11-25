package netobs

import (
	"encoding/binary"
	"testing"
)

// sampleProcNetTCP is a /proc/net/tcp fragment captured on a little-endian
// host: one ESTABLISHED connection to 1.1.1.1:80, one TIME_WAIT, one LISTEN,
// one ESTABLISHED loopback connection, and a duplicate of the first remote.
var sampleProcNetTCP = []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 030011AC:92E1 01010101:0050 01 00000000:00000000 00:00000000 00000000     0        0 23720 2 0000000000000000 20 0 0 10 -1
   1: 030011AC:96D6 84426597:01BB 06 00000000:00000000 03:000016D7 00000000     0        0 0 3 0000000000000000
   2: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1234 1 0000000000000000 100 0 0 10 0
   3: 0100007F:ABCD 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 1235 1 0000000000000000 20 0 0 10 -1
   4: 030011AC:92E2 01010101:0050 01 00000000:00000000 00:00000000 00000000     0        0 23721 2 0000000000000000 20 0 0 10 -1
`)

// sampleProcNetTCP6 is a /proc/net/tcp6 fragment (little-endian host) with an
// IPv4-mapped peer (::ffff:10.0.0.2:5432), a native IPv6 peer
// ([2001:db8::1]:443), and a loopback peer ([::1]:8080).
var sampleProcNetTCP6 = []byte(`  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0000000000000000FFFF00000100000A:D431 0000000000000000FFFF00000200000A:1538 01 00000000:00000000 00:00000000 00000000     0        0 3001 1 0000000000000000 20 0 0 10 -1
   1: B80D0120000000000000000002000000:D432 B80D0120000000000000000001000000:01BB 01 00000000:00000000 00:00000000 00000000     0        0 3002 1 0000000000000000 20 0 0 10 -1
   2: 00000000000000000000000001000000:D433 00000000000000000000000001000000:1F90 01 00000000:00000000 00:00000000 00000000     0        0 3003 1 0000000000000000 20 0 0 10 -1
`)

func TestParseProcNetTCP_IPv4(t *testing.T) {
	if !littleEndian() {
		t.Skip("samples were captured on a little-endian host")
	}
	entries := deduplicateEntries(parseProcNetTCP(sampleProcNetTCP))

	// Non-established, loopback, and duplicate entries must be excluded.
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(entries), entries)
	}
	if e := entries[0]; e.RemoteIP != "1.1.1.1" || e.RemotePort != 80 || e.Protocol != "tcp" {
		t.Errorf("unexpected entry %+v", e)
	}
}

func TestParseProcNetTCP_IPv6(t *testing.T) {
	if !littleEndian() {
		t.Skip("samples were captured on a little-endian host")
	}
	entries := parseProcNetTCP(sampleProcNetTCP6)

	got := map[string]uint32{}
	for _, e := range entries {
		got[e.RemoteIP] = e.RemotePort
	}
	want := map[string]uint32{"10.0.0.2": 5432, "2001:db8::1": 443}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for ip, port := range want {
		if got[ip] != port {
			t.Errorf("expected %s:%d, got %v", ip, port, got)
		}
	}
}

func TestParseProcNetAddr_Malformed(t *testing.T) {
	for _, s := range []string{"", "0100007F", "0100007F:XYZ", "01007F:0050", "ZZ00007F:0050"} {
		if _, err := parseProcNetAddr(s); err == nil {
			t.Errorf("parseProcNetAddr(%q) succeeded, want error", s)
		}
	}
}

// littleEndian reports whether the host stores integers little-endian.
func littleEndian() bool {
	var b [2]byte
	binary.NativeEndian.PutUint16(b[:], 1)
	return b[0] == 1
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
