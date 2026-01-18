// Package httpapi provides tests for token management (RFD 031).
package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStore_GenerateToken(t *testing.T) {
	ts := NewTokenStore("")

	tests := []struct {
		name        string
		tokenID     string
		permissions []Permission
		rateLimit   string
		wantErr     bool
	}{
		{
			name:        "basic token",
			tokenID:     "test-token",
			permissions: []Permission{PermissionStatus, PermissionQuery},
			rateLimit:   "",
			wantErr:     false,
		},
		{
			name:        "token with rate limit",
			tokenID:     "limited-token",
			permissions: []Permission{PermissionStatus},
			rateLimit:   "100/hour",
			wantErr:     false,
		},
		{
			name:        "admin token",
			tokenID:     "admin-token",
			permissions: []Permission{PermissionAdmin},
			rateLimit:   "",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ts.GenerateToken(tt.tokenID, tt.permissions, tt.rateLimit)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if info.TokenID != tt.tokenID {
					t.Errorf("TokenID = %v, want %v", info.TokenID, tt.tokenID)
				}

				if !strings.HasPrefix(info.Token, "coral_") {
					t.Errorf("Token should have 'coral_' prefix, got %v", info.Token)
				}

				if len(info.Permissions) != len(tt.permissions) {
					t.Errorf("Permissions length = %v, want %v", len(info.Permissions), len(tt.permissions))
				}

				if info.RateLimit != tt.rateLimit {
					t.Errorf("RateLimit = %v, want %v", info.RateLimit, tt.rateLimit)
				}
			}
		})
	}
}

func TestTokenStore_GenerateToken_Duplicate(t *testing.T) {
	ts := NewTokenStore("")

	_, err := ts.GenerateToken("duplicate-id", []Permission{PermissionStatus}, "")
	if err != nil {
		t.Fatalf("First GenerateToken() failed: %v", err)
	}

	_, err = ts.GenerateToken("duplicate-id", []Permission{PermissionStatus}, "")
	if err == nil {
		t.Error("Expected error for duplicate token ID, got nil")
	}
}

func TestTokenStore_ValidateToken(t *testing.T) {
	ts := NewTokenStore("")

	// Generate a token.
	info, err := ts.GenerateToken("valid-token", []Permission{PermissionStatus, PermissionQuery}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	// Validate the token.
	token, err := ts.ValidateToken(info.Token)
	if err != nil {
		t.Errorf("ValidateToken() with valid token failed: %v", err)
	}

	if token == nil {
		t.Fatal("ValidateToken() returned nil token")
	}

	if token.TokenID != "valid-token" {
		t.Errorf("TokenID = %v, want valid-token", token.TokenID)
	}
}

func TestTokenStore_ValidateToken_Invalid(t *testing.T) {
	ts := NewTokenStore("")

	// Generate a token.
	_, err := ts.GenerateToken("test-token", []Permission{PermissionStatus}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:    "empty token",
			token:   "",
			wantErr: true,
		},
		{
			name:    "invalid token",
			token:   "coral_invalid",
			wantErr: true,
		},
		{
			name:    "random string",
			token:   "random_garbage_string",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ts.ValidateToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateToken() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTokenStore_ValidateToken_Revoked(t *testing.T) {
	ts := NewTokenStore("")

	// Generate and revoke a token.
	info, err := ts.GenerateToken("revoked-token", []Permission{PermissionStatus}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	if err := ts.RevokeToken("revoked-token"); err != nil {
		t.Fatalf("RevokeToken() failed: %v", err)
	}

	// Validation should fail.
	_, err = ts.ValidateToken(info.Token)
	if err == nil {
		t.Error("Expected error validating revoked token, got nil")
	}
}

func TestTokenStore_ListTokens(t *testing.T) {
	ts := NewTokenStore("")

	// Generate some tokens.
	_, err := ts.GenerateToken("token1", []Permission{PermissionStatus}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	_, err = ts.GenerateToken("token2", []Permission{PermissionQuery}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	// List tokens.
	tokens := ts.ListTokens()
	if len(tokens) != 2 {
		t.Errorf("ListTokens() returned %d tokens, want 2", len(tokens))
	}

	// Revoke one and list again.
	if err := ts.RevokeToken("token1"); err != nil {
		t.Fatalf("RevokeToken() failed: %v", err)
	}

	tokens = ts.ListTokens()
	if len(tokens) != 1 {
		t.Errorf("ListTokens() after revoke returned %d tokens, want 1", len(tokens))
	}

	if tokens[0].TokenID != "token2" {
		t.Errorf("Remaining token ID = %v, want token2", tokens[0].TokenID)
	}
}

func TestTokenStore_GetToken(t *testing.T) {
	ts := NewTokenStore("")

	_, err := ts.GenerateToken("get-test", []Permission{PermissionStatus}, "100/hour")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	token, exists := ts.GetToken("get-test")
	if !exists {
		t.Error("GetToken() returned exists=false for existing token")
	}

	if token.TokenID != "get-test" {
		t.Errorf("TokenID = %v, want get-test", token.TokenID)
	}

	if token.RateLimit != "100/hour" {
		t.Errorf("RateLimit = %v, want 100/hour", token.RateLimit)
	}

	_, exists = ts.GetToken("nonexistent")
	if exists {
		t.Error("GetToken() returned exists=true for nonexistent token")
	}
}

func TestTokenStore_DeleteToken(t *testing.T) {
	ts := NewTokenStore("")

	_, err := ts.GenerateToken("delete-test", []Permission{PermissionStatus}, "")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	if err := ts.DeleteToken("delete-test"); err != nil {
		t.Errorf("DeleteToken() failed: %v", err)
	}

	_, exists := ts.GetToken("delete-test")
	if exists {
		t.Error("Token still exists after deletion")
	}

	// Deleting nonexistent token should error.
	if err := ts.DeleteToken("nonexistent"); err == nil {
		t.Error("Expected error deleting nonexistent token")
	}
}

func TestTokenStore_Persistence(t *testing.T) {
	// Create a temp file for tokens.
	tmpDir := t.TempDir()
	tokensFile := filepath.Join(tmpDir, "tokens.yaml")

	// Create a token store and generate a token.
	ts1 := NewTokenStore(tokensFile)
	info, err := ts1.GenerateToken("persist-test", []Permission{PermissionStatus, PermissionQuery}, "50/minute")
	if err != nil {
		t.Fatalf("GenerateToken() failed: %v", err)
	}

	// Verify file was created.
	if _, err := os.Stat(tokensFile); os.IsNotExist(err) {
		t.Fatal("Tokens file was not created")
	}

	// Create a new token store from the same file.
	ts2 := NewTokenStore(tokensFile)

	// Verify token was loaded.
	tokens := ts2.ListTokens()
	if len(tokens) != 1 {
		t.Errorf("Loaded %d tokens, want 1", len(tokens))
	}

	if tokens[0].TokenID != "persist-test" {
		t.Errorf("TokenID = %v, want persist-test", tokens[0].TokenID)
	}

	if tokens[0].RateLimit != "50/minute" {
		t.Errorf("RateLimit = %v, want 50/minute", tokens[0].RateLimit)
	}

	// Verify the token can be validated.
	token, err := ts2.ValidateToken(info.Token)
	if err != nil {
		t.Errorf("ValidateToken() on reloaded store failed: %v", err)
	}

	if token.TokenID != "persist-test" {
		t.Errorf("Validated TokenID = %v, want persist-test", token.TokenID)
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		name       string
		tokenPerms []Permission
		required   Permission
		want       bool
	}{
		{
			name:       "exact match",
			tokenPerms: []Permission{PermissionStatus, PermissionQuery},
			required:   PermissionStatus,
			want:       true,
		},
		{
			name:       "no match",
			tokenPerms: []Permission{PermissionStatus},
			required:   PermissionQuery,
			want:       false,
		},
		{
			name:       "admin grants all",
			tokenPerms: []Permission{PermissionAdmin},
			required:   PermissionDebug,
			want:       true,
		},
		{
			name:       "admin grants status",
			tokenPerms: []Permission{PermissionAdmin},
			required:   PermissionStatus,
			want:       true,
		},
		{
			name:       "multiple permissions",
			tokenPerms: []Permission{PermissionStatus, PermissionQuery, PermissionAnalyze},
			required:   PermissionAnalyze,
			want:       true,
		},
		{
			name:       "empty permissions",
			tokenPerms: []Permission{},
			required:   PermissionStatus,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &APIToken{
				TokenID:     "test",
				Permissions: tt.tokenPerms,
				CreatedAt:   time.Now(),
			}

			got := HasPermission(token, tt.required)
			if got != tt.want {
				t.Errorf("HasPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParsePermission(t *testing.T) {
	tests := []struct {
		input string
		want  Permission
	}{
		{"status", PermissionStatus},
		{"query", PermissionQuery},
		{"analyze", PermissionAnalyze},
		{"debug", PermissionDebug},
		{"admin", PermissionAdmin},
		{"invalid", ""},
		{"", ""},
		{"STATUS", ""}, // Case-sensitive, uppercase doesn't match.
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParsePermission(tt.input)
			if got != tt.want {
				t.Errorf("ParsePermission(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllPermissions(t *testing.T) {
	perms := AllPermissions()
	if len(perms) != 5 {
		t.Errorf("AllPermissions() returned %d permissions, want 5", len(perms))
	}

	// Verify all expected permissions are present.
	expected := map[Permission]bool{
		PermissionStatus:  true,
		PermissionQuery:   true,
		PermissionAnalyze: true,
		PermissionDebug:   true,
		PermissionAdmin:   true,
	}

	for _, p := range perms {
		if !expected[p] {
			t.Errorf("Unexpected permission: %v", p)
		}
		delete(expected, p)
	}

	if len(expected) > 0 {
		t.Errorf("Missing permissions: %v", expected)
	}
}
