//go:build linux && arm64

package beyla

import (
	_ "embed"
)

// beylaEmbeddedBinary contains the embedded Beyla binary for Linux arm64.
// This file is only compiled when building for Linux arm64.
//
//go:embed binaries/beyla-linux-arm64
var beylaEmbeddedBinary []byte
