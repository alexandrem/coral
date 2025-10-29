package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateColonyID(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		environment string
		wantPrefix  string
	}{
		{
			name:        "simple names",
			appName:     "myapp",
			environment: "production",
			wantPrefix:  "myapp-production-",
		},
		{
			name:        "names with spaces",
			appName:     "My Shop",
			environment: "dev",
			wantPrefix:  "my-shop-dev-",
		},
		{
			name:        "names with special characters",
			appName:     "Payment$API!",
			environment: "staging",
			wantPrefix:  "payment-api-staging-",
		},
		{
			name:        "names with multiple consecutive spaces",
			appName:     "My   App",
			environment: "test",
			wantPrefix:  "my-app-test-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			colonyID, err := GenerateColonyID(tt.appName, tt.environment)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(colonyID, tt.wantPrefix),
				"colony ID %q should start with %q", colonyID, tt.wantPrefix)

			// Verify it ends with 6 hex characters
			parts := strings.Split(colonyID, "-")
			lastPart := parts[len(parts)-1]
			assert.Len(t, lastPart, 6, "random suffix should be 6 characters")

			// Verify hex characters
			for _, c := range lastPart {
				assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
					"character %c should be hexadecimal", c)
			}
		})
	}
}

func TestGenerateColonyID_Uniqueness(t *testing.T) {
	// Generate multiple IDs and ensure they're unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		colonyID, err := GenerateColonyID("test", "dev")
		require.NoError(t, err)
		assert.False(t, ids[colonyID], "generated duplicate colony ID: %s", colonyID)
		ids[colonyID] = true
	}
}

func TestGenerateColonySecret(t *testing.T) {
	// Generate a secret
	secret, err := GenerateColonySecret()
	require.NoError(t, err)
	assert.NotEmpty(t, secret)

	// Secret should be base64url encoded (no padding)
	assert.NotContains(t, secret, "=", "secret should not contain padding")
	assert.NotContains(t, secret, "+", "secret should use base64url encoding")
	assert.NotContains(t, secret, "/", "secret should use base64url encoding")

	// Should be 43 characters (32 bytes base64url encoded without padding)
	assert.Len(t, secret, 43)
}

func TestGenerateColonySecret_Uniqueness(t *testing.T) {
	// Generate multiple secrets and ensure they're unique
	secrets := make(map[string]bool)
	for i := 0; i < 100; i++ {
		secret, err := GenerateColonySecret()
		require.NoError(t, err)
		assert.False(t, secrets[secret], "generated duplicate secret")
		secrets[secret] = true
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"Simple", "simple"},
		{"My App", "my-app"},
		{"My   App", "my-app"},
		{"My-App", "my-app"},
		{"My_App", "my-app"},
		{"My$App!", "my-app"},
		{"123app", "123app"},
		{"app-123", "app-123"},
		{"-leading", "leading"},
		{"trailing-", "trailing"},
		{"--multiple--", "multiple"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalize(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
