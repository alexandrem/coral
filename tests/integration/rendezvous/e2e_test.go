// Package rendezvous_test exercises RFD 108 end-to-end: a real Colony-side
// rendezvous.Dialer and a real Agent bootstrap.Client talking to each other
// through an in-memory, in-process fake Discovery service that implements
// the actual PublishBootstrapRendezvous/PollBootstrapRendezvous/
// AckBootstrapRendezvous contract (record upsert, long-poll, write_token
// hash-check). No Docker, no external services — this is what actually runs
// under `go test ./...` / `make test`.
package rendezvous_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"

	agentbootstrap "github.com/coral-mesh/coral/internal/agent/bootstrap"
	colonyrendezvous "github.com/coral-mesh/coral/internal/colony/rendezvous"
	"github.com/coral-mesh/coral/internal/discovery"
)

// --- Fake Discovery service: real record semantics, in memory. ---

type fakeRecord struct {
	recordID       string
	ciphertext     []byte
	gcmNonce       []byte
	writeTokenHash [32]byte
	expiresAt      time.Time
}

type fakeDiscoveryServer struct {
	discoveryv1connect.UnimplementedDiscoveryServiceHandler

	mu      sync.Mutex
	records map[string]*fakeRecord // mesh_id -> record (single record per mesh_id, matching this test's scope)
	waiters map[string][]chan struct{}
	nextID  int
}

func newFakeDiscoveryServer() *fakeDiscoveryServer {
	return &fakeDiscoveryServer{
		records: make(map[string]*fakeRecord),
		waiters: make(map[string][]chan struct{}),
	}
}

func (s *fakeDiscoveryServer) notify(meshID string) {
	for _, ch := range s.waiters[meshID] {
		close(ch)
	}
	delete(s.waiters, meshID)
}

func (s *fakeDiscoveryServer) CreateBootstrapToken(
	ctx context.Context,
	req *connect.Request[discoveryv1.CreateBootstrapTokenRequest],
) (*connect.Response[discoveryv1.CreateBootstrapTokenResponse], error) {
	return connect.NewResponse(&discoveryv1.CreateBootstrapTokenResponse{
		Jwt:       "test-bootstrap-token",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}), nil
}

func (s *fakeDiscoveryServer) PublishBootstrapRendezvous(
	ctx context.Context,
	req *connect.Request[discoveryv1.PublishBootstrapRendezvousRequest],
) (*connect.Response[discoveryv1.PublishBootstrapRendezvousResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hash := sha256.Sum256(req.Msg.WriteToken)

	existing := s.records[req.Msg.MeshId]
	if req.Msg.RecordId != "" {
		if existing == nil || existing.recordID != req.Msg.RecordId {
			return connect.NewResponse(&discoveryv1.PublishBootstrapRendezvousResponse{Success: false}), nil
		}
		if subtle.ConstantTimeCompare(existing.writeTokenHash[:], hash[:]) != 1 {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("write_token mismatch"))
		}
	}

	recordID := req.Msg.RecordId
	if recordID == "" {
		s.nextID++
		recordID = fmt.Sprintf("rec-%d", s.nextID)
	}

	s.records[req.Msg.MeshId] = &fakeRecord{
		recordID:       recordID,
		ciphertext:     req.Msg.Ciphertext,
		gcmNonce:       req.Msg.GcmNonce,
		writeTokenHash: hash,
		expiresAt:      time.Now().Add(time.Duration(req.Msg.TtlSeconds) * time.Second),
	}
	s.notify(req.Msg.MeshId)

	return connect.NewResponse(&discoveryv1.PublishBootstrapRendezvousResponse{
		Success:  true,
		RecordId: recordID,
	}), nil
}

func (s *fakeDiscoveryServer) PollBootstrapRendezvous(
	ctx context.Context,
	req *connect.Request[discoveryv1.PollBootstrapRendezvousRequest],
) (*connect.Response[discoveryv1.PollBootstrapRendezvousResponse], error) {
	s.mu.Lock()
	if rec := s.records[req.Msg.MeshId]; rec != nil && time.Now().Before(rec.expiresAt) {
		resp := &discoveryv1.PollBootstrapRendezvousResponse{
			Records: []*discoveryv1.BootstrapRendezvousRecord{{
				RecordId: rec.recordID, Ciphertext: rec.ciphertext, GcmNonce: rec.gcmNonce,
			}},
		}
		s.mu.Unlock()
		return connect.NewResponse(resp), nil
	}
	ch := make(chan struct{})
	s.waiters[req.Msg.MeshId] = append(s.waiters[req.Msg.MeshId], ch)
	s.mu.Unlock()

	wait := time.Duration(req.Msg.WaitSeconds) * time.Second
	select {
	case <-ch:
		s.mu.Lock()
		rec := s.records[req.Msg.MeshId]
		s.mu.Unlock()
		if rec == nil {
			return connect.NewResponse(&discoveryv1.PollBootstrapRendezvousResponse{TimedOut: true}), nil
		}
		return connect.NewResponse(&discoveryv1.PollBootstrapRendezvousResponse{
			Records: []*discoveryv1.BootstrapRendezvousRecord{{
				RecordId: rec.recordID, Ciphertext: rec.ciphertext, GcmNonce: rec.gcmNonce,
			}},
		}), nil
	case <-time.After(wait):
		return connect.NewResponse(&discoveryv1.PollBootstrapRendezvousResponse{TimedOut: true}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeDiscoveryServer) AckBootstrapRendezvous(
	ctx context.Context,
	req *connect.Request[discoveryv1.AckBootstrapRendezvousRequest],
) (*connect.Response[discoveryv1.AckBootstrapRendezvousResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.records[req.Msg.MeshId]
	if rec == nil || rec.recordID != req.Msg.RecordId {
		return connect.NewResponse(&discoveryv1.AckBootstrapRendezvousResponse{Success: true}), nil
	}
	hash := sha256.Sum256(req.Msg.WriteToken)
	if subtle.ConstantTimeCompare(rec.writeTokenHash[:], hash[:]) != 1 {
		return connect.NewResponse(&discoveryv1.AckBootstrapRendezvousResponse{Success: false}), nil
	}
	delete(s.records, req.Msg.MeshId)
	return connect.NewResponse(&discoveryv1.AckBootstrapRendezvousResponse{Success: true}), nil
}

// --- Cert helpers (colony CA chain + agent-issued cert), self-contained. ---

func genColonyCert(meshID string) (tls.Certificate, string, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	spiffeURI, err := url.Parse(fmt.Sprintf("spiffe://coral/colony/%s", meshID))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "colony-server"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		URIs: []*url.URL{spiffeURI},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, rootCert, &leafKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	sum := sha256.Sum256(rootDER)
	return tls.Certificate{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}, fmt.Sprintf("sha256:%x", sum[:]), nil
}

// fakeColonyService signs whatever CSR the Agent presents, exactly like the
// real Colony's RequestCertificate handler does at the CSR-signing level.
type fakeColonyService struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	requestCount int32
	mu           sync.Mutex
}

func (f *fakeColonyService) RequestCertificate(
	ctx context.Context,
	req *connect.Request[colonyv1.RequestCertificateRequest],
) (*connect.Response[colonyv1.RequestCertificateResponse], error) {
	f.mu.Lock()
	f.requestCount++
	f.mu.Unlock()

	block, _ := pem.Decode(req.Msg.Csr)
	if block == nil {
		return nil, fmt.Errorf("invalid CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}

	signerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "agent"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		URIs: csr.URIs,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, leafTmpl, csr.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}
	rootTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &signerKey.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&colonyv1.RequestCertificateResponse{
		Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		CaChain:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}), nil
}

// fakePSKProvider satisfies colonyrendezvous.PSKProvider with a single PSK.
type fakePSKProvider struct{ psk string }

func (f *fakePSKProvider) ListValidPSKs(ctx context.Context) ([]string, error) {
	return []string{f.psk}, nil
}

// TestNATColonyDialableAgentBootstrapViaRendezvous is the RFD 108 Phase 4
// primary scenario: a Colony with only loopback/unreachable endpoints
// completes bootstrap with a dialable Agent purely via PSK-encrypted
// rendezvous, end to end — real Dialer, real bootstrap.Client, no mocks of
// the rendezvous crypto or protocol logic itself.
func TestNATColonyDialableAgentBootstrapViaRendezvous(t *testing.T) {
	meshID := "mesh-e2e-1"
	psk := "coral-psk:" + fmt.Sprintf("%064x", 1) // valid-looking coral-psk value

	// Fake Discovery, reachable over real HTTP (connect-go JSON, matching
	// production wire format).
	fakeDiscovery := newFakeDiscoveryServer()
	path, handler := discoveryv1connect.NewDiscoveryServiceHandler(fakeDiscovery)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	discoverySrv := httptest.NewServer(mux)
	defer discoverySrv.Close()

	// Colony's own cert chain, presented as TLS server on the dial-back
	// connection (RFD 108 Key Insight: dial direction != TLS role).
	colonyCert, rootFingerprint, err := genColonyCert(meshID)
	if err != nil {
		t.Fatalf("genColonyCert: %v", err)
	}

	colonySvc := &fakeColonyService{}
	_, colonyHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)

	discoveryClient := discovery.NewClient(discoverySrv.URL)

	dialer := colonyrendezvous.NewDialer(colonyrendezvous.Config{
		MeshID: meshID,
		Client: discoveryClient,
		PSKs:   &fakePSKProvider{psk: psk},
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{colonyCert},
			MinVersion:   tls.VersionTLS13,
		},
		Handler: colonyHandler,
		Logger:  zerolog.Nop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialer.Start(ctx)
	defer dialer.Stop()

	// Agent: dialable, publishes its rendezvous record and waits for the
	// colony to connect back.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	agentPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	agentClient := agentbootstrap.NewClient(agentbootstrap.Config{
		AgentID:                 "agent-e2e-1",
		ColonyID:                meshID,
		CAFingerprint:           rootFingerprint,
		BootstrapPSK:            psk,
		DiscoveryEndpoint:       discoverySrv.URL,
		BootstrapPublicEndpoint: fmt.Sprintf("127.0.0.1:%d", agentPort),
		BootstrapListenPort:     agentPort,
		Logger:                  zerolog.Nop(),
	})

	resultCh := make(chan error, 1)
	go func() {
		bootstrapCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Bootstrap() itself discovers that direct dial is impossible (no
		// colony endpoints registered with the fake Discovery) and falls
		// back to PSK-encrypted rendezvous — exactly the RFD 108 flow a
		// real NAT'd colony + dialable agent would hit.
		res, err := agentClient.Bootstrap(bootstrapCtx)
		if err != nil {
			resultCh <- err
			return
		}
		if res == nil || res.Certificate == nil {
			resultCh <- fmt.Errorf("expected a non-nil certificate result")
			return
		}
		resultCh <- nil
	}()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("end-to-end rendezvous bootstrap failed: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for end-to-end rendezvous bootstrap")
	}

	colonySvc.mu.Lock()
	defer colonySvc.mu.Unlock()
	if colonySvc.requestCount != 1 {
		t.Fatalf("expected exactly one RequestCertificate call, got %d", colonySvc.requestCount)
	}

	// The record must have been acked, not left to linger for its TTL —
	// regression guard for the tight-redial-loop bug described in RFD 108.
	fakeDiscovery.mu.Lock()
	_, stillPresent := fakeDiscovery.records[meshID]
	fakeDiscovery.mu.Unlock()
	if stillPresent {
		t.Fatal("expected the rendezvous record to be acked and removed after a successful bootstrap")
	}
}
