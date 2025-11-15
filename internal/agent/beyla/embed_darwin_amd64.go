//go:build darwin && amd64

package beyla

// beylaEmbeddedBinary is empty for Darwin (macOS) because Beyla is Linux-only.
// Beyla requires eBPF, which is only available on Linux.
// This file is only compiled when building for Darwin amd64.
var beylaEmbeddedBinary []byte
