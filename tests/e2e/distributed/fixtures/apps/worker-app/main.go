// nolint:errcheck
// #nosec G114
// worker-app is a client-only test fixture for RFD 102 (Pluggable Service
// Discovery Providers): it never binds a listening socket, only makes
// outbound HTTP calls, so it must be discovered via EnvVarProvider
// (OTEL_SERVICE_NAME) + ProcFSProvider's client-only process walk, and
// instrumented via a Beyla exe_path rule rather than an open_ports rule.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	target := os.Getenv("WORKER_TARGET_URL")
	if target == "" {
		target = "http://localhost:8090/health"
	}

	fmt.Printf("worker-app started, calling %s every second (no listening socket)\n", target)

	client := &http.Client{Timeout: 3 * time.Second}
	for {
		resp, err := client.Get(target)
		if err != nil {
			fmt.Printf("worker-app call failed: %v\n", err)
		} else {
			fmt.Printf("worker-app call to %s: %s\n", target, resp.Status)
			resp.Body.Close()
		}
		time.Sleep(1 * time.Second)
	}
}
