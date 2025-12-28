//go:build darwin && arm64

package run

import (
	_ "embed"
)

// denoEmbeddedBinary contains the embedded Deno binary for Darwin (macOS) arm64.
// This file is only compiled when building for Darwin arm64.
//
//go:embed binaries/deno-darwin-arm64
var denoEmbeddedBinary []byte
