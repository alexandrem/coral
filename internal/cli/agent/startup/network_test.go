package startup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/coral-mesh/coral/internal/agent/enrollmentstate"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestParseAgentWireGuardPort(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "fixed", value: "51820", want: 51820},
		{name: "lowest fixed", value: "1", want: 1},
		{name: "highest fixed", value: "65535", want: 65535},
		{name: "zero selects ephemeral", value: "0", want: -1},
		{name: "negative one selects ephemeral", value: "-1", want: -1},
		{name: "not a number", value: "abc", wantErr: true},
		{name: "negative out of range", value: "-2", wantErr: true},
		{name: "positive out of range", value: "65536", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAgentWireGuardPort(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseAgentWireGuardPort(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("parseAgentWireGuardPort(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestResolveAgentWireGuardPortDefaultsToDiscoverablePort(t *testing.T) {
	got, err := resolveAgentWireGuardPort("")
	if err != nil {
		t.Fatalf("resolveAgentWireGuardPort(\"\") error = %v", err)
	}
	if got != 51820 {
		t.Fatalf("resolveAgentWireGuardPort(\"\") = %d, want 51820", got)
	}
}

func TestNetworkIdentityRecovery(t *testing.T) {
	for _, failure := range []string{"agent mismatch", "colony mismatch", "invalid key"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.AgentConfig{}
			cfg.Agent.Bootstrap.CertsDir = dir
			resolved := &config.ResolvedConfig{ColonyID: "colony-1"}
			network := NewNetworkInitializer(zerolog.Nop(), resolved, cfg, nil, "agent-1")
			original, err := network.loadOrGenerateAgentKeys()
			require.NoError(t, err)
			store := enrollmentstate.NewStore(dir, zerolog.Nop())
			switch failure {
			case "agent mismatch":
				network.agentID = "agent-2"
			case "colony mismatch":
				resolved.ColonyID = "colony-2"
			case "invalid key":
				cp, err := store.Load()
				require.NoError(t, err)
				cp.WireGuardPrivateKey = "corrupt"
				data, err := json.Marshal(cp)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, enrollmentstate.CheckpointFileName), data, 0600))
			}
			recovered, err := network.loadOrGenerateAgentKeys()
			require.NoError(t, err)
			require.NotEqual(t, original.PublicKey, recovered.PublicKey)
			pending, err := store.Load()
			require.NoError(t, err)
			require.Equal(t, network.agentID, pending.AgentID)
			require.Equal(t, resolved.ColonyID, pending.ColonyID)
			require.Equal(t, recovered.PublicKey, pending.WireGuardPublicKey)
			restarted, err := network.loadOrGenerateAgentKeys()
			require.NoError(t, err)
			require.Equal(t, recovered, restarted)
		})
	}
}
