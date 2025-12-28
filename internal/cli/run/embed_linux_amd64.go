//go:build linux && amd64

package run

import (
	_ "embed"
)

// denoEmbeddedBinary contains the embedded Deno binary for Linux amd64.
// This file is only compiled when building for Linux amd64.
//
//go:embed binaries/deno-linux-amd64
var denoEmbeddedBinary []byte
