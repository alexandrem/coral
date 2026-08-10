package enrollment

import (
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
	"github.com/coral-mesh/coral/internal/colony/jwks"
	"github.com/coral-mesh/coral/internal/config"
)

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
	e.cfg = &config.ResolvedConfig{ColonyID: tc.colonyID}
	ctx := context.Background()

	req := validRequest(tc, t, "jti-1")
	req.Bootstrap.BootstrapPsk = "wrong-psk"

	_, err := e.BootstrapAndRegister(ctx, "rec-1", "", req)
	require.Error(t, err)

	_, err = store.Get(ctx, "rec-1")
	require.ErrorIs(t, err, ErrNotFound)
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
