//go:build darwin && amd64

package run

import (
	_ "embed"
)

// denoEmbeddedBinary contains the embedded Deno binary for Darwin (macOS) amd64.
// This file is only compiled when building for Darwin amd64.
//
//go:embed binaries/deno-darwin-amd64
var denoEmbeddedBinary []byte
