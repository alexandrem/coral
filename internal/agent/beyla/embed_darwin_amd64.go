//go:build darwin && amd64

package beyla

import (
	_ "embed"
)

// beylaEmbeddedBinary contains the embedded Beyla binary for Darwin amd64.
// This file is only compiled when building for Darwin amd64.
//
//go:embed binaries/beyla-darwin-amd64
var beylaEmbeddedBinary []byte
