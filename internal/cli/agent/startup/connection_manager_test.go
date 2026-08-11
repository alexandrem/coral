package startup

import (
	"testing"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
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
