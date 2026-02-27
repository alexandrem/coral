package wireguard

import (
	"testing"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/testutil"
	"github.com/coral-mesh/coral/internal/wireguard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GatherMeshInfo tests functionality. This is a partial unit test covering the GatherMeshInfo
// population logic when given an un-started / mock WireGuard device structure, ensuring basic
// metadata formats and null safety are solid outside the E2E boundary.
func TestGatherMeshInfo_NullSafety(t *testing.T) {
	logger := testutil.NewTestLogger(t)
	info := GatherMeshInfo(nil, "10.42.0.1", "10.42.0.0/16", "test-colony-id", logger)

	require.NotNil(t, info)
	assert.Equal(t, "test-colony-id", info["colony_id"])
	assert.Equal(t, "10.42.0.1", info["mesh_ip"])
	assert.Equal(t, "10.42.0.0/16", info["mesh_subnet"])

	_, hasWg := info["wireguard"]
	assert.False(t, hasWg, "should not have wireguard node if device is nil")
}

func TestGatherMeshInfo_Unstarted(t *testing.T) {
	logger := testutil.NewTestLogger(t)

	// Create keys
	privateKeyBase64 := "yGzL6W6Q6/lDtb59+dG/F/B3BmVN/D9wUqX/77L1oGE=" // dummy key
	pubKeyBase64 := "wGtt3f4A633lH6/gC/g8e1/N2r7M77u5gS1bI9n9AWE="

	cfg := &config.WireGuardConfig{
		PrivateKey:      privateKeyBase64,
		PublicKey:       pubKeyBase64,
		Port:            51820,
		MeshIPv4:        "10.42.0.1",
		MeshNetworkIPv4: "10.42.0.0/16",
	}

	dev, err := wireguard.NewDevice(cfg, logger)
	require.NoError(t, err)

	info := GatherMeshInfo(dev, "10.42.0.1", "10.42.0.0/16", "test-colony-id", logger)

	require.NotNil(t, info)

	wgMap, ok := info["wireguard"].(map[string]interface{})
	require.True(t, ok)

	// Not started, interface doesn't exist yet
	assert.Equal(t, false, wgMap["interface_exists"])
	assert.Equal(t, 51820, wgMap["listen_port"])
}
