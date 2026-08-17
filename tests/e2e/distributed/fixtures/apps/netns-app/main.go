// nolint:errcheck
// #nosec G114
// netns-app is an RFD 112 test fixture: a plain HTTP server that shares
// agent-0's PID namespace (docker-compose `pid: "service:agent-0"`) but
// keeps its own, separate network namespace (no `network_mode` override).
// This mirrors a bare-metal host: a privileged Coral agent sees the
// process via the shared host PID namespace, but the listening socket
// lives in a network namespace the agent does not natively share, so
// ProcFSProvider must scan namespaces to find it (RFD 112).
package main

import (
	"fmt"
	"net/http"
)

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK\n")
}

func main() {
	http.HandleFunc("/", handler)
	http.HandleFunc("/health", handler)

	fmt.Println("netns-app listening on :8080 (own network namespace)")
	http.ListenAndServe(":8080", nil)
}
