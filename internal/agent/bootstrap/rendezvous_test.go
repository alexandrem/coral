package bootstrap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"golang.org/x/net/http2"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
	"github.com/coral-mesh/coral/internal/rendezvous"
)

func TestRendezvousBootstrapFailsFastWithoutPublicEndpoint(t *testing.T) {
	client := NewClient(Config{
		AgentID:       "agent-1",
		ColonyID:      "mesh-1",
		CAFingerprint: "sha256:aabbcc",
		BootstrapPSK:  "coral-psk:abc123",
		Logger:        zerolog.Nop(),
	})

	_, err := client.rendezvousBootstrap(context.Background(), "token")
	if err == nil {
		t.Fatal("expected an error when BootstrapPublicEndpoint is unset")
	}
}

// fakeDiscoveryService implements only PublishBootstrapRendezvous, recording
// every publish it receives; everything else returns Unimplemented.
type fakeDiscoveryService struct {
	discoveryv1connect.UnimplementedDiscoveryServiceHandler
	publishCount int32
	lastReq      atomic.Pointer[discoveryv1.PublishBootstrapRendezvousRequest]
}

func (f *fakeDiscoveryService) PublishBootstrapRendezvous(
	ctx context.Context,
	req *connect.Request[discoveryv1.PublishBootstrapRendezvousRequest],
) (*connect.Response[discoveryv1.PublishBootstrapRendezvousResponse], error) {
	atomic.AddInt32(&f.publishCount, 1)
	f.lastReq.Store(req.Msg)
	recordID := req.Msg.RecordId
	if recordID == "" {
		recordID = "rec-test"
	}
	return connect.NewResponse(&discoveryv1.PublishBootstrapRendezvousResponse{
		Success:  true,
		RecordId: recordID,
	}), nil
}

func TestRendezvousBootstrapPublishesAndCompletesOnDialBack(t *testing.T) {
	fakeSvc := &fakeDiscoveryService{}
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(fakeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	// h2c not needed: connect's JSON client over plain HTTP/1.1 test server works fine
	// for unary calls, since discovery.NewClient uses connect.WithProtoJSON() without
	// forcing HTTP/2.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	agentID := "agent-1"

	client := NewClient(Config{
		AgentID:                 agentID,
		ColonyID:                meshID,
		CAFingerprint:           "sha256:aabbcc", // overridden by manual validation below via fake colony cert
		BootstrapPSK:            psk,
		DiscoveryEndpoint:       srv.URL,
		BootstrapPublicEndpoint: "127.0.0.1:0", // not actually dialed by anyone in this test
		BootstrapListenPort:     0,
		Logger:                  zerolog.Nop(),
	})

	// Use a real fingerprint matching a freshly generated colony cert so the
	// agent's TLS validation succeeds when the fake colony dials in.
	cert, rootFingerprint, err := selfSignedColonyCert(meshID)
	if err != nil {
		t.Fatalf("failed to generate colony cert: %v", err)
	}
	client.cfg.CAFingerprint = rootFingerprint
	client.validator = NewCAValidator(rootFingerprint, meshID)

	// Kick off rendezvousBootstrap in the background; it opens a listener on
	// an OS-assigned port (BootstrapListenPort left 0 -> falls back to the
	// fixed default, which may collide in CI, so instead pick an ephemeral
	// port ourselves and pass it through).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	client.cfg.BootstrapListenPort = port

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := client.rendezvousBootstrap(context.Background(), "test-token")
		done <- outcome{res, err}
	}()

	// Wait for the first publish to land, then extract the published
	// endpoint/session_nonce/write_token by decrypting it ourselves (as the
	// real colony would).
	var payload rendezvous.Payload
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req := fakeSvc.lastReq.Load()
		if req != nil {
			key, derr := rendezvous.DeriveKey(psk, meshID)
			if derr != nil {
				t.Fatalf("DeriveKey: %v", derr)
			}
			p, oerr := rendezvous.Open(key, req.Ciphertext, req.GcmNonce)
			if oerr == nil {
				payload = p
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if payload.Endpoint == "" {
		t.Fatal("timed out waiting for a decryptable rendezvous publish")
	}

	// Now play the colony: dial the agent's listener, present the cert,
	// issue RequestCertificate with the correct nonce header.
	dialAddr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", dialAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial agent listener: %v", err)
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("colony-side handshake failed: %v", err)
	}

	colonyHandlerCalled := int32(0)
	colonySvc := &fakeColonyService{
		onRequestCertificate: func(req *colonyv1.RequestCertificateRequest) {
			atomic.AddInt32(&colonyHandlerCalled, 1)
		},
	}
	_, colonyHTTPHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)

	srv2 := &http2.Server{}
	go srv2.ServeConn(tlsConn, &http2.ServeConnOpts{Handler: colonyHTTPHandler})

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("rendezvousBootstrap failed: %v", o.err)
		}
		if o.res == nil {
			t.Fatal("expected a non-nil result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rendezvousBootstrap to complete")
	}

	if atomic.LoadInt32(&colonyHandlerCalled) != 1 {
		t.Fatal("expected the colony's RequestCertificate handler to be invoked exactly once")
	}
	_ = payload // payload.SessionNonce validated implicitly by fakeColonyService not checking header (agent side only asserts response parses).
}

// TestRendezvousBootstrapUsesCompoundRPCWhenWireGuardPubkeySet is the RFD
// 109 counterpart of TestRendezvousBootstrapPublishesAndCompletesOnDialBack:
// when the Agent has WireGuard registration data available, rendezvous
// bootstrap must call BootstrapAndRegister instead of plain
// RequestCertificate, and surface the returned RegisterResponse on Result.
func TestRendezvousBootstrapUsesCompoundRPCWhenWireGuardPubkeySet(t *testing.T) {
	fakeSvc := &fakeDiscoveryService{}
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(fakeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	meshID := "mesh-1"
	psk := "coral-psk:abc123"
	agentID := "agent-1"

	client := NewClient(Config{
		AgentID:                 agentID,
		ColonyID:                meshID,
		CAFingerprint:           "sha256:aabbcc",
		BootstrapPSK:            psk,
		DiscoveryEndpoint:       srv.URL,
		BootstrapPublicEndpoint: "127.0.0.1:0",
		WireGuardPubkey:         "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		Logger:                  zerolog.Nop(),
	})

	cert, rootFingerprint, err := selfSignedColonyCert(meshID)
	if err != nil {
		t.Fatalf("failed to generate colony cert: %v", err)
	}
	client.cfg.CAFingerprint = rootFingerprint
	client.validator = NewCAValidator(rootFingerprint, meshID)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	client.cfg.BootstrapListenPort = port

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := client.rendezvousBootstrap(context.Background(), "test-token")
		done <- outcome{res, err}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fakeSvc.lastReq.Load() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fakeSvc.lastReq.Load() == nil {
		t.Fatal("timed out waiting for a rendezvous publish")
	}

	dialAddr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", dialAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial agent listener: %v", err)
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("colony-side handshake failed: %v", err)
	}

	requestCertCalled := int32(0)
	bootstrapAndRegisterCalled := int32(0)
	var gotWireguardPubkey string
	colonySvc := &fakeColonyService{
		onRequestCertificate: func(req *colonyv1.RequestCertificateRequest) {
			atomic.AddInt32(&requestCertCalled, 1)
		},
		onBootstrapAndRegister: func(req *colonyv1.BootstrapAndRegisterRequest) {
			atomic.AddInt32(&bootstrapAndRegisterCalled, 1)
			gotWireguardPubkey = req.Registration.WireguardPubkey
		},
	}
	_, colonyHTTPHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)

	srv2 := &http2.Server{}
	go srv2.ServeConn(tlsConn, &http2.ServeConnOpts{Handler: colonyHTTPHandler})

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("rendezvousBootstrap failed: %v", o.err)
		}
		if o.res == nil {
			t.Fatal("expected a non-nil result")
		}
		if o.res.Registration == nil {
			t.Fatal("expected Result.Registration to be set for compound enrollment")
		}
		if o.res.Registration.AssignedIp != "100.64.0.5" {
			t.Fatalf("expected assigned IP from compound response, got %q", o.res.Registration.AssignedIp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rendezvousBootstrap to complete")
	}

	if atomic.LoadInt32(&requestCertCalled) != 0 {
		t.Fatal("expected plain RequestCertificate to never be called when WireGuardPubkey is set")
	}
	if atomic.LoadInt32(&bootstrapAndRegisterCalled) != 1 {
		t.Fatal("expected BootstrapAndRegister to be called exactly once")
	}
	if gotWireguardPubkey != client.cfg.WireGuardPubkey {
		t.Fatalf("expected registration to carry the configured WireGuard pubkey, got %q", gotWireguardPubkey)
	}
}

// TestRendezvousAcceptLoopSurvivesSilentConnection simulates Discovery's own
// reachability probe (a connect-only, no-TLS-data connection) landing on the
// agent's listener before the real colony dials in, and asserts it does not
// block or consume the listener (RFD 108 accept-loop requirements).
func TestRendezvousAcceptLoopSurvivesSilentConnection(t *testing.T) {
	fakeSvc := &fakeDiscoveryService{}
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(fakeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	meshID := "mesh-1"
	psk := "coral-psk:abc123"

	cert, rootFingerprint, err := selfSignedColonyCert(meshID)
	if err != nil {
		t.Fatalf("failed to generate colony cert: %v", err)
	}

	client := NewClient(Config{
		AgentID:                 "agent-1",
		ColonyID:                meshID,
		CAFingerprint:           rootFingerprint,
		BootstrapPSK:            psk,
		DiscoveryEndpoint:       srv.URL,
		BootstrapPublicEndpoint: "127.0.0.1:0",
		Logger:                  zerolog.Nop(),
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	client.cfg.BootstrapListenPort = port

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := client.rendezvousBootstrap(context.Background(), "test-token")
		done <- outcome{res, err}
	}()

	dialAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Wait for the listener to actually be up, then connect and go silent
	// (like Discovery's TCP-connect probe: never sends a TLS ClientHello).
	var probeConn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probeConn, err = net.DialTimeout("tcp", dialAddr, 200*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if probeConn == nil {
		t.Fatalf("failed to connect to agent listener: %v", err)
	}
	defer func() { _ = probeConn.Close() }()

	// Wait for the publish so we can decrypt the session nonce, exactly as
	// the success-path test does.
	var payload rendezvous.Payload
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req := fakeSvc.lastReq.Load()
		if req != nil {
			key, derr := rendezvous.DeriveKey(psk, meshID)
			if derr != nil {
				t.Fatalf("DeriveKey: %v", derr)
			}
			p, oerr := rendezvous.Open(key, req.Ciphertext, req.GcmNonce)
			if oerr == nil {
				payload = p
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if payload.Endpoint == "" {
		t.Fatal("timed out waiting for a decryptable rendezvous publish")
	}

	// Now the real colony connects, despite the silent probe still holding
	// its own connection open.
	conn, err := net.DialTimeout("tcp", dialAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial agent listener: %v", err)
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("colony-side handshake failed: %v", err)
	}

	colonySvc := &fakeColonyService{}
	_, colonyHTTPHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)
	srv2 := &http2.Server{}
	go srv2.ServeConn(tlsConn, &http2.ServeConnOpts{Handler: colonyHTTPHandler})

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("rendezvousBootstrap failed despite a concurrent silent probe connection: %v", o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: silent probe connection appears to have blocked the accept loop")
	}
}
