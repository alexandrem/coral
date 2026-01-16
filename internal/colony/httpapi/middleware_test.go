// Package httpapi provides tests for HTTP middleware (RFD 031).
package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/auth"
)

// Auth middleware tests.

func TestAuthMiddleware_ValidToken(t *testing.T) {
	store := auth.NewTokenStore("")
	info, err := store.GenerateToken("test-token", []auth.Permission{auth.PermissionStatus}, "")
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	logger := zerolog.Nop()
	middleware := NewAuthMiddleware(store, logger)

	var capturedToken *auth.APIToken
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = GetAuthenticatedToken(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusOK)
	}

	if capturedToken == nil {
		t.Error("Token was not set in context")
	} else if capturedToken.TokenID != "test-token" {
		t.Errorf("TokenID = %q, want %q", capturedToken.TokenID, "test-token")
	}
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	store := auth.NewTokenStore("")
	logger := zerolog.Nop()
	middleware := NewAuthMiddleware(store, logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	store := auth.NewTokenStore("")
	logger := zerolog.Nop()
	middleware := NewAuthMiddleware(store, logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer coral_invalidtoken123")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_BearerCaseInsensitive(t *testing.T) {
	store := auth.NewTokenStore("")
	info, err := store.GenerateToken("test-token", []auth.Permission{auth.PermissionStatus}, "")
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	logger := zerolog.Nop()
	middleware := NewAuthMiddleware(store, logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	tests := []string{"Bearer", "bearer", "BEARER", "BeArEr"}

	for _, prefix := range tests {
		t.Run(prefix, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Authorization", prefix+" "+info.Token)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d for prefix %q", rec.Code, http.StatusOK, prefix)
			}
		})
	}
}

func TestGetAuthenticatedToken_NoToken(t *testing.T) {
	ctx := context.Background()
	token := GetAuthenticatedToken(ctx)
	if token != nil {
		t.Errorf("GetAuthenticatedToken() = %v, want nil", token)
	}
}

// RBAC middleware tests.

func TestRBACMiddleware_PermissionGranted(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRBACMiddleware(logger)

	tests := []struct {
		name        string
		path        string
		permissions []auth.Permission
		wantStatus  int
	}{
		{
			name:        "status permission for GetStatus",
			path:        "/coral.colony.v1.ColonyService/GetStatus",
			permissions: []auth.Permission{auth.PermissionStatus},
			wantStatus:  http.StatusOK,
		},
		{
			name:        "query permission for QueryUnifiedSummary",
			path:        "/coral.colony.v1.ColonyService/QueryUnifiedSummary",
			permissions: []auth.Permission{auth.PermissionQuery},
			wantStatus:  http.StatusOK,
		},
		{
			name:        "admin has all permissions",
			path:        "/coral.colony.v1.ColonyService/RequestCertificate",
			permissions: []auth.Permission{auth.PermissionAdmin},
			wantStatus:  http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			wrapped := middleware.Handler(handler)

			token := &auth.APIToken{
				TokenID:     "test-token",
				Permissions: tt.permissions,
			}

			req := httptest.NewRequest("GET", tt.path, nil)
			ctx := context.WithValue(req.Context(), TokenContextKey, token)
			req = req.WithContext(ctx)

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestRBACMiddleware_PermissionDenied(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRBACMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called when permission denied")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	token := &auth.APIToken{
		TokenID:     "test-token",
		Permissions: []auth.Permission{auth.PermissionStatus},
	}

	// Status token cannot access query endpoints.
	req := httptest.NewRequest("GET", "/coral.colony.v1.ColonyService/QueryUnifiedSummary", nil)
	ctx := context.WithValue(req.Context(), TokenContextKey, token)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRBACMiddleware_NoToken(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRBACMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Handler should not be called without token")
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// Rate limit middleware tests.

func TestRateLimitMiddleware_NoToken(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRateLimitMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRateLimitMiddleware_NoRateLimit(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRateLimitMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	token := &auth.APIToken{
		TokenID:     "test-token",
		Permissions: []auth.Permission{auth.PermissionStatus},
		RateLimit:   "",
	}

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), TokenContextKey, token)
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: Status = %d, want %d", i, rec.Code, http.StatusOK)
		}
	}
}

func TestRateLimitMiddleware_WithRateLimit(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRateLimitMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	token := &auth.APIToken{
		TokenID:     "limited-token",
		Permissions: []auth.Permission{auth.PermissionStatus},
		RateLimit:   "3/minute",
	}

	// First 3 requests should succeed.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), TokenContextKey, token)
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Request %d: Status = %d, want %d", i, rec.Code, http.StatusOK)
		}

		if rec.Header().Get("X-RateLimit-Limit") != "3" {
			t.Errorf("X-RateLimit-Limit = %q, want %q", rec.Header().Get("X-RateLimit-Limit"), "3")
		}

		remaining, _ := strconv.Atoi(rec.Header().Get("X-RateLimit-Remaining"))
		expectedRemaining := 2 - i
		if remaining != expectedRemaining {
			t.Errorf("Request %d: X-RateLimit-Remaining = %d, want %d", i, remaining, expectedRemaining)
		}
	}

	// 4th request should fail.
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), TokenContextKey, token)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Request 4: Status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header not set")
	}
}

func TestRateLimitMiddleware_WindowExpiry(t *testing.T) {
	logger := zerolog.Nop()
	middleware := NewRateLimitMiddleware(logger)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := middleware.Handler(handler)

	token := &auth.APIToken{
		TokenID:     "expiry-test",
		Permissions: []auth.Permission{auth.PermissionStatus},
		RateLimit:   "1/second",
	}

	// First request should succeed.
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), TokenContextKey, token)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("First request: Status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Second request should fail.
	req = httptest.NewRequest("GET", "/test", nil)
	ctx = context.WithValue(req.Context(), TokenContextKey, token)
	req = req.WithContext(ctx)

	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Second request: Status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	// Wait for window to expire.
	time.Sleep(1100 * time.Millisecond)

	// Should be allowed again.
	req = httptest.NewRequest("GET", "/test", nil)
	ctx = context.WithValue(req.Context(), TokenContextKey, token)
	req = req.WithContext(ctx)

	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("After expiry: Status = %d, want %d", rec.Code, http.StatusOK)
	}
}
