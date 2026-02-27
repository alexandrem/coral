package duckdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldUseColonyProxy(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://colony.example.com:8443", true},
		{"https://localhost:8443", true},
		{"http://localhost:9000", false},
		{"http://10.0.0.1:9000", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			assert.Equal(t, tt.expected, shouldUseColonyProxy(tt.url))
		})
	}
}

func TestDuckDBAttachBase(t *testing.T) {
	// Set environment variables so config resolution doesn't fail on machines without ~/.coral
	t.Setenv("CORAL_COLONY_ID", "test-colony")
	t.Setenv("CORAL_COLONY_ENDPOINT", "https://colony.example.com:8443")

	tests := []struct {
		url      string
		expected string
	}{
		// Remote HTTPS uses HTTPS directly.
		{"https://colony.example.com:8443", "https://colony.example.com:8443"},
		// Localhost HTTPS falls back to internal HTTP (localhost:9000).
		{"https://localhost:8443", "http://localhost:9000"},
		{"https://127.0.0.1:8443", "http://localhost:9000"},
		// Plain HTTP stays as is.
		{"http://colony.example.com:9000", "http://colony.example.com:9000"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			assert.Equal(t, tt.expected, duckdbAttachBase(tt.url))
		})
	}
}

func TestAgentDuckDBBase(t *testing.T) {
	ctx := context.Background()

	// Test proxy mode.
	t.Setenv("CORAL_COLONY_ENDPOINT", "https://colony.example.com:8443")
	proxyBase, err := agentDuckDBBase(ctx, "agent123", "")
	require.NoError(t, err)
	assert.Equal(t, "https://colony.example.com:8443/agent/agent123", proxyBase)

	// Test local mode (direct mesh IP).
	t.Setenv("CORAL_COLONY_ENDPOINT", "http://localhost:9000")
	localBase, err := agentDuckDBBase(ctx, "agent123", "10.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.1:9001", localBase)
}
