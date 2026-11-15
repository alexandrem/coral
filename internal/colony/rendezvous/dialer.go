// Package rendezvous implements the Colony side of RFD 108: a dedicated
// long-poll loop that discovers PSK-encrypted "how to reach me" records an
// Agent published to Discovery, decrypts them, and dials the Agent back —
// still presenting the Colony's own TLS server certificate, so the existing
// RFD 048 CA-fingerprint validation on the Agent side is unchanged.
package rendezvous

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/http2"

	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/rendezvous"
)

// PSKProvider supplies the currently valid Bootstrap PSKs (active, and grace
// during a rotation window) used to derive candidate rendezvous decryption
// keys — mirroring the dual-acceptance pattern RFD 088 already uses for
// RequestCertificate.
type PSKProvider interface {
	ListValidPSKs(ctx context.Context) ([]string, error)
}

// PollClient is the subset of the Discovery client the Dialer needs.
type PollClient interface {
	PollBootstrapRendezvous(ctx context.Context, meshID string, waitSeconds int32) (*discovery.PollBootstrapRendezvousResponse, error)
	AckBootstrapRendezvous(ctx context.Context, meshID, recordID string, writeToken []byte) (bool, error)
}

// Config configures a Dialer.
type Config struct {
	// MeshID is this Colony's mesh/colony ID.
	MeshID string

	// Client is the Discovery client used for polling and acking.
	Client PollClient

	// PSKs supplies candidate decryption keys.
	PSKs PSKProvider

	// TLSConfig is the Colony's own server TLS config (same cert chain the
	// normal bootstrap listener presents). Required.
	TLSConfig *tls.Config

	// Handler serves RequestCertificate over a dialed-back connection.
	// Any request whose path does not end in "/RequestCertificate" is
	// rejected — this connection exists only to complete bootstrap, not to
	// expose the full ColonyService.
	Handler http.Handler

	// Dial opens the outbound TCP connection to a decrypted endpoint.
	// Defaults to net.Dialer.DialContext; overridable for tests.
	Dial func(ctx context.Context, endpoint string) (net.Conn, error)

	Logger zerolog.Logger
}

type recordState struct {
	lastAttempt time.Time
	attempts    int
}

// Dialer runs the Colony-side rendezvous long-poll + dial-back loop.
type Dialer struct {
	cfg Config

	mu      sync.Mutex
	backoff map[string]*recordState

	wg     sync.WaitGroup
	stopCh chan struct{}
}

// NewDialer creates a Dialer. Call Start to begin polling.
func NewDialer(cfg Config) *Dialer {
	if cfg.Dial == nil {
		var d net.Dialer
		cfg.Dial = func(ctx context.Context, endpoint string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", endpoint)
		}
	}
	return &Dialer{
		cfg:     cfg,
		backoff: make(map[string]*recordState),
		stopCh:  make(chan struct{}),
	}
}

// Start begins the dedicated long-poll goroutine. It is deliberately
// separate from any RegisterColony heartbeat loop — piggybacking on a 60s
// heartbeat cadence would waste most of a 90s rendezvous record's TTL.
func (d *Dialer) Start(ctx context.Context) {
	d.wg.Add(1)
	go d.loop(ctx)
}

// Stop signals the loop to exit and waits for in-flight dial attempts to
// finish.
func (d *Dialer) Stop() {
	close(d.stopCh)
	d.wg.Wait()
}

func (d *Dialer) loop(ctx context.Context) {
	defer d.wg.Done()
	d.cfg.Logger.Info().
		Str("event", "rendezvous_poller_started").
		Str("mesh_id", d.cfg.MeshID).
		Msg("rendezvous: Discovery long-poll loop started")
	defer d.cfg.Logger.Info().
		Str("event", "rendezvous_poller_stopped").
		Str("mesh_id", d.cfg.MeshID).
		Msg("rendezvous: Discovery long-poll loop stopped")

	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		pollCtx, cancel := context.WithTimeout(ctx, time.Duration(constants.DefaultRendezvousPollWaitSeconds)*time.Second+10*time.Second)
		resp, err := d.cfg.Client.PollBootstrapRendezvous(pollCtx, d.cfg.MeshID, int32(constants.DefaultRendezvousPollWaitSeconds))
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-d.stopCh:
				return
			default:
			}
			d.cfg.Logger.Warn().
				Str("event", "rendezvous_poll_failed").
				Err(err).
				Msg("rendezvous: poll failed")
			if !d.sleep(2 * time.Second) {
				return
			}
			continue
		}
		if len(resp.Records) > 0 {
			d.cfg.Logger.Info().
				Str("event", "rendezvous_records_received").
				Int("record_count", len(resp.Records)).
				Msg("rendezvous: bootstrap records received from Discovery")
		}

		attempted, soonest := d.processRecords(ctx, resp.Records)
		if !attempted && soonest > 0 {
			wait := soonest
			if wait > 5*time.Second {
				wait = 5 * time.Second
			}
			if !d.sleep(wait) {
				return
			}
		}
	}
}

func (d *Dialer) sleep(dur time.Duration) bool {
	select {
	case <-time.After(dur):
		return true
	case <-d.stopCh:
		return false
	}
}

// processRecords dispatches a decrypt+dial attempt for every record not
// currently inside its backoff window. It returns whether anything was
// attempted this cycle and, if not, how long until the soonest backed-off
// record becomes eligible again.
func (d *Dialer) processRecords(ctx context.Context, records []discovery.BootstrapRendezvousRecord) (attempted bool, soonestRemaining time.Duration) {
	now := time.Now()
	for _, rec := range records {
		remaining, backedOff := d.remainingBackoff(rec.RecordID, now)
		if backedOff {
			if soonestRemaining == 0 || remaining < soonestRemaining {
				soonestRemaining = remaining
			}
			continue
		}
		attempted = true
		d.wg.Add(1)
		go func(rec discovery.BootstrapRendezvousRecord) {
			defer d.wg.Done()
			d.attempt(ctx, rec)
		}(rec)
	}
	return attempted, soonestRemaining
}

// backoffDuration returns the exponential backoff (2s, 4s, 8s, capped at
// 15s) for the given number of prior attempts.
func backoffDuration(attempts int) time.Duration {
	wait := constants.DefaultRendezvousBackoffInitial
	for i := 1; i < attempts && wait < constants.DefaultRendezvousBackoffMax; i++ {
		wait *= 2
	}
	if wait > constants.DefaultRendezvousBackoffMax {
		wait = constants.DefaultRendezvousBackoffMax
	}
	return wait
}

func (d *Dialer) remainingBackoff(recordID string, now time.Time) (time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.backoff[recordID]
	if !ok {
		return 0, false
	}
	wait := backoffDuration(st.attempts)
	elapsed := now.Sub(st.lastAttempt)
	if elapsed >= wait {
		return 0, false
	}
	return wait - elapsed, true
}

func (d *Dialer) markAttempt(recordID string, now time.Time) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ok := d.backoff[recordID]
	if !ok {
		st = &recordState{}
		d.backoff[recordID] = st
	}
	st.lastAttempt = now
	st.attempts++
	return st.attempts
}

func (d *Dialer) forget(recordID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.backoff, recordID)
}

func (d *Dialer) attempt(ctx context.Context, rec discovery.BootstrapRendezvousRecord) {
	attemptNumber := d.markAttempt(rec.RecordID, time.Now())
	d.cfg.Logger.Info().
		Str("event", "rendezvous_dial_started").
		Str("record_id", rec.RecordID).
		Int("attempt", attemptNumber).
		Msg("rendezvous: processing bootstrap record and starting dial-back")

	keys, err := d.candidateKeys(ctx)
	if err != nil {
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_key_derivation_failed").
			Str("record_id", rec.RecordID).
			Err(err).
			Msg("rendezvous: failed to derive candidate keys")
		return
	}

	// Decrypt is cheap; a failure here just means a stale/garbage/foreign
	// record, not necessarily an attack. Never log plaintext endpoint,
	// nonce, or PSK material.
	payload, err := rendezvous.OpenWithKeys(keys, rec.Ciphertext, rec.GCMNonce)
	if err != nil {
		d.cfg.Logger.Debug().
			Str("event", "rendezvous_record_decryption_failed").
			Str("record_id", rec.RecordID).
			Msg("rendezvous: record did not decrypt with any known key, discarding")
		return
	}
	d.cfg.Logger.Debug().
		Str("event", "rendezvous_record_decrypted").
		Str("record_id", rec.RecordID).
		Msg("rendezvous: bootstrap record decrypted")

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := d.cfg.Dial(dialCtx, payload.Endpoint)
	cancel()
	if err != nil {
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_dial_failed").
			Str("record_id", rec.RecordID).
			Int("attempt", attemptNumber).
			Str("error", redactEndpoint(err, payload.Endpoint)).
			Msg("rendezvous: dial-back failed")
		return
	}
	d.cfg.Logger.Info().
		Str("event", "rendezvous_tcp_connected").
		Str("record_id", rec.RecordID).
		Int("attempt", attemptNumber).
		Msg("rendezvous: TCP dial-back connection established")

	// Dial direction is decoupled from TLS role (RFD 108 Key Insight):
	// the Colony dialed out, but still presents its certificate chain as
	// the TLS server.
	tlsConn := tls.Server(conn, d.cfg.TLSConfig)
	defer func() { _ = tlsConn.Close() }()

	// http2.Server.ServeConn reads ConnectionState() synchronously on
	// entry — it does not itself drive the handshake — so an explicit
	// Handshake() here is required, or ServeConn observes a zero-value
	// (pre-handshake) TLS version and immediately rejects the connection.
	if err := tlsConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_tls_deadline_failed").
			Err(err).
			Str("record_id", rec.RecordID).
			Msg("rendezvous: failed to set handshake deadline")
		return
	}
	if err := tlsConn.Handshake(); err != nil {
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_tls_handshake_failed").
			Err(err).
			Str("record_id", rec.RecordID).
			Msg("rendezvous: TLS handshake failed")
		return
	}
	if err := tlsConn.SetDeadline(time.Time{}); err != nil {
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_tls_deadline_failed").
			Err(err).
			Str("record_id", rec.RecordID).
			Msg("rendezvous: failed to clear handshake deadline")
		return
	}
	tlsState := tlsConn.ConnectionState()
	d.cfg.Logger.Info().
		Str("event", "rendezvous_tls_established").
		Str("record_id", rec.RecordID).
		Str("tls_version", tls.VersionName(tlsState.Version)).
		Str("cipher_suite", tls.CipherSuiteName(tlsState.CipherSuite)).
		Msg("rendezvous: TLS session established")

	handler := d.nonceCheckHandler(rec.RecordID, payload.SessionNonce, payload.WriteToken)

	srv := &http2.Server{}
	srv.ServeConn(tlsConn, &http2.ServeConnOpts{Handler: handler})
	d.cfg.Logger.Debug().
		Str("event", "rendezvous_session_closed").
		Str("record_id", rec.RecordID).
		Msg("rendezvous: dial-back session closed")
}

func redactEndpoint(err error, endpoint string) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), endpoint, "<redacted>")
}

func (d *Dialer) candidateKeys(ctx context.Context) ([][]byte, error) {
	psks, err := d.cfg.PSKs.ListValidPSKs(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([][]byte, 0, len(psks))
	for _, psk := range psks {
		key, err := rendezvous.DeriveKey(psk, d.cfg.MeshID)
		if err != nil {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no usable bootstrap PSKs available")
	}
	return keys, nil
}

// nonceCheckHandler validates the Coral-Rendezvous-Nonce header against the
// nonce decrypted for this specific dial attempt before forwarding to the
// RequestCertificate handler, and acks the rendezvous record on a
// successful exchange (RFD 108 Data Flow § Rendezvous Session Binding).
func (d *Dialer) nonceCheckHandler(recordID string, expectedNonce, writeToken []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/RequestCertificate") && !strings.HasSuffix(r.URL.Path, "/BootstrapAndRegister") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		headerVal := r.Header.Get(constants.RendezvousNonceHeader)
		got, err := base64.StdEncoding.DecodeString(headerVal)
		if err != nil || len(got) == 0 || subtle.ConstantTimeCompare(got, expectedNonce) != 1 {
			d.cfg.Logger.Warn().
				Str("event", "rendezvous_nonce_mismatch").
				Str("record_id", recordID).
				Msg("rendezvous: RENDEZVOUS_NONCE_MISMATCH")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		procedure := "request_certificate"
		if strings.HasSuffix(r.URL.Path, "/BootstrapAndRegister") {
			procedure = "bootstrap_and_register"
		}
		d.cfg.Logger.Info().
			Str("event", "rendezvous_request_received").
			Str("record_id", recordID).
			Str("procedure", procedure).
			Msg("rendezvous: authenticated bootstrap request received")

		// Only reached after the nonce check above succeeds. Trusted proof
		// to the handler (RFD 109) that this request arrived over an
		// RFD 108 dial-back connection for exactly this record_id — set
		// here, never accepted from the inbound request itself.
		r.Header.Set(constants.RendezvousRecordIDHeader, recordID)

		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		d.cfg.Handler.ServeHTTP(rw, r)

		if rw.status < 200 || rw.status >= 300 {
			d.cfg.Logger.Warn().
				Str("event", "rendezvous_request_failed").
				Str("record_id", recordID).
				Str("procedure", procedure).
				Int("status_code", rw.status).
				Msg("rendezvous: bootstrap request failed")
			return
		}
		d.cfg.Logger.Info().
			Str("event", "rendezvous_request_completed").
			Str("record_id", recordID).
			Str("procedure", procedure).
			Int("status_code", rw.status).
			Msg("rendezvous: bootstrap request completed")

		ackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ok, err := d.cfg.Client.AckBootstrapRendezvous(ackCtx, d.cfg.MeshID, recordID, writeToken)
		if err != nil {
			d.cfg.Logger.Warn().
				Str("event", "rendezvous_ack_failed").
				Err(err).
				Str("record_id", recordID).
				Msg("rendezvous: ack failed")
			return
		}
		if ok {
			d.forget(recordID)
			d.cfg.Logger.Info().
				Str("event", "rendezvous_record_acknowledged").
				Str("record_id", recordID).
				Msg("rendezvous: bootstrap record acknowledged")
			return
		}
		d.cfg.Logger.Warn().
			Str("event", "rendezvous_ack_rejected").
			Str("record_id", recordID).
			Msg("rendezvous: Discovery rejected bootstrap record acknowledgement")
	})
}

// statusRecorder captures the response status code written by the wrapped
// handler so the caller can decide whether to ack.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
