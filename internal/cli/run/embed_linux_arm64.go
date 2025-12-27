//go:build linux && arm64

package run

import (
	_ "embed"
)

// denoEmbeddedBinary contains the embedded Deno binary for Linux arm64.
// This file is only compiled when building for Linux arm64.
//
//go:embed binaries/deno-linux-arm64
var denoEmbeddedBinary []byte
