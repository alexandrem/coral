package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// TargetFunction is the function we want to trace.
// This is intentionally simple to make it easy to find in DWARF.
func TargetFunction(message string) string {
	time.Sleep(10 * time.Millisecond)
	return fmt.Sprintf("processed: %s", message)
}

// AnotherFunction is another traceable function.
func AnotherFunction(x, y int) int {
	return x + y
}

func main() {
	log.Println("Starting test application (NO SDK)")

	// Simple HTTP server for health checks.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		result := TargetFunction("hello from http")
		sum := AnotherFunction(5, 10)
		fmt.Fprintf(w, "%s, sum=%d", result, sum)
	})

	// Start server in background.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		addr := ":" + port
		log.Printf("Starting HTTP server on %s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Keep calling the target function to generate activity.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			result := TargetFunction("periodic call")
			_ = result
		}
	}()

	// Write PID to file for test coordination.
	if pidFile := os.Getenv("PID_FILE"); pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
			log.Printf("Failed to write PID file: %v", err)
		} else {
			log.Printf("Wrote PID %d to %s", os.Getpid(), pidFile)
		}
	}

	// Signal ready.
	log.Println("Application ready")

	// Wait for interrupt signal.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
