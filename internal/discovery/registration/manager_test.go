package registration

import (
	"context"
	"testing"
	"time"

	"github.com/coral-mesh/coral/internal/logging"
)

func TestNewManager(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           true,
		AutoRegister:      true,
		RegisterInterval:  1 * time.Minute,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://localhost:8080",
		DiscoveryTimeout:  5 * time.Second,
	}

	manager := NewManager(cfg, logger)

	if manager == nil {
		t.Fatal("expected non-nil manager")
	}

	if manager.config.MeshID != "test-colony" {
		t.Errorf("expected MeshID 'test-colony', got %s", manager.config.MeshID)
	}
}

func TestManager_DisabledRegistration(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           false, // Disabled
		AutoRegister:      true,
		RegisterInterval:  1 * time.Second,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://localhost:8080",
		DiscoveryTimeout:  5 * time.Second,
	}

	manager := NewManager(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start should succeed even when disabled.
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("start should not fail when disabled: %v", err)
	}
	defer func() { _ = manager.Stop() }()

	// Should not be registered since it's disabled.
	if manager.IsRegistered() {
		t.Error("expected not to be registered when disabled")
	}
}

func TestManager_AutoRegisterDisabled(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           true,
		AutoRegister:      false, // Auto-register disabled
		RegisterInterval:  1 * time.Second,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://localhost:8080",
		DiscoveryTimeout:  5 * time.Second,
	}

	manager := NewManager(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start should succeed even when auto-register is disabled.
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("start should not fail when auto-register disabled: %v", err)
	}
	defer func() { _ = manager.Stop() }()

	// Should not be registered since auto-register is disabled.
	if manager.IsRegistered() {
		t.Error("expected not to be registered when auto-register disabled")
	}
}

func TestManager_StartWithInvalidEndpoint(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           true,
		AutoRegister:      true,
		RegisterInterval:  1 * time.Second,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://invalid-host-that-does-not-exist:9999",
		DiscoveryTimeout:  1 * time.Second,
	}

	manager := NewManager(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start should not fail even when discovery is unavailable (it retries in background).
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("start should not fail when discovery unavailable: %v", err)
	}
	defer func() { _ = manager.Stop() }()

	// Wait a bit for registration attempts.
	time.Sleep(1 * time.Second)

	// Should not be registered since discovery is unavailable.
	if manager.IsRegistered() {
		t.Error("expected not to be registered when discovery unavailable")
	}

	// Check status.
	registered, _, lastErr := manager.Status()
	if registered {
		t.Error("expected status to show not registered")
	}
	if lastErr == nil {
		t.Error("expected status to have an error")
	}
}

func TestManager_GracefulShutdown(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           true,
		AutoRegister:      true,
		RegisterInterval:  100 * time.Millisecond,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://invalid-host:9999",
		DiscoveryTimeout:  1 * time.Second,
	}

	manager := NewManager(cfg, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}

	// Stop the manager.
	stopStart := time.Now()
	err = manager.Stop()
	stopDuration := time.Since(stopStart)

	if err != nil {
		t.Errorf("stop should not error: %v", err)
	}

	if stopDuration > 6*time.Second {
		t.Errorf("stop took too long: %v", stopDuration)
	}

	// Verify done channel is closed.
	select {
	case <-manager.Done():
		// Good, done channel closed.
	case <-time.After(100 * time.Millisecond):
		t.Error("done channel was not closed after stop")
	}
}

func TestManager_IsRegistered(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           false,
		AutoRegister:      false,
		RegisterInterval:  1 * time.Minute,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://localhost:8080",
		DiscoveryTimeout:  5 * time.Second,
	}

	manager := NewManager(cfg, logger)

	// Should not be registered initially.
	if manager.IsRegistered() {
		t.Error("expected not to be registered initially")
	}

	// Manually set registration state to test IsRegistered logic.
	manager.mu.Lock()
	manager.registered = true
	manager.expiresAt = time.Now().Add(5 * time.Minute)
	manager.mu.Unlock()

	if !manager.IsRegistered() {
		t.Error("expected to be registered after setting state")
	}

	// Set expiration to the past.
	manager.mu.Lock()
	manager.expiresAt = time.Now().Add(-1 * time.Minute)
	manager.mu.Unlock()

	if manager.IsRegistered() {
		t.Error("expected not to be registered when expired")
	}
}

func TestManager_Status(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "test")

	cfg := Config{
		Enabled:           false,
		AutoRegister:      false,
		RegisterInterval:  1 * time.Minute,
		MeshID:            "test-colony",
		PublicKey:         "test-pubkey",
		Endpoints:         []string{":51820"},
		Metadata:          map[string]string{"env": "test"},
		DiscoveryEndpoint: "http://localhost:8080",
		DiscoveryTimeout:  5 * time.Second,
	}

	manager := NewManager(cfg, logger)

	// Check initial status.
	registered, expiresAt, lastErr := manager.Status()
	if registered {
		t.Error("expected not to be registered initially")
	}
	if !expiresAt.IsZero() {
		t.Error("expected expiresAt to be zero initially")
	}
	if lastErr != nil {
		t.Errorf("expected no error initially, got: %v", lastErr)
	}

	// Manually set state to test Status.
	testTime := time.Now().Add(10 * time.Minute)
	manager.mu.Lock()
	manager.registered = true
	manager.expiresAt = testTime
	manager.mu.Unlock()

	registered, expiresAt, lastErr = manager.Status()
	if !registered {
		t.Error("expected to be registered after setting state")
	}
	if !expiresAt.Equal(testTime) {
		t.Errorf("expected expiresAt %v, got %v", testTime, expiresAt)
	}
	if lastErr != nil {
		t.Errorf("expected no error, got: %v", lastErr)
	}
}
