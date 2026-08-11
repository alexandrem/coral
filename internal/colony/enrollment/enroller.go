package enrollment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/mesh"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// waitPollInterval and waitTimeout bound how long a caller that observed
// OutcomeWait polls for another in-flight owner to reach Completed.
const (
	waitPollInterval = 250 * time.Millisecond
	waitTimeout      = 20 * time.Second
)

var phaseOrder = map[Phase]int{
	PhaseClaimed:         0,
	PhaseAuthorized:      1,
	PhaseIPAllocated:     2,
	PhaseOldPeerRemoved:  3,
	PhaseNewPeerAdded:    4,
	PhaseRegistryUpdated: 5,
	PhaseCompleted:       6,
}

func before(phase, target Phase) bool { return phaseOrder[phase] < phaseOrder[target] }

// Enroller implements RFD 109's BootstrapAndRegister processing: compound
// certificate issuance + mesh registration over an RFD 108 rendezvous
// connection, backed by durable Store state so retries and concurrent
// deliveries of the same record_id are safe.
type Enroller struct {
	caManager       *ca.Manager
	wgDevice        *wireguard.Device
	registry        *registry.Registry
	discoveryClient *discovery.Client
	store           *Store
	keys            *KeyStore
	cfg             *config.ResolvedConfig
	logger          zerolog.Logger
}

// NewEnroller creates an Enroller.
func NewEnroller(
	caManager *ca.Manager,
	wgDevice *wireguard.Device,
	reg *registry.Registry,
	discoveryClient *discovery.Client,
	store *Store,
	keys *KeyStore,
	cfg *config.ResolvedConfig,
	logger zerolog.Logger,
) *Enroller {
	return &Enroller{
		caManager:       caManager,
		wgDevice:        wgDevice,
		registry:        reg,
		discoveryClient: discoveryClient,
		store:           store,
		keys:            keys,
		cfg:             cfg,
		logger:          logger.With().Str("component", "rendezvous-enrollment").Logger(),
	}
}

// BootstrapAndRegister processes one RFD 109 compound enrollment call.
// recordID is the RFD 108 rendezvous record_id this call arrived over
// (already nonce-verified by the caller before this is invoked); peerAddr
// is the connection's remote address, used the same way ordinary
// MeshService.Register uses it for endpoint-source matching.
func (e *Enroller) BootstrapAndRegister(ctx context.Context, recordID, peerAddr string, req *colonyv1.BootstrapAndRegisterRequest) (*colonyv1.BootstrapAndRegisterResponse, error) {
	if recordID == "" {
		return nil, fmt.Errorf("enrollment: missing rendezvous record_id")
	}
	if req.GetBootstrap() == nil || req.GetRegistration() == nil {
		return nil, fmt.Errorf("enrollment: request must carry both bootstrap and registration")
	}
	e.logger.Info().
		Str("event", "rendezvous_enrollment_started").
		Str("record_id", recordID).
		Msg("rendezvous: compound bootstrap and registration started")

	ownerID := uuid.NewString()
	row, outcome, err := e.store.Claim(ctx, recordID, ownerID, DefaultLease)
	if err != nil {
		return nil, fmt.Errorf("enrollment: failed to claim record_id=%s: %w", recordID, err)
	}

	switch outcome {
	case OutcomeReplay:
		e.logger.Info().
			Str("event", "rendezvous_enrollment_replayed").
			Str("record_id", recordID).
			Str("agent_id", row.AgentID).
			Str("phase", string(row.Phase)).
			Msg("rendezvous: replaying completed enrollment")
		resp, err := buildResponse(row)
		if err == nil {
			e.logCompleted(row, true)
		}
		return resp, err
	case OutcomeWait:
		e.logger.Info().
			Str("event", "rendezvous_enrollment_waiting").
			Str("record_id", recordID).
			Str("phase", string(row.Phase)).
			Msg("rendezvous: waiting for concurrent enrollment owner")
		completed, err := e.store.WaitForCompletion(ctx, recordID, waitPollInterval, waitTimeout)
		if err != nil {
			return nil, fmt.Errorf("enrollment: record_id=%s did not complete: %w", recordID, err)
		}
		resp, err := buildResponse(completed)
		if err == nil {
			e.logCompleted(completed, true)
		}
		return resp, err
	default: // OutcomeOwned
		e.logger.Debug().
			Str("event", "rendezvous_enrollment_claimed").
			Str("record_id", recordID).
			Str("phase", string(row.Phase)).
			Msg("rendezvous: enrollment lease acquired")
		resp, err := e.process(ctx, row, ownerID, peerAddr, req)
		if err != nil {
			e.logger.Warn().
				Str("event", "rendezvous_enrollment_failed").
				Str("record_id", recordID).
				Str("agent_id", row.AgentID).
				Str("phase", string(row.Phase)).
				Str("failure_class", enrollmentFailureClass(row.Phase)).
				Err(err).
				Msg("rendezvous: compound enrollment failed")
		}
		return resp, err
	}
}

func enrollmentFailureClass(phase Phase) string {
	switch phase {
	case PhaseClaimed:
		return "authorization"
	case PhaseAuthorized:
		return "endpoint_or_ip_allocation"
	case PhaseIPAllocated:
		return "old_peer_removal"
	case PhaseOldPeerRemoved:
		return "new_peer_addition"
	case PhaseNewPeerAdded:
		return "registry_update"
	case PhaseRegistryUpdated:
		return "certificate_issuance"
	case PhaseCompleted:
		return "response_replay"
	default:
		return "unknown"
	}
}

func (e *Enroller) logPhase(row *Row) {
	event := e.logger.Info().
		Str("event", "rendezvous_enrollment_phase_changed").
		Str("record_id", row.RecordID).
		Str("agent_id", row.AgentID).
		Str("phase", string(row.Phase))
	if row.AllocatedIP != "" {
		event = event.Str("mesh_ip", row.AllocatedIP)
	}
	event.Msg("rendezvous: enrollment phase advanced")
}

func (e *Enroller) logCompleted(row *Row, replayed bool) {
	e.logger.Info().
		Str("event", "rendezvous_enrollment_completed").
		Str("record_id", row.RecordID).
		Str("agent_id", row.AgentID).
		Str("mesh_ip", row.AllocatedIP).
		Bool("replayed", replayed).
		Msg("rendezvous: compound bootstrap and registration completed")
}

// process advances row through the remaining phases, resuming from
// row.Phase rather than starting over, and returns the final response once
// Completed.
func (e *Enroller) process(ctx context.Context, row *Row, ownerID, peerAddr string, req *colonyv1.BootstrapAndRegisterRequest) (*colonyv1.BootstrapAndRegisterResponse, error) {
	if before(row.Phase, PhaseAuthorized) {
		if err := e.authorize(ctx, row, ownerID, req); err != nil {
			// A Claimed-phase failure is deleted, not advanced (RFD 109
			// Security Model: an unauthenticated row is never treated as
			// authorization).
			e.store.Delete(ctx, row.RecordID, ownerID) //nolint:errcheck
			return nil, err
		}
	}

	if before(row.Phase, PhaseIPAllocated) {
		if err := e.allocate(ctx, row, ownerID, peerAddr, req); err != nil {
			e.store.SetLastError(ctx, row.RecordID, ownerID, err.Error())
			return nil, err
		}
	}

	if before(row.Phase, PhaseOldPeerRemoved) {
		if row.OldPubkey != "" && row.OldPubkey != row.NewPubkey {
			if err := e.wgDevice.RemovePeer(row.OldPubkey); err != nil {
				e.store.SetLastError(ctx, row.RecordID, ownerID, err.Error())
				return nil, fmt.Errorf("enrollment: failed to remove prior peer for agent_id=%s: %w", row.AgentID, err)
			}
			e.logger.Info().
				Str("event", "rendezvous_old_peer_removed").
				Str("record_id", row.RecordID).
				Str("agent_id", row.AgentID).
				Msg("rendezvous: prior Agent WireGuard peer removed")
		}
		if err := e.store.SetPhase(ctx, row.RecordID, ownerID, PhaseOldPeerRemoved, DefaultLease); err != nil {
			return nil, err
		}
		row.Phase = PhaseOldPeerRemoved
		e.logPhase(row)
	}

	if before(row.Phase, PhaseNewPeerAdded) {
		peerConfig := &wireguard.PeerConfig{
			PublicKey:           row.NewPubkey,
			Endpoint:            row.ResolvedEndpoint,
			AllowedIPs:          []string{row.AllocatedIP + "/32"},
			PersistentKeepalive: 25,
		}
		if err := e.wgDevice.AddPeer(peerConfig); err != nil {
			e.store.SetLastError(ctx, row.RecordID, ownerID, err.Error())
			return nil, fmt.Errorf("enrollment: failed to add peer for agent_id=%s: %w", row.AgentID, err)
		}
		if err := e.store.SetPhase(ctx, row.RecordID, ownerID, PhaseNewPeerAdded, DefaultLease); err != nil {
			return nil, err
		}
		row.Phase = PhaseNewPeerAdded
		e.logger.Info().
			Str("event", "rendezvous_peer_added").
			Str("record_id", row.RecordID).
			Str("agent_id", row.AgentID).
			Str("mesh_ip", row.AllocatedIP).
			Msg("rendezvous: Agent WireGuard peer added")
		e.logPhase(row)

		// Trigger an immediate handshake rather than waiting for the next
		// keepalive interval; best-effort, non-fatal.
		if err := e.wgDevice.TriggerHandshake(row.NewPubkey); err != nil {
			e.logger.Warn().
				Str("event", "rendezvous_wireguard_handshake_failed").
				Err(err).
				Str("record_id", row.RecordID).
				Str("agent_id", row.AgentID).
				Msg("rendezvous: failed to trigger immediate handshake")
		} else {
			e.logger.Info().
				Str("event", "rendezvous_wireguard_handshake_started").
				Str("record_id", row.RecordID).
				Str("agent_id", row.AgentID).
				Msg("rendezvous: immediate WireGuard handshake triggered")
		}
	}

	if before(row.Phase, PhaseRegistryUpdated) {
		if err := e.keys.SetCurrentPubkey(ctx, row.AgentID, row.ColonyID, row.NewPubkey); err != nil {
			return nil, err
		}
		reg := req.GetRegistration()
		//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		if _, err := e.registry.Register(row.AgentID, reg.ComponentName, row.AllocatedIP, "", reg.Services, reg.RuntimeContext, reg.ProtocolVersion); err != nil {
			e.logger.Warn().
				Str("event", "rendezvous_registry_update_failed").
				Err(err).
				Str("record_id", row.RecordID).
				Str("agent_id", row.AgentID).
				Msg("rendezvous: failed to register agent in registry (non-fatal)")
		} else {
			e.logger.Info().
				Str("event", "rendezvous_registry_updated").
				Str("record_id", row.RecordID).
				Str("agent_id", row.AgentID).
				Msg("rendezvous: Agent registry updated")
		}
		if err := e.store.SetPhase(ctx, row.RecordID, ownerID, PhaseRegistryUpdated, DefaultLease); err != nil {
			return nil, err
		}
		row.Phase = PhaseRegistryUpdated
		e.logPhase(row)
	}

	return e.finish(ctx, row, ownerID, req)
}

// authorize is the Claimed-phase-only validation: referral ticket, PSK, and
// identity consistency across ticket/CSR/RegisterRequest. It never mutates
// the allocator or WireGuard device.
func (e *Enroller) authorize(ctx context.Context, row *Row, ownerID string, req *colonyv1.BootstrapAndRegisterRequest) error {
	bootstrap := req.GetBootstrap()
	reg := req.GetRegistration()

	if bootstrap.GetJwt() == "" {
		return fmt.Errorf("enrollment: jwt is required")
	}
	if len(bootstrap.GetCsr()) == 0 {
		return fmt.Errorf("enrollment: csr is required")
	}
	if reg.GetWireguardPubkey() == "" {
		return fmt.Errorf("enrollment: wireguard_pubkey is required")
	}

	claims, err := e.caManager.ValidateReferralTicket(bootstrap.GetJwt())
	if err != nil {
		return fmt.Errorf("enrollment: invalid referral ticket: %w", err)
	}
	if claims.ColonyID != e.cfg.ColonyID {
		return fmt.Errorf("enrollment: colony ID mismatch")
	}

	if claims.Intent != "renew" {
		if bootstrap.GetBootstrapPsk() == "" {
			return fmt.Errorf("enrollment: bootstrap PSK is required")
		}
		if err := e.caManager.ValidateBootstrapPSK(ctx, bootstrap.GetBootstrapPsk()); err != nil {
			return fmt.Errorf("enrollment: invalid bootstrap PSK: %w", err)
		}
	}

	// Identity consistency: ticket claims, CSR subject, and RegisterRequest
	// must all agree (RFD 109 Enrollment Processing step 4).
	if reg.GetAgentId() != claims.AgentID || reg.GetColonyId() != claims.ColonyID {
		return fmt.Errorf("enrollment: identity mismatch between referral ticket and registration request")
	}
	if err := e.caManager.ValidateCSRIdentity(bootstrap.GetCsr(), claims.AgentID, claims.ColonyID); err != nil {
		return fmt.Errorf("enrollment: CSR identity mismatch: %w", err)
	}

	var ticketExpiresAt time.Time
	if claims.ExpiresAt != nil {
		ticketExpiresAt = claims.ExpiresAt.Time
	}

	if err := e.store.SetAuthorized(ctx, row.RecordID, ownerID, claims.AgentID, claims.ColonyID, claims.ID, ticketExpiresAt, requestHash(req), DefaultLease); err != nil {
		return err
	}
	row.Phase = PhaseAuthorized
	row.AgentID = claims.AgentID
	row.ColonyID = claims.ColonyID
	row.TicketJTI = claims.ID
	row.TicketExpiresAt = ticketExpiresAt
	e.logPhase(row)
	return nil
}

// allocate resolves the Agent's Discovery UDP endpoint (bound to the
// enrolling public key), allocates/recovers the stable mesh IP, and
// records the durable pre-image peer mutation resumes from.
func (e *Enroller) allocate(ctx context.Context, row *Row, ownerID, peerAddr string, req *colonyv1.BootstrapAndRegisterRequest) error {
	reg := req.GetRegistration()

	agentInfo, err := e.discoveryClient.LookupAgent(ctx, row.AgentID, row.ColonyID)
	if err != nil {
		return fmt.Errorf("enrollment: discovery lookup failed for agent_id=%s: %w", row.AgentID, err)
	}
	if agentInfo.Pubkey != reg.GetWireguardPubkey() {
		return fmt.Errorf("enrollment: discovery endpoint pubkey does not match enrolling agent's wireguard_pubkey for agent_id=%s "+
			"(the record may have been overwritten by an unrelated caller; re-register with discovery and retry)", row.AgentID)
	}

	var peerHost string
	if peerAddr != "" {
		if host, _, err := net.SplitHostPort(peerAddr); err == nil {
			peerHost = host
		}
	}

	selectedEp, selectionType := mesh.SelectBestAgentEndpoint(agentInfo.ObservedEndpoints, peerHost, e.logger, row.AgentID)
	if selectedEp == nil {
		return fmt.Errorf("enrollment: no usable Discovery UDP endpoint for agent_id=%s; configure STUN or a publicly reachable WireGuard UDP port", row.AgentID)
	}
	resolvedEndpoint := net.JoinHostPort(selectedEp.IP, fmt.Sprintf("%d", selectedEp.Port))
	e.logger.Info().
		Str("event", "rendezvous_endpoint_selected").
		Str("record_id", row.RecordID).
		Str("agent_id", row.AgentID).
		Str("endpoint", resolvedEndpoint).
		Str("selection_type", selectionType).
		Msg("rendezvous: selected Agent WireGuard endpoint from Discovery")

	allocator := e.wgDevice.Allocator()
	allocatedIP, err := allocator.Allocate(row.AgentID)
	if err != nil {
		return fmt.Errorf("enrollment: failed to allocate mesh IP for agent_id=%s: %w", row.AgentID, err)
	}

	oldPubkey, err := e.keys.CurrentPubkey(ctx, row.AgentID)
	if err != nil {
		return err
	}

	if err := e.store.SetIPAllocated(ctx, row.RecordID, ownerID, resolvedEndpoint, allocatedIP.String(), oldPubkey, reg.GetWireguardPubkey(), DefaultLease); err != nil {
		return err
	}
	row.Phase = PhaseIPAllocated
	row.ResolvedEndpoint = resolvedEndpoint
	row.AllocatedIP = allocatedIP.String()
	row.OldPubkey = oldPubkey
	row.NewPubkey = reg.GetWireguardPubkey()
	e.logPhase(row)
	return nil
}

// finish consumes the referral ticket's jti and issues the certificate —
// the last step that can fail as a fresh operation — then marks the row
// Completed.
func (e *Enroller) finish(ctx context.Context, row *Row, ownerID string, req *colonyv1.BootstrapAndRegisterRequest) (*colonyv1.BootstrapAndRegisterResponse, error) {
	consumed, err := e.caManager.IsReferralTicketConsumed(ctx, row.TicketJTI)
	if err != nil {
		return nil, err
	}
	if consumed {
		// A prior owner consumed the jti but crashed before marking the row
		// Completed. We cannot safely reissue under the same jti and have
		// no durable record of the certificate bytes to replay; surface a
		// distinct, actionable error rather than silently reissuing.
		return nil, fmt.Errorf("enrollment: record_id=%s stuck between ticket consumption and completion; "+
			"obtain a fresh referral ticket and retry bootstrap", row.RecordID)
	}
	if err := e.caManager.ConsumeReferralTicketJTI(ctx, row.TicketJTI, row.TicketExpiresAt); err != nil {
		return nil, fmt.Errorf("enrollment: failed to consume referral ticket: %w", err)
	}

	certPEM, caChain, expiresAt, err := e.caManager.IssueCertificate(row.AgentID, row.ColonyID, req.GetBootstrap().GetCsr())
	if err != nil {
		return nil, fmt.Errorf("enrollment: failed to issue certificate for agent_id=%s: %w", row.AgentID, err)
	}
	e.logger.Info().
		Str("event", "rendezvous_certificate_issued").
		Str("record_id", row.RecordID).
		Str("agent_id", row.AgentID).
		Time("expires_at", expiresAt).
		Msg("rendezvous: Agent certificate issued")

	registerResp := e.buildRegisterResponse(row)
	registerRespBytes, err := proto.Marshal(registerResp)
	if err != nil {
		return nil, fmt.Errorf("enrollment: failed to marshal registration response: %w", err)
	}

	if err := e.store.SetCompleted(ctx, row.RecordID, ownerID, certPEM, caChain, expiresAt, registerRespBytes); err != nil {
		return nil, err
	}
	row.Phase = PhaseCompleted
	e.logPhase(row)
	e.logCompleted(row, false)

	return &colonyv1.BootstrapAndRegisterResponse{
		Certificate: &colonyv1.RequestCertificateResponse{
			Certificate: certPEM,
			CaChain:     caChain,
			ExpiresAt:   expiresAt.Unix(),
		},
		Registration: registerResp,
	}, nil
}

// buildRegisterResponse constructs the ordinary MeshService.Register-shaped
// response using the row's durably recorded assignment.
func (e *Enroller) buildRegisterResponse(row *Row) *meshv1.RegisterResponse {
	peers := []*meshv1.PeerInfo{}
	for _, peer := range e.wgDevice.ListPeers() {
		if peer.PublicKey != row.NewPubkey && len(peer.AllowedIPs) > 0 {
			peers = append(peers, &meshv1.PeerInfo{
				WireguardPubkey: peer.PublicKey,
				MeshIp:          peer.AllowedIPs[0],
			})
		}
	}
	return &meshv1.RegisterResponse{
		Accepted:     true,
		AssignedIp:   row.AllocatedIP,
		MeshSubnet:   e.cfg.WireGuard.MeshNetworkIPv4,
		Peers:        peers,
		RegisteredAt: timestamppb.Now(),
	}
}

// buildResponse reconstructs a BootstrapAndRegisterResponse from a
// Completed row, for replay (no re-validation, no CA/WireGuard calls).
func buildResponse(row *Row) (*colonyv1.BootstrapAndRegisterResponse, error) {
	var reg meshv1.RegisterResponse
	if err := proto.Unmarshal(row.RegisterResponse, &reg); err != nil {
		return nil, fmt.Errorf("enrollment: failed to unmarshal stored registration response for record_id=%s: %w", row.RecordID, err)
	}
	return &colonyv1.BootstrapAndRegisterResponse{
		Certificate: &colonyv1.RequestCertificateResponse{
			Certificate: row.CertificatePEM,
			CaChain:     row.CAChain,
			ExpiresAt:   row.CertExpiresAt.Unix(),
		},
		Registration: &reg,
	}, nil
}

// requestHash is a cheap consistency fingerprint over the identity-bearing
// request fields, used to sanity-check a replay is for the same request —
// not a re-validation.
func requestHash(req *colonyv1.BootstrapAndRegisterRequest) string {
	h := sha256.New()
	h.Write([]byte(req.GetBootstrap().GetJwt()))
	h.Write(req.GetBootstrap().GetCsr())
	h.Write([]byte(req.GetRegistration().GetAgentId()))
	h.Write([]byte(req.GetRegistration().GetColonyId()))
	h.Write([]byte(req.GetRegistration().GetWireguardPubkey()))
	return hex.EncodeToString(h.Sum(nil))
}
