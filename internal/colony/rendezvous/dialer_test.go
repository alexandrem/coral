package rendezvous

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/http2"

	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/rendezvous"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakePSKProvider implements PSKProvider for tests.
type fakePSKProvider struct {
	psks []string
}

func (f *fakePSKProvider) ListValidPSKs(ctx context.Context) ([]string, error) {
	if len(f.psks) == 0 {
		return nil, fmt.Errorf("no psks")
	}
	return f.psks, nil
}

// fakePollClient implements PollClient for tests, serving one batch of
// records then blocking (like a long-poll timeout) until Stop.
type fakePollClient struct {
	mu   sync.Mutex
	recs []discovery.BootstrapRendezvousRecord
	sent bool

	acked      []string
	ackAllowed map[string][]byte // recordID -> required write_token; nil map means allow-all.
}

func (f *fakePollClient) PollBootstrapRendezvous(ctx context.Context, meshID string, waitSeconds int32) (*discovery.PollBootstrapRendezvousResponse, error) {
	f.mu.Lock()
	if !f.sent {
		f.sent = true
		recs := f.recs
		f.mu.Unlock()
		return &discovery.PollBootstrapRendezvousResponse{Records: recs}, nil
	}
	f.mu.Unlock()

	// Simulate a long-poll timeout so the loop doesn't busy-spin.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return &discovery.PollBootstrapRendezvousResponse{TimedOut: true}, nil
	}
}

func (f *fakePollClient) AckBootstrapRendezvous(ctx context.Context, meshID, recordID string, writeToken []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ackAllowed != nil {
		want, ok := f.ackAllowed[recordID]
		if !ok || string(want) != string(writeToken) {
			return false, nil
		}
	}
	f.acked = append(f.acked, recordID)
	return true, nil
}

func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	cert, err := tlsSelfSignedCert()
	if err != nil {
		t.Fatalf("failed to generate self-signed cert: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
}

func TestDialerDecryptsDialsAndAcksOnSuccess(t *testing.T) {
	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	key, err := rendezvous.DeriveKey(psk, meshID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	sessionNonce, _ := rendezvous.GenerateSessionNonce()
	writeToken, _ := rendezvous.GenerateWriteToken()
	payload := rendezvous.Payload{
		Endpoint:     ln.Addr().String(),
		SessionNonce: sessionNonce,
		WriteToken:   writeToken,
		ExpiresAt:    time.Now().Add(90 * time.Second),
	}
	ciphertext, gcmNonce, err := rendezvous.Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// The Agent side of the test: accept the Colony's dial-back, run TLS
	// client, and issue a bare RequestCertificate-shaped request.
	agentDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			agentDone <- err
			return
		}
		defer func() { _ = conn.Close() }()

		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}) //nolint:gosec // test-only.
		if err := tlsConn.Handshake(); err != nil {
			agentDone <- fmt.Errorf("handshake: %w", err)
			return
		}

		req, err := http.NewRequest(http.MethodPost, "https://agent/coral.colony.v1.ColonyService/RequestCertificate", nil)
		if err != nil {
			agentDone <- err
			return
		}
		req.Header.Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString(sessionNonce))

		tr := &http2.Transport{}
		cc, err := tr.NewClientConn(tlsConn)
		if err != nil {
			agentDone <- fmt.Errorf("new client conn: %w", err)
			return
		}
		resp, err := cc.RoundTrip(req)
		if err != nil {
			agentDone <- fmt.Errorf("round trip: %w", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		agentDone <- nil
	}()

	var handlerCalled int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handlerCalled, 1)
		w.WriteHeader(http.StatusOK)
	})

	pollClient := &fakePollClient{
		recs: []discovery.BootstrapRendezvousRecord{{
			RecordID:   "rec-1",
			Ciphertext: ciphertext,
			GCMNonce:   gcmNonce,
		}},
		ackAllowed: map[string][]byte{"rec-1": writeToken},
	}
	var logBuf lockedBuffer

	d := NewDialer(Config{
		MeshID:    meshID,
		Client:    pollClient,
		PSKs:      &fakePSKProvider{psks: []string{psk}},
		TLSConfig: selfSignedTLSConfig(t),
		Handler:   handler,
		Logger:    zerolog.New(&logBuf).Level(zerolog.DebugLevel),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Stop()

	select {
	case err := <-agentDone:
		if err != nil {
			t.Fatalf("agent side failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for colony dial-back")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&handlerCalled) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&handlerCalled) != 1 {
		t.Fatal("expected RequestCertificate handler to be invoked exactly once")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pollClient.mu.Lock()
		acked := len(pollClient.acked) == 1
		pollClient.mu.Unlock()
		if acked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	pollClient.mu.Lock()
	defer pollClient.mu.Unlock()
	if len(pollClient.acked) != 1 || pollClient.acked[0] != "rec-1" {
		t.Fatalf("expected record rec-1 to be acked, got %v", pollClient.acked)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logBuf.String(), `"event":"rendezvous_record_acknowledged"`) {
		time.Sleep(10 * time.Millisecond)
	}
	logOutput := logBuf.String()
	for _, event := range []string{
		"rendezvous_records_received",
		"rendezvous_dial_started",
		"rendezvous_record_decrypted",
		"rendezvous_tcp_connected",
		"rendezvous_tls_established",
		"rendezvous_request_received",
		"rendezvous_request_completed",
		"rendezvous_record_acknowledged",
	} {
		if !strings.Contains(logOutput, `"event":"`+event+`"`) {
			t.Errorf("expected lifecycle event %q in logs:\n%s", event, logOutput)
		}
	}
}

func TestDialerRedactsDecryptedEndpointFromDialFailureLog(t *testing.T) {
	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	key, err := rendezvous.DeriveKey(psk, meshID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	endpoint := "203.0.113.10:8444"
	payload := rendezvous.Payload{
		Endpoint:     endpoint,
		SessionNonce: []byte("session-nonce"),
		WriteToken:   []byte("write-token"),
		ExpiresAt:    time.Now().Add(time.Minute),
	}
	ciphertext, gcmNonce, err := rendezvous.Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	var logBuf lockedBuffer
	d := NewDialer(Config{
		MeshID: meshID,
		PSKs:   &fakePSKProvider{psks: []string{psk}},
		Dial: func(context.Context, string) (net.Conn, error) {
			return nil, fmt.Errorf("dial tcp %s: connect: connection refused", endpoint)
		},
		Logger: zerolog.New(&logBuf).Level(zerolog.DebugLevel),
	})

	d.attempt(context.Background(), discovery.BootstrapRendezvousRecord{
		RecordID:   "rec-redacted",
		Ciphertext: ciphertext,
		GCMNonce:   gcmNonce,
	})

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, `"event":"rendezvous_dial_failed"`) {
		t.Fatalf("expected dial failure event in logs: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"record_id":"rec-redacted"`) {
		t.Fatalf("expected record correlation in logs: %s", logOutput)
	}
	if strings.Contains(logOutput, endpoint) {
		t.Fatalf("decrypted endpoint leaked into logs: %s", logOutput)
	}
	if strings.Contains(logOutput, psk) {
		t.Fatalf("PSK leaked into logs: %s", logOutput)
	}
}

func TestDialerDecryptsRecordEncryptedUnderGracePSK(t *testing.T) {
	meshID := "mesh-1"
	activePSK := "coral-psk:active000"
	gracePSK := "coral-psk:grace0000"
	graceKey, err := rendezvous.DeriveKey(gracePSK, meshID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	sessionNonce, _ := rendezvous.GenerateSessionNonce()
	writeToken, _ := rendezvous.GenerateWriteToken()
	payload := rendezvous.Payload{
		Endpoint:     ln.Addr().String(),
		SessionNonce: sessionNonce,
		WriteToken:   writeToken,
		ExpiresAt:    time.Now().Add(90 * time.Second),
	}
	// Encrypted under the PSK that's now in its rotation grace period, not
	// the current active PSK — the Agent published this before the Colony
	// rotated (RFD 088 dual-acceptance during grace).
	ciphertext, gcmNonce, err := rendezvous.Seal(graceKey, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	agentDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			agentDone <- err
			return
		}
		defer func() { _ = conn.Close() }()

		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}) //nolint:gosec // test-only.
		if err := tlsConn.Handshake(); err != nil {
			agentDone <- fmt.Errorf("handshake: %w", err)
			return
		}

		req, err := http.NewRequest(http.MethodPost, "https://agent/coral.colony.v1.ColonyService/RequestCertificate", nil)
		if err != nil {
			agentDone <- err
			return
		}
		req.Header.Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString(sessionNonce))

		tr := &http2.Transport{}
		cc, err := tr.NewClientConn(tlsConn)
		if err != nil {
			agentDone <- fmt.Errorf("new client conn: %w", err)
			return
		}
		resp, err := cc.RoundTrip(req)
		if err != nil {
			agentDone <- fmt.Errorf("round trip: %w", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		agentDone <- nil
	}()

	var handlerCalled int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handlerCalled, 1)
		w.WriteHeader(http.StatusOK)
	})

	pollClient := &fakePollClient{
		recs: []discovery.BootstrapRendezvousRecord{{
			RecordID:   "rec-grace",
			Ciphertext: ciphertext,
			GCMNonce:   gcmNonce,
		}},
		ackAllowed: map[string][]byte{"rec-grace": writeToken},
	}

	// PSKProvider returns both the active and grace PSKs, mirroring
	// ca.Manager.ListValidPSKs during a rotation grace window.
	d := NewDialer(Config{
		MeshID:    meshID,
		Client:    pollClient,
		PSKs:      &fakePSKProvider{psks: []string{activePSK, gracePSK}},
		TLSConfig: selfSignedTLSConfig(t),
		Handler:   handler,
		Logger:    zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Stop()

	select {
	case err := <-agentDone:
		if err != nil {
			t.Fatalf("agent side failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for colony dial-back")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&handlerCalled) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&handlerCalled) != 1 {
		t.Fatal("expected the record encrypted under the grace PSK to still decrypt and dial successfully")
	}
}

func TestDialerRejectsNonceMismatchWithoutInvokingHandler(t *testing.T) {
	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	key, err := rendezvous.DeriveKey(psk, meshID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	sessionNonce, _ := rendezvous.GenerateSessionNonce()
	writeToken, _ := rendezvous.GenerateWriteToken()
	payload := rendezvous.Payload{
		Endpoint:     ln.Addr().String(),
		SessionNonce: sessionNonce,
		WriteToken:   writeToken,
		ExpiresAt:    time.Now().Add(90 * time.Second),
	}
	ciphertext, gcmNonce, err := rendezvous.Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	agentDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			agentDone <- err
			return
		}
		defer func() { _ = conn.Close() }()

		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}) //nolint:gosec // test-only.
		if err := tlsConn.Handshake(); err != nil {
			agentDone <- fmt.Errorf("handshake: %w", err)
			return
		}

		req, err := http.NewRequest(http.MethodPost, "https://agent/coral.colony.v1.ColonyService/RequestCertificate", nil)
		if err != nil {
			agentDone <- err
			return
		}
		// Wrong/stale nonce.
		req.Header.Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString([]byte("not-the-real-nonce-not-the-real")))

		tr := &http2.Transport{}
		cc, err := tr.NewClientConn(tlsConn)
		if err != nil {
			agentDone <- fmt.Errorf("new client conn: %w", err)
			return
		}
		resp, err := cc.RoundTrip(req)
		if err != nil {
			// A rejected (non-2xx) response is expected here; only treat a
			// transport-level failure as a test error.
			agentDone <- nil
			return
		}
		_ = resp.Body.Close()
		agentDone <- nil
	}()

	var handlerCalled int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handlerCalled, 1)
		w.WriteHeader(http.StatusOK)
	})

	pollClient := &fakePollClient{
		recs: []discovery.BootstrapRendezvousRecord{{
			RecordID:   "rec-1",
			Ciphertext: ciphertext,
			GCMNonce:   gcmNonce,
		}},
	}

	d := NewDialer(Config{
		MeshID:    meshID,
		Client:    pollClient,
		PSKs:      &fakePSKProvider{psks: []string{psk}},
		TLSConfig: selfSignedTLSConfig(t),
		Handler:   handler,
		Logger:    zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Stop()

	select {
	case err := <-agentDone:
		if err != nil {
			t.Fatalf("agent side failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for colony dial-back")
	}

	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&handlerCalled) != 0 {
		t.Fatal("expected RequestCertificate handler NOT to be invoked on nonce mismatch")
	}
	pollClient.mu.Lock()
	defer pollClient.mu.Unlock()
	if len(pollClient.acked) != 0 {
		t.Fatal("expected the record to remain unacked after a nonce mismatch")
	}
}

// TestDialerRoutesBootstrapAndRegisterAndInjectsRecordID is the RFD 109
// counterpart of the RFD 108 nonce test: a valid-nonce request to
// /BootstrapAndRegister must reach the handler (unlike arbitrary other
// paths, which still 404), and the dialer must inject the trusted
// Coral-Rendezvous-Record-Id header carrying this dial attempt's record_id
// — the only way the handler can look up enrollment state.
func TestDialerRoutesBootstrapAndRegisterAndInjectsRecordID(t *testing.T) {
	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	key, err := rendezvous.DeriveKey(psk, meshID)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	sessionNonce, _ := rendezvous.GenerateSessionNonce()
	writeToken, _ := rendezvous.GenerateWriteToken()
	payload := rendezvous.Payload{
		Endpoint:     ln.Addr().String(),
		SessionNonce: sessionNonce,
		WriteToken:   writeToken,
		ExpiresAt:    time.Now().Add(90 * time.Second),
	}
	ciphertext, gcmNonce, err := rendezvous.Seal(key, payload)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	agentDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			agentDone <- err
			return
		}
		defer func() { _ = conn.Close() }()

		tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}) //nolint:gosec // test-only.
		if err := tlsConn.Handshake(); err != nil {
			agentDone <- fmt.Errorf("handshake: %w", err)
			return
		}
		tr := &http2.Transport{}
		cc, err := tr.NewClientConn(tlsConn)
		if err != nil {
			agentDone <- fmt.Errorf("new client conn: %w", err)
			return
		}

		// A path outside the allowlist must still 404, even with a valid nonce.
		otherReq, err := http.NewRequest(http.MethodPost, "https://agent/coral.colony.v1.ColonyService/GetStatus", nil)
		if err != nil {
			agentDone <- err
			return
		}
		otherReq.Header.Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString(sessionNonce))
		otherResp, err := cc.RoundTrip(otherReq)
		if err != nil {
			agentDone <- fmt.Errorf("other path round trip: %w", err)
			return
		}
		_ = otherResp.Body.Close()
		if otherResp.StatusCode != http.StatusNotFound {
			agentDone <- fmt.Errorf("expected 404 for GetStatus over rendezvous connection, got %d", otherResp.StatusCode)
			return
		}

		req, err := http.NewRequest(http.MethodPost, "https://agent/coral.colony.v1.ColonyService/BootstrapAndRegister", nil)
		if err != nil {
			agentDone <- err
			return
		}
		req.Header.Set(constants.RendezvousNonceHeader, base64.StdEncoding.EncodeToString(sessionNonce))
		resp, err := cc.RoundTrip(req)
		if err != nil {
			agentDone <- fmt.Errorf("bootstrap and register round trip: %w", err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			agentDone <- fmt.Errorf("expected 200 for BootstrapAndRegister over rendezvous connection, got %d", resp.StatusCode)
			return
		}
		agentDone <- nil
	}()

	var gotRecordIDHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/BootstrapAndRegister") {
			gotRecordIDHeader = r.Header.Get(constants.RendezvousRecordIDHeader)
		}
		w.WriteHeader(http.StatusOK)
	})

	pollClient := &fakePollClient{
		recs: []discovery.BootstrapRendezvousRecord{{
			RecordID:   "rec-109",
			Ciphertext: ciphertext,
			GCMNonce:   gcmNonce,
		}},
	}

	d := NewDialer(Config{
		MeshID:    meshID,
		Client:    pollClient,
		PSKs:      &fakePSKProvider{psks: []string{psk}},
		TLSConfig: selfSignedTLSConfig(t),
		Handler:   handler,
		Logger:    zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Stop()

	select {
	case err := <-agentDone:
		if err != nil {
			t.Fatalf("agent side failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for colony dial-back")
	}

	time.Sleep(200 * time.Millisecond)
	if gotRecordIDHeader != "rec-109" {
		t.Fatalf("expected the dialer to inject record_id=rec-109, got %q", gotRecordIDHeader)
	}
}

func TestDialerDiscardsRecordThatFailsToDecrypt(t *testing.T) {
	meshID := "mesh-1"
	garbage := []byte("not valid ciphertext at all, just garbage bytes")
	nonce := make([]byte, rendezvous.GCMNonceSize)

	var handlerCalled int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handlerCalled, 1)
	})

	pollClient := &fakePollClient{
		recs: []discovery.BootstrapRendezvousRecord{{
			RecordID:   "rec-garbage",
			Ciphertext: garbage,
			GCMNonce:   nonce,
		}},
	}

	dialed := int32(0)
	d := NewDialer(Config{
		MeshID:    meshID,
		Client:    pollClient,
		PSKs:      &fakePSKProvider{psks: []string{"coral-psk:abc123"}},
		TLSConfig: selfSignedTLSConfig(t),
		Handler:   handler,
		Dial: func(ctx context.Context, endpoint string) (net.Conn, error) {
			atomic.AddInt32(&dialed, 1)
			return nil, fmt.Errorf("should not be called")
		},
		Logger: zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Stop()

	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&dialed) != 0 {
		t.Fatal("expected an undecryptable record to never trigger a dial attempt")
	}
	if atomic.LoadInt32(&handlerCalled) != 0 {
		t.Fatal("expected handler not to be invoked")
	}
}

func TestBackoffDurationExponentialWithCap(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, constants.DefaultRendezvousBackoffInitial},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, constants.DefaultRendezvousBackoffMax},
		{10, constants.DefaultRendezvousBackoffMax},
	}
	for _, tc := range cases {
		got := backoffDuration(tc.attempts)
		if got != tc.want {
			t.Errorf("backoffDuration(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestDialerSkipsRecordStillInBackoffWindow(t *testing.T) {
	d := NewDialer(Config{
		MeshID: "mesh-1",
		Client: &fakePollClient{},
		PSKs:   &fakePSKProvider{},
		Logger: zerolog.Nop(),
	})

	now := time.Now()
	d.markAttempt("rec-1", now)

	if _, backedOff := d.remainingBackoff("rec-1", now); !backedOff {
		t.Fatal("expected record to be inside its backoff window immediately after an attempt")
	}
	if _, backedOff := d.remainingBackoff("rec-1", now.Add(3*time.Second)); backedOff {
		t.Fatal("expected record to be eligible again after its backoff window elapses")
	}
}
