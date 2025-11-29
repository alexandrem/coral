package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

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
	// Setup logger.
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

	// Initialize Coral SDK with debug enabled.
	coralSDK, err := sdk.New(sdk.Config{
		ServiceName: "payment-service",
		EnableDebug: true,
		Logger:      logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize Coral SDK")
	}
	defer coralSDK.Close()

	logger.Info().
		Str("debug_addr", coralSDK.DebugAddr()).
		Msg("Application started with Coral SDK")

	// Simulate application workload.
	go runWorkload(logger)

	// Wait for interrupt signal.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	logger.Info().Msg("Received shutdown signal")
}

func runWorkload(logger zerolog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Simulate various payment operations.
		if err := ProcessPayment(99.99, "USD"); err != nil {
			logger.Error().Err(err).Msg("Payment failed")
		}

		if valid, err := ValidateCard("4532123456789012"); err != nil {
			logger.Error().Err(err).Msg("Card validation failed")
		} else {
			logger.Info().Bool("valid", valid).Msg("Card validated")
		}

		total := CalculateTotal(100.00, 0.08)
		logger.Info().Float64("total", total).Msg("Calculated total")
	}
}
