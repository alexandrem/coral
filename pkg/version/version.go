// Package version provides build version information.
package version

import (
	"runtime"
)

var (
	// Version is the semantic version (set by build flags)
	Version = "dev"

	// GitCommit is the git commit hash (set by build flags)
	GitCommit = "unknown"

	// BuildDate is the build timestamp (set by build flags)
	BuildDate = "unknown"

	// GoVersion is the Go version used to build
	GoVersion = runtime.Version()
)
