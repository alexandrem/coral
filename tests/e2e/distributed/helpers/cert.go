// Package helpers provides test utilities for e2e tests.
package helpers

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CertStatusResult contains parsed output from `coral agent cert status`.
type CertStatusResult struct {
	Status        string // Valid, Expired, ExpiringSoon, RenewalNeeded, Missing
	AgentID       string
	ColonyID      string
	SPIFFEID      string
	DaysRemaining int
	CertPath      string
	Output        string
	Err           error
}

// BootstrapResult contains parsed output from `coral agent bootstrap`.
type BootstrapResult struct {
	Success  bool
	AgentID  string
	ColonyID string
	SPIFFEID string
	CertsDir string
	Output   string
	Err      error
	ExitCode int
}

// CertRenewResult contains parsed output from `coral agent cert renew`.
type CertRenewResult struct {
	Success  bool
	Method   string // "mtls" or "discovery"
	Output   string
	Err      error
	ExitCode int
}

// AgentBootstrap executes `coral agent bootstrap` with the given parameters.
func AgentBootstrap(ctx context.Context, env map[string]string, colonyID, fingerprint, discoveryURL string, opts ...string) *BootstrapResult {
	args := []string{"agent", "bootstrap",
		"--colony", colonyID,
		"--fingerprint", fingerprint,
	}

	// Include bootstrap PSK from environment if available (RFD 088).
	if psk, ok := env["CORAL_BOOTSTRAP_PSK"]; ok && psk != "" {
		args = append(args, "--psk", psk)
	}

	if discoveryURL != "" {
		args = append(args, "--discovery", discoveryURL)
	}

	// Add any additional options.
	args = append(args, opts...)

	result := RunCLIWithEnv(ctx, env, args...)

	br := &BootstrapResult{
		Success:  result.ExitCode == 0 && result.Err == nil,
		Output:   result.Output,
		Err:      result.Err,
		ExitCode: result.ExitCode,
		ColonyID: colonyID,
	}

	// Parse output for additional info.
	if br.Success {
		br.parseSuccessOutput()
	}

	return br
}

// parseSuccessOutput parses the bootstrap output on success.
func (br *BootstrapResult) parseSuccessOutput() {
	lines := strings.Split(br.Output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Agent ID:") {
			br.AgentID = strings.TrimSpace(strings.TrimPrefix(line, "Agent ID:"))
		}
		if strings.Contains(line, "SPIFFE ID:") {
			br.SPIFFEID = strings.TrimSpace(strings.TrimPrefix(line, "SPIFFE ID:"))
		}
		if strings.Contains(line, "Certificates saved to:") {
			br.CertsDir = strings.TrimSpace(strings.TrimPrefix(line, "Certificates saved to:"))
		}
	}
}

// AgentCertStatus executes `coral agent cert status` and returns parsed result.
func AgentCertStatus(ctx context.Context, env map[string]string, certsDir string) *CertStatusResult {
	args := []string{"agent", "cert", "status"}

	if certsDir != "" {
		args = append(args, "--certs-dir", certsDir)
	}

	result := RunCLIWithEnv(ctx, env, args...)

	csr := &CertStatusResult{
		Output: result.Output,
		Err:    result.Err,
	}

	// Parse output.
	csr.parseOutput()

	return csr
}

// parseOutput parses the cert status output.
func (csr *CertStatusResult) parseOutput() {
	lines := strings.Split(csr.Output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			status := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			// Remove any ANSI codes or symbols.
			status = strings.TrimPrefix(status, "✓ ")
			status = strings.TrimPrefix(status, "✗ ")
			status = strings.TrimPrefix(status, "⚠ ")
			csr.Status = status
		}
		if strings.HasPrefix(line, "Agent ID:") {
			csr.AgentID = strings.TrimSpace(strings.TrimPrefix(line, "Agent ID:"))
		}
		if strings.HasPrefix(line, "Colony ID:") {
			csr.ColonyID = strings.TrimSpace(strings.TrimPrefix(line, "Colony ID:"))
		}
		if strings.HasPrefix(line, "SPIFFE ID:") {
			csr.SPIFFEID = strings.TrimSpace(strings.TrimPrefix(line, "SPIFFE ID:"))
		}
		if strings.HasPrefix(line, "Certificate Path:") {
			csr.CertPath = strings.TrimSpace(strings.TrimPrefix(line, "Certificate Path:"))
		}
	}

	// Determine status from output if not explicitly stated.
	if csr.Status == "" {
		if strings.Contains(csr.Output, "No certificate found") {
			csr.Status = "Missing"
		} else if strings.Contains(csr.Output, "expired") {
			csr.Status = "Expired"
		} else if strings.Contains(csr.Output, "expiring soon") {
			csr.Status = "ExpiringSoon"
		}
	}
}

// AgentCertStatusJSON executes `coral agent cert status --format json` and parses the output.
func AgentCertStatusJSON(ctx context.Context, env map[string]string, certsDir string) (map[string]interface{}, error) {
	args := []string{"agent", "cert", "status", "--format", "json"}

	if certsDir != "" {
		args = append(args, "--certs-dir", certsDir)
	}

	result := RunCLIWithEnv(ctx, env, args...)

	if result.Err != nil {
		return nil, fmt.Errorf("cert status failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var status map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return status, nil
}

// AgentCertRenew executes `coral agent cert renew` and returns parsed result.
func AgentCertRenew(ctx context.Context, env map[string]string, colonyID, fingerprint, colonyEndpoint, discoveryURL string, force bool) *CertRenewResult {
	args := []string{"agent", "cert", "renew"}

	if colonyID != "" {
		args = append(args, "--colony", colonyID)
	}

	if fingerprint != "" {
		args = append(args, "--fingerprint", fingerprint)
	}

	// Include bootstrap PSK from environment if available (RFD 088).
	if psk, ok := env["CORAL_BOOTSTRAP_PSK"]; ok && psk != "" {
		args = append(args, "--psk", psk)
	}

	if colonyEndpoint != "" {
		args = append(args, "--colony-endpoint", colonyEndpoint)
	}

	if discoveryURL != "" {
		args = append(args, "--discovery", discoveryURL)
	}

	if force {
		args = append(args, "--force")
	}

	result := RunCLIWithEnv(ctx, env, args...)

	crr := &CertRenewResult{
		Success:  result.ExitCode == 0 && result.Err == nil,
		Output:   result.Output,
		Err:      result.Err,
		ExitCode: result.ExitCode,
	}

	// Determine renewal method from output.
	if strings.Contains(crr.Output, "mTLS") || strings.Contains(crr.Output, "direct") {
		crr.Method = "mtls"
	} else if strings.Contains(crr.Output, "Discovery") {
		crr.Method = "discovery"
	}

	return crr
}

// GetColonyCAFingerprint reads the Root CA certificate from a colony's CA directory
// and returns its SHA256 fingerprint in the format "sha256:hexstring".
func GetColonyCAFingerprint(colonyDir string) (string, error) {
	rootCAPath := filepath.Join(colonyDir, "ca", "root-ca.crt")
	return GetCertFingerprint(rootCAPath)
}

// GetCertFingerprint reads a certificate file and returns its SHA256 fingerprint.
func GetCertFingerprint(certPath string) (string, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("failed to read certificate: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	fingerprint := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(fingerprint[:]), nil
}

// CreateCertsDir creates a certificate directory structure for testing.
func CreateCertsDir(baseDir string) (string, error) {
	certsDir := filepath.Join(baseDir, "certs")
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create certs directory: %w", err)
	}
	return certsDir, nil
}

// CertFilesExist checks if all expected certificate files exist in the directory.
func CertFilesExist(certsDir string) (bool, []string) {
	expectedFiles := []string{
		"agent.crt",
		"agent.key",
		"root-ca.crt",
		"ca-chain.crt",
	}

	var missing []string
	for _, file := range expectedFiles {
		path := filepath.Join(certsDir, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			missing = append(missing, file)
		}
	}

	return len(missing) == 0, missing
}

// ReadAgentCert reads and parses the agent certificate from the certs directory.
func ReadAgentCert(certsDir string) (*x509.Certificate, error) {
	certPath := filepath.Join(certsDir, "agent.crt")
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent certificate: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, nil
}

// GetSPIFFEIDFromCert extracts the SPIFFE ID from a certificate's URI SAN.
func GetSPIFFEIDFromCert(cert *x509.Certificate) string {
	for _, uri := range cert.URIs {
		if strings.HasPrefix(uri.String(), "spiffe://") {
			return uri.String()
		}
	}
	return ""
}

// CleanupCerts removes all certificate files from a directory.
func CleanupCerts(certsDir string) error {
	files := []string{"agent.crt", "agent.key", "root-ca.crt", "ca-chain.crt", "agent-id"}
	for _, file := range files {
		path := filepath.Join(certsDir, file)
		_ = os.Remove(path) // Ignore errors for non-existent files.
	}
	return nil
}
