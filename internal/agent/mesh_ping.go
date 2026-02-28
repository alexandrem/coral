package agent

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/constants"
)

// meshPingStatus represents the operational state of the MeshPingServer.
type meshPingStatus string

const (
	meshPingRunning meshPingStatus = "running"
	meshPingStopped meshPingStatus = "stopped"
	meshPingFailed  meshPingStatus = "failed"
)

// MeshPingServer implements a tiny UDP echo receiver for mesh connectivity testing (RFD 097).
// It listens on the mesh interface and responds to UDP pings, verifying the user-space
// cryptography routing path independently of kernel ICMP filtering.
type MeshPingServer struct {
	logger zerolog.Logger
	meshIP string
	port   int
	conn   *net.UDPConn
	mu     sync.Mutex
	status meshPingStatus
}

// NewMeshPingServer creates a new mesh ping server.
func NewMeshPingServer(meshIP string, logger zerolog.Logger) *MeshPingServer {
	return &MeshPingServer{
		logger: logger.With().Str("component", "mesh-ping").Logger(),
		meshIP: meshIP,
		port:   constants.DefaultMeshPingPort,
		status: meshPingStopped,
	}
}

// Start starts the UDP echo receiver.
func (s *MeshPingServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.status == meshPingRunning {
		s.mu.Unlock()
		return nil
	}

	// Listen on all interfaces, but the ping is expected through wg0 (mesh IP).
	// Binding strictly to meshIP might fail if the interface is not up yet or
	// if the IP is not assigned when we start.
	// However, the request says "alongside the WireGuard socket", and
	// "dispatches an encrypted UDP ping directly through its wg0 interface to the assigned mesh_ip".

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		s.status = meshPingFailed
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on UDP port %d: %w", s.port, err)
	}

	s.conn = conn
	s.status = meshPingRunning
	s.mu.Unlock()

	s.logger.Info().
		Int("port", s.port).
		Str("mesh_ip", s.meshIP).
		Msg("Mesh ping echo receiver started")

	go s.serve(ctx)

	return nil
}

// Stop stops the UDP echo receiver.
func (s *MeshPingServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		s.status = meshPingStopped
		return err
	}

	return nil
}

func (s *MeshPingServer) serve(ctx context.Context) {
	buffer := make([]byte, 1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline to avoid blocking forever on ctx.Done()
			_ = s.conn.SetReadDeadline(time.Now().Add(time.Second))
			n, remoteAddr, err := s.conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}

				s.mu.Lock()
				status := s.status
				s.mu.Unlock()

				if status == meshPingStopped {
					return
				}

				s.logger.Error().Err(err).Msg("Error reading from UDP")
				time.Sleep(100 * time.Millisecond) // Avoid tight error loop
				continue
			}

			// Simple echo: Send back the same data
			s.logger.Debug().
				Str("remote", remoteAddr.String()).
				Int("bytes", n).
				Msg("Received mesh ping, sending echo")

			_, err = s.conn.WriteToUDP(buffer[:n], remoteAddr)
			if err != nil {
				s.logger.Error().Err(err).Msg("Error sending UDP echo")
			}
		}
	}
}
