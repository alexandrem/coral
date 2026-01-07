package fixtures

import "os"

func init() {
	// Enable Docker BuildKit for all container builds.
	// This allows us to use cache mounts in Dockerfiles, dramatically speeding up
	// Go module downloads and builds in E2E tests.
	//
	// BuildKit cache mounts persist across builds, so:
	// - First build: Downloads all dependencies (~slow)
	// - Subsequent builds: Uses cached dependencies (~fast)
	//
	// With Colima (4 CPU, 8GB RAM), this reduces build times from ~5-10 minutes
	// to ~30-60 seconds after the first build.
	os.Setenv("DOCKER_BUILDKIT", "1")
}
