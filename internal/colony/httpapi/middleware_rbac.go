// Package httpapi provides RBAC middleware for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"net/http"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/auth"
)

// RBACMiddleware checks permissions for each request based on the method path.
type RBACMiddleware struct {
	logger zerolog.Logger
}

// NewRBACMiddleware creates a new RBAC middleware.
func NewRBACMiddleware(logger zerolog.Logger) *RBACMiddleware {
	return &RBACMiddleware{
		logger: logger.With().Str("middleware", "rbac").Logger(),
	}
}

// Handler wraps an http.Handler with RBAC checks.
func (m *RBACMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := GetAuthenticatedToken(r.Context())
		if token == nil {
			// Should not happen if auth middleware ran first.
			m.logger.Error().
				Str("path", r.URL.Path).
				Msg("No authenticated token in context - auth middleware may not have run")
			http.Error(w, "Internal Server Error: authentication required", http.StatusInternalServerError)
			return
		}

		// Determine required permission based on path.
		requiredPerm := GetRequiredPermission(r.URL.Path)

		if !auth.HasPermission(token, requiredPerm) {
			m.logger.Warn().
				Str("token_id", token.TokenID).
				Str("path", r.URL.Path).
				Str("required_permission", string(requiredPerm)).
				Strs("token_permissions", permissionsToStrings(token.Permissions)).
				Msg("Permission denied")
			http.Error(w, "Forbidden: insufficient permissions", http.StatusForbidden)
			return
		}

		m.logger.Debug().
			Str("token_id", token.TokenID).
			Str("path", r.URL.Path).
			Str("permission", string(requiredPerm)).
			Msg("Permission granted")

		next.ServeHTTP(w, r)
	})
}

// permissionsToStrings converts a slice of permissions to strings for logging.
func permissionsToStrings(perms []auth.Permission) []string {
	result := make([]string, len(perms))
	for i, p := range perms {
		result[i] = string(p)
	}
	return result
}

// RequireRBAC creates an RBAC middleware.
// This is a convenience function.
func RequireRBAC(logger zerolog.Logger) func(http.Handler) http.Handler {
	mw := NewRBACMiddleware(logger)
	return mw.Handler
}
