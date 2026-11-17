package bootstrap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoveryv1 "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
)

type bootstrapTokenDiscoveryService struct {
	discoveryv1connect.UnimplementedDiscoveryServiceHandler
}

func (*bootstrapTokenDiscoveryService) CreateBootstrapToken(
	context.Context,
	*connect.Request[discoveryv1.CreateBootstrapTokenRequest],
) (*connect.Response[discoveryv1.CreateBootstrapTokenResponse], error) {
	return connect.NewResponse(&discoveryv1.CreateBootstrapTokenResponse{Jwt: "test-jwt"}), nil
}

func TestClient_BootstrapValidatesRequestConnection(t *testing.T) {
	root, rootKey := generateTestCA(t, "pinned-root")
	leaf, leafKey := generateTestServerCert(t, root, rootKey, "expected-colony")
	validCert := tls.Certificate{Certificate: [][]byte{leaf.Raw, root.Raw}, PrivateKey: leafKey}
	wrongRoot, wrongRootKey := generateTestCA(t, "untrusted-root")
	wrongRootLeaf, wrongRootLeafKey := generateTestServerCert(t, wrongRoot, wrongRootKey, "expected-colony")
	wrongColonyLeaf, wrongColonyLeafKey := generateTestServerCert(t, root, rootKey, "wrong-colony")
	agentLeaf, agentLeafKey := generateTestServerCert(t, root, rootKey, "expected-colony/agent/web-1")
	clientLeaf, clientLeafKey := generateTestServerCert(t, root, rootKey, "expected-colony")
	clientLeaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	clientLeaf = reissueTestCertificate(t, clientLeaf, root, rootKey)

	for _, tc := range []struct {
		name    string
		cert    tls.Certificate
		wantErr string
	}{
		{
			name: "valid certificate",
			cert: validCert,
		},
		{
			name:    "wrong root",
			cert:    tls.Certificate{Certificate: [][]byte{wrongRootLeaf.Raw, wrongRoot.Raw}, PrivateKey: wrongRootLeafKey},
			wantErr: "root CA fingerprint mismatch",
		},
		{
			name:    "wrong colony",
			cert:    tls.Certificate{Certificate: [][]byte{wrongColonyLeaf.Raw, root.Raw}, PrivateKey: wrongColonyLeafKey},
			wantErr: "colony ID mismatch",
		},
		{
			name:    "invalid chain",
			cert:    tls.Certificate{Certificate: [][]byte{wrongRootLeaf.Raw, root.Raw}, PrivateKey: wrongRootLeafKey},
			wantErr: "certificate chain validation failed",
		},
		{
			name:    "agent identity with server auth",
			cert:    tls.Certificate{Certificate: [][]byte{agentLeaf.Raw, root.Raw}, PrivateKey: agentLeafKey},
			wantErr: "no valid Coral Colony SPIFFE ID",
		},
		{
			name:    "colony identity with client-only auth",
			cert:    tls.Certificate{Certificate: [][]byte{clientLeaf.Raw, root.Raw}, PrivateKey: clientLeafKey},
			wantErr: "certificate chain validation failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var handshakes atomic.Int32
			captured := make(chan *colonyv1.RequestCertificateRequest, 1)
			service := &fakeColonyService{onRequestCertificate: func(req *colonyv1.RequestCertificateRequest) {
				captured <- req
			}}
			_, handler := colonyv1connect.NewColonyServiceHandler(service)
			colony := httptest.NewUnstartedServer(handler)
			colony.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
				// The endpoint preflight always succeeds. The actual RPC gets
				// the test certificate on a separate TLS connection.
				if handshakes.Add(1) == 1 {
					return &tls.Config{Certificates: []tls.Certificate{validCert}}, nil
				}
				return &tls.Config{Certificates: []tls.Certificate{tc.cert}}, nil
			}}
			colony.StartTLS()
			t.Cleanup(colony.Close)

			discoveryPath, discoveryHandler := discoveryv1connect.NewDiscoveryServiceHandler(&bootstrapTokenDiscoveryService{})
			mux := http.NewServeMux()
			mux.Handle(discoveryPath, discoveryHandler)
			discovery := httptest.NewServer(mux)
			t.Cleanup(discovery.Close)

			client := NewClient(Config{
				AgentID:           "test-agent",
				ColonyID:          "expected-colony",
				CAFingerprint:     computeFingerprint(root),
				ColonyEndpoint:    colony.URL,
				DiscoveryEndpoint: discovery.URL,
				BootstrapPSK:      "test-psk",
				Logger:            zerolog.Nop(),
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := client.Bootstrap(ctx)
			assert.EqualValues(t, 2, handshakes.Load(), "must exercise the connection after preflight")
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.True(t, result == nil, "rejected bootstrap must not return credentials")
				select {
				case <-captured:
					t.Error("bootstrap credentials were sent over an unverified connection")
				default:
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			select {
			case request := <-captured:
				assert.Equal(t, "test-jwt", request.Jwt)
				assert.Equal(t, "test-psk", request.BootstrapPsk)
			default:
				t.Fatal("expected credentials to reach the validated server")
			}
		})
	}
}
