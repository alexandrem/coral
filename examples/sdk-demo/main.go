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

// Example business logic functions that can be traced.

// ProcessPayment processes a payment transaction.
func ProcessPayment(amount float64, currency string) error {
	// Simulate some work.
	time.Sleep(50 * time.Millisecond)

	if amount <= 0 {
		return fmt.Errorf("invalid amount: %.2f", amount)
	}

	fmt.Printf("Processing payment: %.2f %s\n", amount, currency)
	return nil
}

// ValidateCard validates a credit card number.
func ValidateCard(cardNumber string) (bool, error) {
	// Simulate validation work.
	time.Sleep(20 * time.Millisecond)

	if len(cardNumber) < 13 {
		return false, fmt.Errorf("invalid card number length")
	}

	fmt.Printf("Validated card: %s\n", maskCard(cardNumber))
	return true, nil
}

// CalculateTotal calculates the total with tax.
func CalculateTotal(subtotal float64, taxRate float64) float64 {
	time.Sleep(10 * time.Millisecond)
	return subtotal * (1 + taxRate)
}

func maskCard(card string) string {
	if len(card) < 4 {
		return "****"
	}
	return "****" + card[len(card)-4:]
}

func main() {
	// Setup logger with JSON handler to match previous behavior
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize Coral SDK with runtime monitoring enabled.
	agentAddr := os.Getenv("CORAL_AGENT_ADDR")
	if agentAddr == "" {
		agentAddr = "127.0.0.1:9001"
	}
	logger.Info("Configured Agent Address", "addr", agentAddr)

	err := sdk.RegisterService("payment-service", sdk.Options{
		Port:          3001,
		AgentAddr:     agentAddr,
		SdkListenAddr: ":9092",
	})
	if err != nil {
		logger.Error("Failed to register service", "error", err)
		os.Exit(1)
	}

	if err := sdk.EnableRuntimeMonitoring(); err != nil {
		logger.Error("Failed to enable runtime monitoring", "error", err)
		os.Exit(1)
	}

	logger.Info("Application started with Coral SDK (Runtime Monitoring Enabled)")

	// Start HTTP server for agent health checks
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	go func() {
		logger.Info("Starting HTTP server", "port", 3001)
		if err := http.ListenAndServe(":3001", nil); err != nil {
			logger.Error("HTTP server failed", "error", err)
		}
	}()

	// Simulate application workload.
	go runWorkload(logger)

	// Wait for interrupt signal.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logger.Info("Received shutdown signal")
}

func runWorkload(logger *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Simulate various payment operations.
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
