package netobs

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
)

// streamer sends batches of aggregated connections to the colony
// via the ReportConnections client-streaming RPC.
type streamer struct {
	agentID    string
	colonyURL  string
	httpClient *http.Client
	logger     zerolog.Logger
}

// newStreamer creates a streamer targeting the given colony base URL.
func newStreamer(agentID, colonyURL string, httpClient *http.Client, logger zerolog.Logger) *streamer {
	return &streamer{
		agentID:    agentID,
		colonyURL:  colonyURL,
		httpClient: httpClient,
		logger:     logger,
	}
}

// Send opens a ReportConnections stream, sends one batch, and closes the stream.
// A new stream is opened per batch; this avoids managing long-lived stream state
// across network interruptions.
func (s *streamer) Send(ctx context.Context, entries []ConnectionEntry) error {
	start := time.Now()
	if len(entries) == 0 {
		return nil
	}

	if s.colonyURL == "" {
		return fmt.Errorf("colony URL not yet available")
	}

	client := colonyv1connect.NewColonyServiceClient(s.httpClient, s.colonyURL)

	stream := client.ReportConnections(ctx)

	protos := make([]*colonyv1.L4ConnectionEntry, 0, len(entries))
	for _, e := range entries {
		protos = append(protos, &colonyv1.L4ConnectionEntry{
			RemoteIp:      e.RemoteIP,
			RemotePort:    e.RemotePort,
			Protocol:      e.Protocol,
			BytesSent:     e.BytesSent,
			BytesReceived: e.BytesReceived,
			Retransmits:   e.Retransmits,
			RttUs:         e.RTTUS,
			LastObserved:  timestamppb.New(e.LastObserved),
		})
	}

	if err := stream.Send(&colonyv1.ReportConnectionsRequest{
		AgentId:     s.agentID,
		Connections: protos,
	}); err != nil {
		_, _ = stream.CloseAndReceive()
		return fmt.Errorf("send failed: %w", err)
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		return fmt.Errorf("CloseAndReceive failed: %w", err)
	}

	s.logger.Debug().
		Int("entries", len(entries)).
		Dur("rpc", time.Since(start)).
		Msg("Reported L4 connections to colony")

	return nil
}

// newColonyHTTPClient returns a plain HTTP client suitable for use over the
// WireGuard mesh (encryption is provided by WireGuard, not TLS).
func newColonyHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    10,
			IdleConnTimeout: 90 * time.Second,
		},
	}
}
