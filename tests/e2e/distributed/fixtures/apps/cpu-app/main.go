// nolint:errcheck
// #nosec G404
// #nosec G114
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
)

func cpuIntensiveWork(iterations int) string {
	data := make([]byte, 1024)
	rand.Read(data)

	hash := sha256.Sum256(data)
	for i := 0; i < iterations; i++ {
		hash = sha256.Sum256(hash[:])
	}

	return hex.EncodeToString(hash[:])
}

func handler(w http.ResponseWriter, r *http.Request) {
	// Do CPU-intensive work
	// Increased from 10k to 100k iterations to ensure meaningful CPU profiling samples
	result := cpuIntensiveWork(100000)
	fmt.Fprintf(w, "Hash: %s\n", result)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK\n")
}

func main() {
	http.HandleFunc("/", handler)
	http.HandleFunc("/health", healthHandler)

	fmt.Println("CPU-intensive test app listening on :8080")
	http.ListenAndServe(":8080", nil)
}
