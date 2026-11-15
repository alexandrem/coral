package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

// selfSignedColonyCert builds a two-certificate chain [leaf, root] where the
// leaf carries a spiffe://coral/colony/<meshID> SAN (as the real colony
// server certificate does, RFD 047) and root is a self-signed CA. Returns
// the tls.Certificate to present and the expected "sha256:hex" fingerprint
// of the root, matching what CAValidator expects.
func selfSignedColonyCert(meshID string) (tls.Certificate, string, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-root-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
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
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-colony-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		URIs:         []*url.URL{spiffeURI},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCert, &leafKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	cert := tls.Certificate{
		Certificate: [][]byte{leafDER, rootDER},
		PrivateKey:  leafKey,
	}

	sum := sha256.Sum256(rootDER)
	fingerprint := fmt.Sprintf("sha256:%x", sum[:])
	return cert, fingerprint, nil
}

// fakeColonyService implements just enough of ColonyServiceHandler to
// exercise the agent's rendezvous dial-back path in tests: it signs
// whatever CSR the agent presents so the agent's own result-parsing logic
// (which checks the returned cert's public key against the CSR key) succeeds.
type fakeColonyService struct {
	colonyv1connect.UnimplementedColonyServiceHandler
	onRequestCertificate   func(*colonyv1.RequestCertificateRequest)
	onBootstrapAndRegister func(*colonyv1.BootstrapAndRegisterRequest)
	registrationResponse   *meshv1.RegisterResponse // used by BootstrapAndRegister; defaults to a minimal accepted response.
}

func (f *fakeColonyService) RequestCertificate(
	ctx context.Context,
	req *connect.Request[colonyv1.RequestCertificateRequest],
) (*connect.Response[colonyv1.RequestCertificateResponse], error) {
	if f.onRequestCertificate != nil {
		f.onRequestCertificate(req.Msg)
	}

	resp, err := issueTestCertificate(req.Msg.Csr)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(resp), nil
}

func (f *fakeColonyService) BootstrapAndRegister(
	ctx context.Context,
	req *connect.Request[colonyv1.BootstrapAndRegisterRequest],
) (*connect.Response[colonyv1.BootstrapAndRegisterResponse], error) {
	if f.onBootstrapAndRegister != nil {
		f.onBootstrapAndRegister(req.Msg)
	}

	certResp, err := issueTestCertificate(req.Msg.Bootstrap.Csr)
	if err != nil {
		return nil, err
	}

	regResp := f.registrationResponse
	if regResp == nil {
		regResp = &meshv1.RegisterResponse{
			Accepted:     true,
			AssignedIp:   "100.64.0.5",
			MeshSubnet:   "100.64.0.0/16",
			RegisteredAt: timestamppb.Now(),
		}
	}

	return connect.NewResponse(&colonyv1.BootstrapAndRegisterResponse{
		Certificate:  certResp,
		Registration: regResp,
	}), nil
}

// issueTestCertificate signs whatever CSR the agent presents so the
// agent's own result-parsing logic (which checks the returned cert's
// public key against the CSR key) succeeds.
func issueTestCertificate(csrPEM []byte) (*colonyv1.RequestCertificateResponse, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}

	signerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "test-agent"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         csr.URIs,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, leafTemplate, csr.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})

	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &signerKey.PublicKey, signerKey)
	if err != nil {
		return nil, err
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})

	return &colonyv1.RequestCertificateResponse{
		Certificate: certPEM,
		CaChain:     rootPEM,
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	}, nil
}
