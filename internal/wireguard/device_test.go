package wireguard

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/tuntest"

	"github.com/coral-mesh/coral/internal/auth"
)

// newTestDevice returns a started Device backed by an in-memory TUN, so peer
// management runs against a real wireguard-go device without root. It has
// no OS interface, so route management is skipped.
func newTestDevice(t *testing.T) *Device {
	t.Helper()

	keys, err := auth.GenerateWireGuardKeyPair()
	require.NoError(t, err)
	privateKey, err := base64.StdEncoding.DecodeString(keys.PrivateKey)
	require.NoError(t, err)

	wgDevice := device.NewDevice(
		tuntest.NewChannelTUN().TUN(),
		conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelSilent, ""),
	)
	t.Cleanup(wgDevice.Close)
	require.NoError(t, wgDevice.IpcSet("private_key="+hex.EncodeToString(privateKey)+"\n"))

	return &Device{
		wgDevice: wgDevice,
		peers:    make(map[string]*PeerConfig),
		logger:   zerolog.Nop(),
	}
}

// newTestPublicKey returns a fresh base64-encoded WireGuard public key.
func newTestPublicKey(t *testing.T) string {
	t.Helper()

	keys, err := auth.GenerateWireGuardKeyPair()
	require.NoError(t, err)
	return keys.PublicKey
}

// devicePeers returns the AllowedIPs of every peer configured in the
// wireguard-go device, keyed by base64-encoded public key.
func devicePeers(t *testing.T, d *Device) map[string][]string {
	t.Helper()

	uapi, err := d.wgDevice.IpcGet()
	require.NoError(t, err)

	peers := make(map[string][]string)
	var current string
	for _, line := range strings.Split(uapi, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "public_key":
			keyBytes, err := hex.DecodeString(value)
			require.NoError(t, err)
			current = base64.StdEncoding.EncodeToString(keyBytes)
			peers[current] = []string{}
		case "allowed_ip":
			peers[current] = append(peers[current], value)
		}
	}
	return peers
}

func TestDeviceRemovePeer(t *testing.T) {
	d := newTestDevice(t)
	removed := newTestPublicKey(t)
	kept := newTestPublicKey(t)

	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: removed, AllowedIPs: []string{"100.64.0.2/32"}}))
	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: kept, AllowedIPs: []string{"100.64.0.3/32"}}))

	require.NoError(t, d.RemovePeer(removed))

	require.Equal(t, map[string][]string{kept: {"100.64.0.3/32"}}, devicePeers(t, d))
	_, ok := d.GetPeer(removed)
	require.False(t, ok)

	// Removing an absent peer is a no-op.
	require.NoError(t, d.RemovePeer(removed))
}

func TestDeviceAddPeerRejectsNonBase64Key(t *testing.T) {
	d := newTestDevice(t)

	err := d.AddPeer(&PeerConfig{
		PublicKey:  strings.Repeat("!", 44),
		AllowedIPs: []string{"100.64.0.2/32"},
	})

	require.Error(t, err)
	require.Empty(t, d.ListPeers())
	require.Empty(t, devicePeers(t, d))
}

func TestDeviceAllowedIPInUse(t *testing.T) {
	d := newTestDevice(t)
	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: newTestPublicKey(t), AllowedIPs: []string{"100.64.0.2/32"}}))

	require.True(t, d.allowedIPInUse("100.64.0.2/32"))
	require.False(t, d.allowedIPInUse("100.64.0.3/32"))
}

// deviceKeepalive returns the persistent keepalive interval configured in
// the wireguard-go device for the peer with the given public key.
func deviceKeepalive(t *testing.T, d *Device, publicKey string) string {
	t.Helper()

	keyBytes, err := base64.StdEncoding.DecodeString(publicKey)
	require.NoError(t, err)
	uapi, err := d.wgDevice.IpcGet()
	require.NoError(t, err)

	var inPeer bool
	for _, line := range strings.Split(uapi, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "public_key":
			inPeer = value == hex.EncodeToString(keyBytes)
		case "persistent_keepalive_interval":
			if inPeer {
				return value
			}
		}
	}
	t.Fatalf("peer %s not found in device", publicKey)
	return ""
}

func TestDeviceAddPeerReplacesAllowedIPs(t *testing.T) {
	d := newTestDevice(t)
	publicKey := newTestPublicKey(t)

	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: publicKey, AllowedIPs: []string{"100.64.0.2/32"}}))
	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: publicKey, AllowedIPs: []string{"100.64.0.3/32"}}))

	require.Equal(t, map[string][]string{publicKey: {"100.64.0.3/32"}}, devicePeers(t, d))
	peer, ok := d.GetPeer(publicKey)
	require.True(t, ok)
	require.Equal(t, []string{"100.64.0.3/32"}, peer.AllowedIPs)
}

func TestDeviceAddPeerDisablesKeepalive(t *testing.T) {
	d := newTestDevice(t)
	publicKey := newTestPublicKey(t)

	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: publicKey, AllowedIPs: []string{"100.64.0.2/32"}, PersistentKeepalive: 25}))
	require.Equal(t, "25", deviceKeepalive(t, d, publicKey))

	require.NoError(t, d.AddPeer(&PeerConfig{PublicKey: publicKey, AllowedIPs: []string{"100.64.0.2/32"}}))
	require.Equal(t, "0", deviceKeepalive(t, d, publicKey))
}
