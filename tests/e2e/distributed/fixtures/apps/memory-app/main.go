// nolint:errcheck
// #nosec G114
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/coral-mesh/coral/pkg/sdk"
)

// sink prevents the compiler from optimizing away allocations.
var sink []interface{}

// allocateBuffers allocates many []byte slices to generate heap profile samples.
// Each request allocates ~10MB total in 1KB chunks to ensure reliable capture.
func allocateBuffers(w http.ResponseWriter, _ *http.Request) {
	var bufs [][]byte
	for i := 0; i < 10000; i++ {
		buf := make([]byte, 1024)
		buf[0] = byte(i)
		bufs = append(bufs, buf)
	}
	// Hold briefly so profiler can see them on the heap.
	sink = append(sink, bufs)
	if len(sink) > 50 {
		sink = sink[len(sink)-50:]
	}
	fmt.Fprintf(w, "Allocated %d buffers (%d bytes total)\n", len(bufs), len(bufs)*1024)
}

// allocateTypes allocates diverse Go types for type attribution testing.
func allocateTypes(w http.ResponseWriter, _ *http.Request) {
	// Slices.
	slices := make([][]int, 100)
	for i := range slices {
		slices[i] = make([]int, 1000)
	}

	// Maps.
	maps := make([]map[string]interface{}, 100)
	for i := range maps {
		m := make(map[string]interface{}, 10)
		for j := 0; j < 10; j++ {
			m[fmt.Sprintf("key-%d", j)] = j
		}
		maps[i] = m
	}

	// Strings via concatenation.
	var strs []string
	for i := 0; i < 1000; i++ {
		strs = append(strs, strings.Repeat("x", 1024))
	}

	sink = append(sink, slices, maps, strs)
	if len(sink) > 50 {
		sink = sink[len(sink)-50:]
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Fprintf(w, "Allocated diverse types (heap: %d bytes, objects: %d)\n", m.HeapAlloc, m.HeapObjects)
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintf(w, "OK\n")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize Coral SDK with runtime monitoring on port 9004.
	// Port 9002 is used by sdk-app in the same agent-1 namespace.
	err := sdk.EnableRuntimeMonitoring(sdk.Options{
		DebugAddr: ":9004",
	})
	if err != nil {
		logger.Error("Failed to enable runtime monitoring", "error", err)
	} else {
		logger.Info("Coral SDK runtime monitoring enabled", "debug_addr", ":9004")
	}

	http.HandleFunc("/", allocateBuffers)
	http.HandleFunc("/types", allocateTypes)
	http.HandleFunc("/health", healthHandler)

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		logger.Info("Shutting down")
		os.Exit(0)
	}()

	logger.Info("Memory-intensive test app listening on :8080")
	http.ListenAndServe(":8080", nil)
}
