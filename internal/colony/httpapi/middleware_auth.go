// Package httpapi provides authentication middleware for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/auth"
)

// ContextKey is the type for context keys used by middleware.
type ContextKey string

const (
	// TokenContextKey is the context key for the authenticated token.
	TokenContextKey ContextKey = "authenticated_token"
)

// AuthMiddleware validates Bearer tokens in the Authorization header.
type AuthMiddleware struct {
	tokenStore *auth.TokenStore
	logger     zerolog.Logger
}

// NewAuthMiddleware creates a new authentication middleware.
func NewAuthMiddleware(store *auth.TokenStore, logger zerolog.Logger) *AuthMiddleware {
	return &AuthMiddleware{
		tokenStore: store,
		logger:     logger.With().Str("middleware", "auth").Logger(),
	}
}

// Handler wraps an http.Handler with authentication.
func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header.
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			m.logger.Debug().
				Str("path", r.URL.Path).
				Str("remote_addr", r.RemoteAddr).
				Msg("Missing Authorization header")
			http.Error(w, "Unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		// Expect "Bearer <token>".
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			m.logger.Debug().
				Str("path", r.URL.Path).
				Str("remote_addr", r.RemoteAddr).
				Msg("Invalid Authorization header format")
			http.Error(w, "Unauthorized: invalid Authorization format (expected 'Bearer <token>')", http.StatusUnauthorized)
			return
		}

		token := parts[1]

		// Validate token.
		storedToken, err := m.tokenStore.ValidateToken(token)
		if err != nil {
			m.logger.Warn().
				Str("path", r.URL.Path).
				Str("remote_addr", r.RemoteAddr).
				Msg("Invalid token")
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		m.logger.Debug().
			Str("token_id", storedToken.TokenID).
			Str("path", r.URL.Path).
			Msg("Request authenticated")

		// Add token to context.
		ctx := context.WithValue(r.Context(), TokenContextKey, storedToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetAuthenticatedToken retrieves the authenticated token from the request context.
// Returns nil if no token is found (e.g., unauthenticated request or middleware not applied).
func GetAuthenticatedToken(ctx context.Context) *auth.APIToken {
	token, _ := ctx.Value(TokenContextKey).(*auth.APIToken)
	return token
}

// RequireAuth creates an auth middleware that requires authentication.
// This is a convenience function that combines NewAuthMiddleware with Handler.
func RequireAuth(store *auth.TokenStore, logger zerolog.Logger) func(http.Handler) http.Handler {
	mw := NewAuthMiddleware(store, logger)
	return mw.Handler
}
