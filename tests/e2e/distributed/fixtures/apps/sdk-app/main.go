package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/coral-mesh/coral/pkg/sdk"
)

// Business logic functions for uprobe tracing tests.

// ProcessPayment processes a payment transaction.
func ProcessPayment(amount float64, currency string) error {
	// Simulate some work.
	time.Sleep(50 * time.Millisecond)

	if amount <= 0 {
		return fmt.Errorf("invalid amount: %.2f", amount)
	}

	return nil
}

// ValidateCard validates a credit card number.
func ValidateCard(cardNumber string) (bool, error) {
	// Simulate validation work.
	time.Sleep(20 * time.Millisecond)

	if len(cardNumber) < 13 {
		return false, fmt.Errorf("invalid card number length")
	}

	return true, nil
}

// CalculateTotal calculates the total with tax.
func CalculateTotal(subtotal float64, taxRate float64) float64 {
	time.Sleep(10 * time.Millisecond)
	return subtotal * (1 + taxRate)
}

func main() {
	// Setup logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize Coral SDK with runtime monitoring.
	err := sdk.EnableRuntimeMonitoring(sdk.Options{
		DebugAddr: ":9002",
	})
	if err != nil {
		logger.Error("Failed to enable runtime monitoring", "error", err)
	} else {
		logger.Info("Coral SDK initialized (Runtime Monitoring Enabled)")
	}

	logger.Info("SDK test app started")

	// HTTP server for health checks and triggering workload.
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/trigger", handleTrigger)

	go func() {
		logger.Info("Starting HTTP server", "port", 3001)
		if err := http.ListenAndServe(":3001", nil); err != nil {
			logger.Error("HTTP server failed", "error", err)
		}
	}()

	// Optionally run continuous workload.
	if os.Getenv("AUTO_WORKLOAD") == "true" {
		go runWorkload(logger)
	}

	// Wait for interrupt signal.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logger.Info("Received shutdown signal")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

func handleTrigger(w http.ResponseWriter, r *http.Request) {
	// Trigger payment operations on demand (useful for tests).
	logger := slog.Default()

	err := ProcessPayment(99.99, "USD")
	if err != nil {
		logger.Error("Payment failed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
		return
	}

	valid, err := ValidateCard("4532123456789012")
	if err != nil {
		logger.Error("Card validation failed", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
		return
	}

	total := CalculateTotal(100.00, 0.08)

	logger.Info("Operations completed", "valid", valid, "total", total)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"status":"success","total":%.2f}`, total)))
}

func runWorkload(logger *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := ProcessPayment(99.99, "USD"); err != nil {
			logger.Error("Payment failed", "error", err)
		}

		if valid, err := ValidateCard("4532123456789012"); err != nil {
			logger.Error("Card validation failed", "error", err)
		} else {
			logger.Info("Card validated", "valid", valid)
		}

		total := CalculateTotal(100.00, 0.08)
		logger.Info("Calculated total", "total", total)
	}
}
