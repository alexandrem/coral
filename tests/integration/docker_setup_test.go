package integration

import (
	"os"
	"path/filepath"
	"runtime"
)

func init() {
	// If DOCKER_HOST is not set, try to find a valid Docker socket,
	// prioritizing Colima as requested by the user.
	if os.Getenv("DOCKER_HOST") == "" && runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()

		// Search paths in order of preference.
		searchPaths := []string{
			// 1. Colima socket (preferred by user)
			filepath.Join(home, ".colima", "default", "docker.sock"),
			// 2. OrbStack socket (another popular alternative)
			filepath.Join(home, ".orbstack", "run", "docker.sock"),
			// 3. Official Docker Desktop socket (modern location)
			filepath.Join(home, ".docker", "run", "docker.sock"),
			// 4. Default system socket (symlink location)
			"/var/run/docker.sock",
		}

		for _, path := range searchPaths {
			if _, err := os.Stat(path); err == nil {
				os.Setenv("DOCKER_HOST", "unix://"+path)
				return
			}
		}
	}
}
