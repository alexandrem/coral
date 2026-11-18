package enrollment

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/jwks"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/wireguard"
)

type restorePeerDevice struct {
	peerDevice
	peer       *wireguard.PeerConfig
	handshakes int
}

func (d *restorePeerDevice) GetPeer(key string) (*wireguard.PeerConfig, bool) {
	return d.peer, d.peer != nil && d.peer.PublicKey == key
}

func (d *restorePeerDevice) AddPeer(peer *wireguard.PeerConfig) error {
	d.peer = peer
	return nil
}

func (d *restorePeerDevice) TriggerHandshake(string) error {
	d.handshakes++
	return nil
}

func TestRestorePeersRecoversRegistryWithMissingDiscoveryRecord(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(t.TempDir(), "colony-1", constants.DefaultConnectionsCacheTTL, zerolog.Nop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store, err := NewStore(db.DB())
	require.NoError(t, err)
	keys, err := NewKeyStore(store.db)
	require.NoError(t, err)
	const agentID, colonyID, ip = "agent-1", "colony-1", "100.64.0.3"
	const endpoint, pubkey = "203.0.113.1:51820", "peer-key"
	_, _, err = store.Claim(ctx, "record", "owner", DefaultLease)
	require.NoError(t, err)
	require.NoError(t, store.SetAuthorized(ctx, "record", "owner", agentID, colonyID, "ticket", time.Now().Add(time.Hour), "hash", DefaultLease))
	require.NoError(t, store.SetIPAllocated(ctx, "record", "owner", endpoint, ip, "", pubkey, DefaultLease))
	require.NoError(t, store.SetCompleted(ctx, "record", "owner", nil, nil, time.Now().Add(time.Hour), nil))
	require.NoError(t, keys.SetCurrentPubkey(ctx, agentID, colonyID, pubkey))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"agent not found"}`))
	}))
	t.Cleanup(server.Close)
	client := discovery.NewClient(server.URL)

	t.Run("reinstall peer and registry address", func(t *testing.T) {
		device := &restorePeerDevice{}
		reg := registry.New(nil)
		e := &Enroller{store: store, keys: keys, registry: reg, discoveryClient: client, wgDevice: device,
			cfg: &config.ResolvedConfig{ColonyID: colonyID}, logger: zerolog.Nop()}
		require.NoError(t, e.RestorePeers(ctx))
		require.Equal(t, endpoint, device.peer.Endpoint)
		require.Equal(t, []string{ip + "/32"}, device.peer.AllowedIPs)
		require.Equal(t, 1, device.handshakes)
		entry, err := reg.Get(agentID)
		require.NoError(t, err)
		require.Equal(t, ip, entry.MeshIPv4)
		require.True(t, entry.LastSeen.IsZero())
		require.NoError(t, reg.UpdateHeartbeat(agentID))
	})

	t.Run("keep existing peer and cached services", func(t *testing.T) {
		existing := &wireguard.PeerConfig{PublicKey: pubkey, Endpoint: "203.0.113.2:51820", AllowedIPs: []string{ip + "/32"}}
		device := &restorePeerDevice{peer: existing}
		reg := registry.New(nil)
		services := []*meshv1.ServiceInfo{{Name: "cached-service"}}
		cached, err := reg.Register(agentID, "", "", "", services, nil, "")
		require.NoError(t, err)
		e := &Enroller{store: store, keys: keys, registry: reg, discoveryClient: client, wgDevice: device,
			cfg: &config.ResolvedConfig{ColonyID: colonyID}, logger: zerolog.Nop()}
		require.NoError(t, e.RestorePeers(ctx))
		require.Same(t, existing, device.peer)
		require.Zero(t, device.handshakes)
		entry, err := reg.Get(agentID)
		require.NoError(t, err)
		require.Equal(t, ip, entry.MeshIPv4)
		require.Equal(t, services, entry.Services)
		require.Equal(t, cached.LastSeen, entry.LastSeen)
	})

	t.Run("restore removed agent peers without inventory", func(t *testing.T) {
		reg := registry.New(db)
		require.NoError(t, reg.RestoreMeshIPv4(ctx, agentID, ip))
		require.NoError(t, reg.Remove(ctx, agentID))

		reloaded := registry.New(db)
		require.NoError(t, reloaded.LoadFromDatabase(ctx))
		device := &restorePeerDevice{}
		e := &Enroller{store: store, keys: keys, registry: reloaded, discoveryClient: client, wgDevice: device,
			cfg: &config.ResolvedConfig{ColonyID: colonyID}, logger: zerolog.Nop()}
		require.NoError(t, e.RestorePeers(ctx))
		require.NotNil(t, device.peer, "a removed agent keeps mesh access so it can register again")
		require.Equal(t, []string{ip + "/32"}, device.peer.AllowedIPs)
		require.Zero(t, reloaded.Count())

		_, err := reloaded.Register(agentID, "", ip, "", nil, nil, "")
		require.NoError(t, err)
		require.Equal(t, 1, reloaded.Count(), "registering again returns the agent to inventory")
	})
}

// testCA builds a real ca.Manager with a live JWKS server backing referral
// ticket signature validation, and a PSK on file — the same construction
// TestValidateReferralTicket in the ca package uses, extended with a real
// DuckDB so Consume/IsReferralTicketConsumed and Store both work.
type testCA struct {
	manager  *ca.Manager
	signKey  ed25519.PrivateKey
	colonyID string
	psk      string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	jwkSet := jwks.JWKS{Keys: []jwks.JWK{{
		KTY: "OKP", ALG: "EdDSA", CRV: "Ed25519", KID: "test-key-1",
		X: base64.RawURLEncoding.EncodeToString(pub),
	}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jwkSet)
	}))
	t.Cleanup(server.Close)

	logger := zerolog.Nop()
	jwksClient := jwks.NewClient(server.URL, logger)
	require.NoError(t, jwksClient.Refresh())

	tmpDir := t.TempDir()
	caDir := filepath.Join(tmpDir, "ca")
	colonyID := "colony-1"
	result, err := ca.Initialize(caDir, colonyID)
	require.NoError(t, err)

	db, err := sql.Open("duckdb", filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	manager, err := ca.NewManager(db, logger, ca.Config{
		ColonyID:   colonyID,
		CADir:      caDir,
		JWKSClient: jwksClient,
	})
	require.NoError(t, err)
	require.NoError(t, manager.StorePSK(context.Background(), result.BootstrapPSK))

	return &testCA{manager: manager, signKey: priv, colonyID: colonyID, psk: result.BootstrapPSK}
}

func (tc *testCA) issueTicket(t *testing.T, agentID, colonyID, intent string, jti string, ttl time.Duration) string {
	t.Helper()
	claims := &ca.ReferralClaims{
		ReefID:   "reef-1",
		ColonyID: colonyID,
		AgentID:  agentID,
		Intent:   intent,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    "coral-discovery",
			Audience:  jwt.ClaimStrings{"coral-colony"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "test-key-1"
	tokenString, err := token.SignedString(tc.signKey)
	require.NoError(t, err)
	return tokenString
}

func testCSR(t *testing.T, agentID, colonyID string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "agent." + agentID + "." + colonyID,
			Organization: []string{colonyID},
		},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, priv)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func newTestEnroller(t *testing.T, tc *testCA) (*Enroller, *Store) {
	t.Helper()
	store := newTestStore(t)
	return &Enroller{
		caManager: tc.manager,
		store:     store,
		logger:    zerolog.Nop(),
		cfg:       nil, // authorize() never touches cfg beyond ColonyID via caManager's own colonyID.
	}, store
}

func validRequest(tc *testCA, t *testing.T, jti string) *colonyv1.BootstrapAndRegisterRequest {
	csr := testCSR(t, "agent-1", tc.colonyID)
	return &colonyv1.BootstrapAndRegisterRequest{
		Bootstrap: &colonyv1.RequestCertificateRequest{
			Jwt:          tc.issueTicket(t, "agent-1", tc.colonyID, "bootstrap", jti, time.Hour),
			Csr:          csr,
			BootstrapPsk: tc.psk,
		},
		Registration: &meshv1.RegisterRequest{
			AgentId:         "agent-1",
			ColonyId:        tc.colonyID,
			WireguardPubkey: "cGxhY2Vob2xkZXItcHVia2V5LTMyLWJ5dGVzLS0=",
		},
	}
}

func TestAuthorize_ValidRequestTransitionsToAuthorized(t *testing.T) {
	tc := newTestCA(t)
	e, store := newTestEnroller(t, tc)
	var logBuf bytes.Buffer
	e.logger = zerolog.New(&logBuf)
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	row, outcome, err := store.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)
	require.Equal(t, OutcomeOwned, outcome)

	req := validRequest(tc, t, "jti-1")
	require.NoError(t, e.authorize(ctx, row, "owner-a", req))
	require.Equal(t, PhaseAuthorized, row.Phase)
	require.Equal(t, "agent-1", row.AgentID)
	require.Equal(t, "jti-1", row.TicketJTI)

	persisted, err := store.Get(ctx, "rec-1")
	require.NoError(t, err)
	require.Equal(t, PhaseAuthorized, persisted.Phase)
	require.Contains(t, logBuf.String(), `"event":"rendezvous_enrollment_phase_changed"`)
	require.Contains(t, logBuf.String(), `"record_id":"rec-1"`)
	require.Contains(t, logBuf.String(), `"agent_id":"agent-1"`)
	require.Contains(t, logBuf.String(), `"phase":"authorized"`)
}

func TestAuthorize_WrongPSKFails(t *testing.T) {
	tc := newTestCA(t)
	e, store := newTestEnroller(t, tc)
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	row, _, err := store.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)

	req := validRequest(tc, t, "jti-1")
	req.Bootstrap.BootstrapPsk = "wrong-psk"
	err = e.authorize(ctx, row, "owner-a", req)
	require.Error(t, err)
}

func TestAuthorize_IdentityMismatchFails(t *testing.T) {
	tc := newTestCA(t)
	e, store := newTestEnroller(t, tc)
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	row, _, err := store.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)

	req := validRequest(tc, t, "jti-1")
	req.Registration.AgentId = "agent-2" // Ticket says agent-1.
	err = e.authorize(ctx, row, "owner-a", req)
	require.Error(t, err)
}

func TestAuthorize_ExpiredTicketFails(t *testing.T) {
	tc := newTestCA(t)
	e, store := newTestEnroller(t, tc)
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	row, _, err := store.Claim(ctx, "rec-1", "owner-a", DefaultLease)
	require.NoError(t, err)

	csr := testCSR(t, "agent-1", tc.colonyID)
	req := &colonyv1.BootstrapAndRegisterRequest{
		Bootstrap: &colonyv1.RequestCertificateRequest{
			Jwt:          tc.issueTicket(t, "agent-1", tc.colonyID, "bootstrap", "jti-1", -time.Hour),
			Csr:          csr,
			BootstrapPsk: tc.psk,
		},
		Registration: &meshv1.RegisterRequest{
			AgentId:         "agent-1",
			ColonyId:        tc.colonyID,
			WireguardPubkey: "cGxhY2Vob2xkZXItcHVia2V5LTMyLWJ5dGVzLS0=",
		},
	}
	err = e.authorize(ctx, row, "owner-a", req)
	require.Error(t, err)
}

// TestProcess_ClaimedPhaseFailureDeletesRow is the RFD 109 regression
// guarding that a failed Claimed-phase validation is deleted, not left
// around for a later caller to mistakenly resume.
func TestProcess_ClaimedPhaseFailureDeletesRow(t *testing.T) {
	tc := newTestCA(t)
	e, store := newTestEnroller(t, tc)
	var logBuf bytes.Buffer
	e.logger = zerolog.New(&logBuf)
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	req := validRequest(tc, t, "jti-1")
	req.Bootstrap.BootstrapPsk = "wrong-psk"

	_, err := e.BootstrapAndRegister(ctx, "rec-1", "", req)
	require.Error(t, err)

	_, err = store.Get(ctx, "rec-1")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, logBuf.String(), `"event":"rendezvous_enrollment_started"`)
	require.Contains(t, logBuf.String(), `"event":"rendezvous_enrollment_failed"`)
	require.Contains(t, logBuf.String(), `"phase":"claimed"`)
	require.Contains(t, logBuf.String(), `"failure_class":"authorization"`)
}

func TestBuildResponse_ReplaysStoredData(t *testing.T) {
	regResp := &meshv1.RegisterResponse{
		Accepted:     true,
		AssignedIp:   "100.64.0.5",
		MeshSubnet:   "100.64.0.0/16",
		RegisteredAt: timestamppb.Now(),
	}
	regBytes, err := proto.Marshal(regResp)
	require.NoError(t, err)

	row := &Row{
		RecordID:         "rec-1",
		Phase:            PhaseCompleted,
		CertificatePEM:   []byte("cert"),
		CAChain:          []byte("chain"),
		CertExpiresAt:    time.Now().Add(time.Hour),
		RegisterResponse: regBytes,
	}

	resp, err := buildResponse(row)
	require.NoError(t, err)
	require.Equal(t, []byte("cert"), resp.Certificate.Certificate)
	require.Equal(t, "100.64.0.5", resp.Registration.AssignedIp)
}

func TestRequestHash_DifferentRequestsDifferentHash(t *testing.T) {
	tc := newTestCA(t)
	req1 := validRequest(tc, t, "jti-1")
	req2 := validRequest(tc, t, "jti-2")
	require.NotEqual(t, requestHash(req1), requestHash(req2))
}

func TestRequestHash_SameRequestSameHash(t *testing.T) {
	tc := newTestCA(t)
	req := validRequest(tc, t, "jti-1")
	require.Equal(t, requestHash(req), requestHash(req))
}
