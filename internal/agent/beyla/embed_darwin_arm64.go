//go:build darwin && arm64

package beyla

import (
	_ "embed"
)

// beylaEmbeddedBinary contains the embedded Beyla binary for Darwin arm64 (Apple Silicon).
// This file is only compiled when building for Darwin arm64.
//
//go:embed binaries/beyla-darwin-arm64
var beylaEmbeddedBinary []byte
