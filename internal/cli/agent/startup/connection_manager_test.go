package startup

import (
	"context"
	"errors"
	"testing"
	"time"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/discovery"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/rs/zerolog"
)

func TestApplyBootstrapRegistration(t *testing.T) {
	cm := &ConnectionManager{
		state:  StateUnregistered,
		logger: zerolog.Nop(),
	}

	err := cm.ApplyBootstrapRegistration(&meshv1.RegisterResponse{
		Accepted:   true,
		AssignedIp: "100.64.0.42",
		MeshSubnet: "100.64.0.0/10",
	})
	if err != nil {
		t.Fatalf("ApplyBootstrapRegistration() error = %v", err)
	}
	if cm.GetState() != StateRegistered {
		t.Fatalf("state = %s, want registered", cm.GetState())
	}
	ip, subnet := cm.GetAssignedIP()
	if ip != "100.64.0.42" || subnet != "100.64.0.0/10" {
		t.Fatalf("assignment = %s/%s, want 100.64.0.42/100.64.0.0/10", ip, subnet)
	}
	if endpoint := cm.GetCurrentEndpoint(); endpoint != "" {
		t.Fatalf("endpoint = %q, want empty for WireGuard roaming", endpoint)
	}
}

func TestWaitForDiscoveryRetriesUntilColonyAppears(t *testing.T) {
	cm := &ConnectionManager{
		config: &config.ResolvedConfig{ColonyID: "test-colony"},
		state:  StateWaitingDiscovery,
		logger: zerolog.Nop(),
		discoveryBackoff: &ExponentialBackoff{
			InitialInterval: time.Millisecond,
			MaxInterval:     time.Millisecond,
			Multiplier:      1,
		},
	}

	attempts := 0
	cm.discoveryLookup = func(*config.ResolvedConfig, logging.Logger) (*discovery.LookupColonyResponse, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("colony not found")
		}
		return &discovery.LookupColonyResponse{Pubkey: "colony-key"}, nil
	}

	info, err := cm.WaitForDiscovery(context.Background())
	if err != nil {
		t.Fatalf("WaitForDiscovery() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if info.Pubkey != "colony-key" {
		t.Fatalf("pubkey = %q, want colony-key", info.Pubkey)
	}
	if cm.GetState() != StateUnregistered {
		t.Fatalf("state = %s, want unregistered", cm.GetState())
	}
}

func TestWaitForDiscoveryStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := &ConnectionManager{
		config: &config.ResolvedConfig{ColonyID: "test-colony"},
		state:  StateWaitingDiscovery,
		logger: zerolog.Nop(),
		discoveryBackoff: &ExponentialBackoff{
			InitialInterval: time.Hour,
			MaxInterval:     time.Hour,
			Multiplier:      1,
		},
		discoveryLookup: func(*config.ResolvedConfig, logging.Logger) (*discovery.LookupColonyResponse, error) {
			cancel()
			return nil, errors.New("colony not found")
		},
	}

	_, err := cm.WaitForDiscovery(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForDiscovery() error = %v, want context.Canceled", err)
	}
}

func TestApplyBootstrapRegistrationRejectsInvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		resp *meshv1.RegisterResponse
	}{
		{name: "nil", resp: nil},
		{name: "rejected", resp: &meshv1.RegisterResponse{Reason: "denied"}},
		{name: "invalid IP", resp: &meshv1.RegisterResponse{Accepted: true, AssignedIp: "bad", MeshSubnet: "100.64.0.0/10"}},
		{name: "invalid subnet", resp: &meshv1.RegisterResponse{Accepted: true, AssignedIp: "100.64.0.42", MeshSubnet: "bad"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &ConnectionManager{state: StateUnregistered, logger: zerolog.Nop()}
			if err := cm.ApplyBootstrapRegistration(tt.resp); err == nil {
				t.Fatal("ApplyBootstrapRegistration() error = nil, want error")
			}
			if cm.GetState() != StateUnregistered {
				t.Fatalf("state = %s, want unregistered", cm.GetState())
			}
		})
	}
}
