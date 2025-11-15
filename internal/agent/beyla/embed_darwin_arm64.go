//go:build darwin && arm64

package beyla

// beylaEmbeddedBinary is empty for Darwin (macOS) because Beyla is Linux-only.
// Beyla requires eBPF, which is only available on Linux.
// This file is only compiled when building for Darwin arm64.
var beylaEmbeddedBinary []byte
