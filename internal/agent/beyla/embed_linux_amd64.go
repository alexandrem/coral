//go:build linux && amd64

package beyla

import (
	_ "embed"
)

// beylaEmbeddedBinary contains the embedded Beyla binary for Linux amd64.
// This file is only compiled when building for Linux amd64.
//
//go:embed binaries/beyla-linux-amd64
var beylaEmbeddedBinary []byte
