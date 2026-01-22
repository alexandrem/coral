// Package ca provides certificate authority management for mTLS.
package ca

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/errors"
	"github.com/coral-mesh/coral/internal/privilege"
)

// CertificateMetadata contains metadata about a stored certificate.
type CertificateMetadata struct {
	SerialNumber     string
	AgentID          string
	ColonyID         string
	CertificatePEM   string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	RevocationReason *string
	Status           string
}

// CertificateStorage defines the interface for certificate storage operations.
type CertificateStorage interface {
	// StoreCertificate stores an issued certificate.
	StoreCertificate(ctx context.Context, cert *CertificateMetadata) error

	// GetCertificate retrieves a certificate by serial number.
	GetCertificate(ctx context.Context, serialNumber string) (*CertificateMetadata, error)

	// RevokeCertificate marks a certificate as revoked.
	RevokeCertificate(ctx context.Context, serialNumber, reason, revokedBy string) error

	// ListCertificates lists all certificates, optionally filtered.
	ListCertificates(ctx context.Context, filters map[string]interface{}) ([]*CertificateMetadata, error)
}

// FilesystemStorage implements certificate storage using the filesystem.
type FilesystemStorage struct {
	caDir string
}

// NewFilesystemStorage creates a new filesystem-based certificate storage.
func NewFilesystemStorage(caDir string) *FilesystemStorage {
	return &FilesystemStorage{
		caDir: caDir,
	}
}

// SaveCertAndKey saves a certificate and private key to the filesystem.
func (f *FilesystemStorage) SaveCertAndKey(name string, certDER []byte, key *ecdsa.PrivateKey) error {
	// Save certificate.
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	certPath := filepath.Join(f.caDir, name+".crt")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key with restricted permissions.
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})
	keyPath := filepath.Join(f.caDir, name+".key")
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// LoadCert loads a certificate from the filesystem.
func (f *FilesystemStorage) LoadCert(name string) (*x509.Certificate, error) {
	certPath := filepath.Join(f.caDir, name+".crt")
	//nolint:gosec // G304: Path is constructed from trusted CA directory and validated name.
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, nil
}

// LoadKey loads a private key from the filesystem.
func (f *FilesystemStorage) LoadKey(name string) (*ecdsa.PrivateKey, error) {
	keyPath := filepath.Join(f.caDir, name+".key")
	//nolint:gosec // G304: Path is constructed from trusted CA directory and validated name.
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode key PEM")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}

	return key, nil
}

// ArchiveCertAndKey archives a certificate and key with a timestamp.
func (f *FilesystemStorage) ArchiveCertAndKey(name string) error {
	timestamp := time.Now().Format("20060102150405")
	oldCertPath := filepath.Join(f.caDir, name+".crt")
	oldKeyPath := filepath.Join(f.caDir, name+".key")

	if err := os.Rename(oldCertPath, filepath.Join(f.caDir, fmt.Sprintf("%s.old.%s.crt", name, timestamp))); err != nil {
		return fmt.Errorf("failed to archive old certificate: %w", err)
	}
	if err := os.Rename(oldKeyPath, filepath.Join(f.caDir, fmt.Sprintf("%s.old.%s.key", name, timestamp))); err != nil {
		return fmt.Errorf("failed to archive old key: %w", err)
	}

	return nil
}

// EnsureCADirectory creates the CA directory with secure permissions.
func (f *FilesystemStorage) EnsureCADirectory() error {
	if err := os.MkdirAll(f.caDir, 0700); err != nil {
		return fmt.Errorf("failed to create CA directory: %w", err)
	}
	return nil
}

// FixOwnership fixes file ownership when running as root.
func (f *FilesystemStorage) FixOwnership() error {
	if !privilege.IsRoot() {
		return nil
	}

	// Fix CA directory ownership.
	if err := privilege.FixFileOwnership(f.caDir); err != nil {
		return err
	}

	// Fix ownership of all files in the CA directory.
	entries, err := os.ReadDir(f.caDir)
	if err != nil {
		return fmt.Errorf("failed to read CA directory: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(f.caDir, entry.Name())
		if err := privilege.FixFileOwnership(path); err != nil {
			return err
		}
	}

	return nil
}

// GetCAFingerprint returns the root CA fingerprint.
func (f *FilesystemStorage) GetCAFingerprint() (string, error) {
	rootCert, err := f.LoadCert("root-ca")
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(rootCert.Raw)
	return hex.EncodeToString(hash[:]), nil
}

// CAExists checks if the CA files exist.
func (f *FilesystemStorage) CAExists() bool {
	rootCertPath := filepath.Join(f.caDir, "root-ca.crt")
	_, err := os.Stat(rootCertPath)
	return err == nil
}

// StoreCertificate is not implemented for filesystem storage.
func (f *FilesystemStorage) StoreCertificate(ctx context.Context, cert *CertificateMetadata) error {
	return fmt.Errorf("certificate metadata tracking not supported by filesystem storage")
}

// GetCertificate is not implemented for filesystem storage.
func (f *FilesystemStorage) GetCertificate(ctx context.Context, serialNumber string) (*CertificateMetadata, error) {
	return nil, fmt.Errorf("certificate metadata tracking not supported by filesystem storage")
}

// RevokeCertificate is not implemented for filesystem storage.
func (f *FilesystemStorage) RevokeCertificate(ctx context.Context, serialNumber, reason, revokedBy string) error {
	return fmt.Errorf("certificate metadata tracking not supported by filesystem storage")
}

// ListCertificates is not implemented for filesystem storage.
func (f *FilesystemStorage) ListCertificates(ctx context.Context, filters map[string]interface{}) ([]*CertificateMetadata, error) {
	return nil, fmt.Errorf("certificate metadata tracking not supported by filesystem storage")
}

// DatabaseStorage implements certificate storage using a database.
type DatabaseStorage struct {
	db               *sql.DB
	logger           zerolog.Logger
	issuedCertsTable *duckdb.Table[IssuedCertificate]
	revocationsTable *duckdb.Table[Revocation]
}

// NewDatabaseStorage creates a new database-based certificate storage.
func NewDatabaseStorage(db *sql.DB, logger zerolog.Logger) *DatabaseStorage {
	return &DatabaseStorage{
		db:               db,
		logger:           logger.With().Str("component", "ca_database").Logger(),
		issuedCertsTable: duckdb.NewTable[IssuedCertificate](db, "issued_certificates"),
		revocationsTable: duckdb.NewTable[Revocation](db, "certificate_revocations"),
	}
}

// StoreCertificate stores an issued certificate in the database.
func (d *DatabaseStorage) StoreCertificate(ctx context.Context, cert *CertificateMetadata) error {
	issuedCert := &IssuedCertificate{
		SerialNumber:     cert.SerialNumber,
		AgentID:          cert.AgentID,
		ColonyID:         cert.ColonyID,
		CertificatePEM:   cert.CertificatePEM,
		IssuedAt:         cert.IssuedAt,
		ExpiresAt:        cert.ExpiresAt,
		RevokedAt:        cert.RevokedAt,
		RevocationReason: cert.RevocationReason,
		Status:           cert.Status,
	}

	if err := d.issuedCertsTable.Insert(ctx, issuedCert); err != nil {
		return fmt.Errorf("failed to store certificate: %w", err)
	}

	return nil
}

// GetCertificate retrieves a certificate by serial number.
func (d *DatabaseStorage) GetCertificate(ctx context.Context, serialNumber string) (*CertificateMetadata, error) {
	var cert IssuedCertificate
	err := d.db.QueryRowContext(ctx, `
		SELECT serial_number, agent_id, colony_id, certificate_pem, issued_at, expires_at, revoked_at, revocation_reason, status
		FROM issued_certificates
		WHERE serial_number = ?
	`, serialNumber).Scan(
		&cert.SerialNumber,
		&cert.AgentID,
		&cert.ColonyID,
		&cert.CertificatePEM,
		&cert.IssuedAt,
		&cert.ExpiresAt,
		&cert.RevokedAt,
		&cert.RevocationReason,
		&cert.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("certificate not found")
		}
		return nil, fmt.Errorf("failed to get certificate: %w", err)
	}

	return &CertificateMetadata{
		SerialNumber:     cert.SerialNumber,
		AgentID:          cert.AgentID,
		ColonyID:         cert.ColonyID,
		CertificatePEM:   cert.CertificatePEM,
		IssuedAt:         cert.IssuedAt,
		ExpiresAt:        cert.ExpiresAt,
		RevokedAt:        cert.RevokedAt,
		RevocationReason: cert.RevocationReason,
		Status:           cert.Status,
	}, nil
}

// RevokeCertificate marks a certificate as revoked.
func (d *DatabaseStorage) RevokeCertificate(ctx context.Context, serialNumber, reason, revokedBy string) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func(tx *sql.Tx) {
		_ = tx.Rollback()
	}(tx)

	_, err = tx.ExecContext(ctx, `
		UPDATE issued_certificates
		SET status = 'revoked', revoked_at = ?, revocation_reason = ?
		WHERE serial_number = ?
	`, time.Now(), reason, serialNumber)
	if err != nil {
		return fmt.Errorf("failed to update certificate status: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO certificate_revocations (serial_number, reason, revoked_by)
		VALUES (?, ?, ?)
	`, serialNumber, reason, revokedBy)
	if err != nil {
		return fmt.Errorf("failed to record revocation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ListCertificates lists all certificates, optionally filtered.
func (d *DatabaseStorage) ListCertificates(ctx context.Context, filters map[string]interface{}) ([]*CertificateMetadata, error) {
	query := "SELECT serial_number, agent_id, colony_id, certificate_pem, issued_at, expires_at, revoked_at, revocation_reason, status FROM issued_certificates WHERE 1=1"
	args := []interface{}{}

	if agentID, ok := filters["agent_id"]; ok {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}

	if colonyID, ok := filters["colony_id"]; ok {
		query += " AND colony_id = ?"
		args = append(args, colonyID)
	}

	if status, ok := filters["status"]; ok {
		query += " AND status = ?"
		args = append(args, status)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificates: %w", err)
	}
	defer errors.DeferClose(d.logger, rows, "failed to close certificate query rows")

	var certs []*CertificateMetadata
	for rows.Next() {
		var cert CertificateMetadata
		if err := rows.Scan(
			&cert.SerialNumber,
			&cert.AgentID,
			&cert.ColonyID,
			&cert.CertificatePEM,
			&cert.IssuedAt,
			&cert.ExpiresAt,
			&cert.RevokedAt,
			&cert.RevocationReason,
			&cert.Status,
		); err != nil {
			return nil, fmt.Errorf("failed to scan certificate: %w", err)
		}
		certs = append(certs, &cert)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating certificates: %w", err)
	}

	return certs, nil
}
