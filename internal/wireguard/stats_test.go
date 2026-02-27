package wireguard

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUAPI(t *testing.T) {
	pubKeyBase64 := "2f0KjFz1q1W/9O+L6A4y4S6P7Q8u9L+M0N1o2p3Q4S8="
	pubKeyBytes, err := base64.StdEncoding.DecodeString(pubKeyBase64)
	require.NoError(t, err)
	pubKeyHex := hex.EncodeToString(pubKeyBytes)

	uapiOutput := `private_key=3132333435363738393031323334353637383930313233343536373839303132
listen_port=51820
public_key=` + pubKeyHex + `
endpoint=192.168.1.100:41580
allowed_ip=10.42.0.15/32
last_handshake_time_sec=1614556800
last_handshake_time_nsec=123456789
rx_bytes=1024
tx_bytes=2048
persistent_keepalive_interval=25
`
	stats, err := ParseUAPI(uapiOutput)
	require.NoError(t, err)
	require.NotNil(t, stats)

	assert.Equal(t, "3132333435363738393031323334353637383930313233343536373839303132", stats.PrivateKey)
	assert.Equal(t, 51820, stats.ListenPort)

	// Since we construct the public key as base64 in the parser
	peer, exists := stats.Peers[pubKeyBase64]
	require.True(t, exists, "Peer with the original base64 public key should exist")

	assert.Equal(t, pubKeyBase64, peer.PublicKey)
	assert.Equal(t, "192.168.1.100:41580", peer.Endpoint)
	assert.ElementsMatch(t, []string{"10.42.0.15/32"}, peer.AllowedIPs)
	assert.Equal(t, int64(1614556800), peer.LastHandshakeTime.Unix())
	assert.Equal(t, int64(1024), peer.RxBytes)
	assert.Equal(t, int64(2048), peer.TxBytes)
	assert.Equal(t, 25, peer.PersistentKeepalive)
}
