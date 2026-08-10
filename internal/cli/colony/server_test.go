package colony

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBlockBootstrapAndRegister is the RFD 109 regression guarding "No
// broad rendezvous handler": the ordinary mesh/public listener must 404
// BootstrapAndRegister even though the underlying ColonyServiceHandler
// implements it, while every other procedure is forwarded unchanged.
func TestBlockBootstrapAndRegister(t *testing.T) {
	var forwardedPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	wrapped := blockBootstrapAndRegister(inner)

	t.Run("blocks BootstrapAndRegister", func(t *testing.T) {
		forwardedPath = ""
		req := httptest.NewRequest(http.MethodPost, "/coral.colony.v1.ColonyService/BootstrapAndRegister", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if forwardedPath != "" {
			t.Fatal("expected the inner handler to never be invoked")
		}
	})

	t.Run("forwards other procedures", func(t *testing.T) {
		forwardedPath = ""
		req := httptest.NewRequest(http.MethodPost, "/coral.colony.v1.ColonyService/RequestCertificate", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if forwardedPath != "/coral.colony.v1.ColonyService/RequestCertificate" {
			t.Fatalf("expected the inner handler to be invoked with the original path, got %q", forwardedPath)
		}
	})
}
