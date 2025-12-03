package debug

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Server provides the SDK Debug Service HTTP server.
type Server struct {
	logger   *slog.Logger
	provider *FunctionMetadataProvider
	listener net.Listener
	server   *http.Server
	addr     string
}

// NewServer creates a new SDK debug server.
func NewServer(logger *slog.Logger, provider *FunctionMetadataProvider) (*Server, error) {
	return &Server{
		logger:   logger.With("component", "sdk-debug-server"),
		provider: provider,
	}, nil
}

// Start starts the HTTP server on the specified address.
func (s *Server) Start(listenAddr string) error {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	s.listener = listener
	s.addr = s.listener.Addr().String()

	// Create HTTP server.
	s.server = &http.Server{
		Handler: s,
	}

	// Start serving in background.
	go func() {
		s.logger.Info("SDK debug server started", "addr", s.listener.Addr().String())
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Failed to start HTTP server", "error", err)
		}
	}()

	return nil
}

// Stop stops the HTTP server.
func (s *Server) Stop() error {
	s.logger.Info("Stopping SDK debug server")
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// Addr returns the server's listen address.
func (s *Server) Addr() string {
	return s.addr
}

// ServeHTTP implements the http.Handler interface.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers for local development tools
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch {
	case r.URL.Path == "/debug/capabilities":
		s.handleCapabilities(w, r)
	case r.URL.Path == "/debug/functions":
		s.handleListFunctions(w, r)
	case r.URL.Path == "/debug/functions/export":
		s.handleExportFunctions(w, r)
	case strings.HasPrefix(r.URL.Path, "/debug/functions/"):
		s.handleGetFunction(w, r)
	default:
		http.NotFound(w, r)
	}
}

// CapabilitiesResponse defines the response for /debug/capabilities.
type CapabilitiesResponse struct {
	ProcessID       string `json:"process_id"`
	SdkVersion      string `json:"sdk_version"`
	HasDwarfSymbols bool   `json:"has_dwarf_symbols"`
	FunctionCount   int    `json:"function_count"`
	BinaryPath      string `json:"binary_path"`
	BinaryHash      string `json:"binary_hash"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	binHash, err := s.provider.GetBinaryHash()
	if err != nil {
		s.logger.Warn("Failed to calculate binary hash", "error", err)
		binHash = "unknown"
	}

	caps := CapabilitiesResponse{
		ProcessID:       strconv.Itoa(s.provider.pid),
		SdkVersion:      "v0.2.0", // TODO: Get from version package
		HasDwarfSymbols: s.provider.HasDWARF(),
		FunctionCount:   s.provider.GetFunctionCount(),
		BinaryPath:      s.provider.BinaryPath(),
		BinaryHash:      binHash,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(caps)
}

// ListFunctionsResponse defines the response for /debug/functions.
type ListFunctionsResponse struct {
	Functions []*BasicInfo `json:"functions"`
	Total     int          `json:"total"`
	Returned  int          `json:"returned"`
	Offset    int          `json:"offset"`
	HasMore   bool         `json:"has_more"`
}

func (s *Server) handleListFunctions(w http.ResponseWriter, r *http.Request) {
	// Parse pagination params
	limit := parseInt(r.URL.Query().Get("limit"), 100, 1000)
	offset := parseInt(r.URL.Query().Get("offset"), 0, math.MaxInt)
	pattern := r.URL.Query().Get("pattern")

	// Get filtered functions
	functions, total := s.provider.ListFunctions(pattern, limit, offset)

	// Enable gzip compression for large responses if client supports it
	// Note: net/http automatically handles this if we don't set Content-Encoding manually,
	// but explicit handling is often better for API control.
	// For simplicity, we'll rely on standard library behavior or middleware if added later.
	// But here we just return JSON.

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Total-Count", strconv.Itoa(total))

	resp := ListFunctionsResponse{
		Functions: functions,
		Total:     total,
		Returned:  len(functions),
		Offset:    offset,
		HasMore:   offset+len(functions) < total,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGetFunction(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/debug/functions/")
	if name == "" {
		http.Error(w, "function name required", http.StatusBadRequest)
		return
	}

	metadata, err := s.provider.GetFunctionMetadata(name)
	if err != nil {
		http.Error(w, fmt.Sprintf("function not found: %s", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

func (s *Server) handleExportFunctions(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	binHash, _ := s.provider.GetBinaryHash()
	if binHash == "" {
		binHash = "unknown"
	}

	// Set headers for compressed download
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="functions-%s.%s.gz"`,
			binHash[:8], format))
	w.Header().Set("X-Total-Functions",
		strconv.Itoa(s.provider.GetFunctionCount()))

	// Create gzip writer
	gzWriter := gzip.NewWriter(w)
	defer func() { _ = gzWriter.Close() }()

	functions := s.provider.ListAllFunctions()

	if format == "ndjson" {
		// Stream newline-delimited JSON (efficient for large datasets)
		encoder := json.NewEncoder(gzWriter)
		for _, fn := range functions {
			_ = encoder.Encode(fn)
		}
	} else {
		// Standard JSON array
		_ = json.NewEncoder(gzWriter).Encode(functions)
	}
}

func parseInt(value string, defaultVal, maxVal int) int {
	if value == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(value)
	if err != nil {
		return defaultVal
	}
	if i < 0 {
		return 0
	}
	if i > maxVal {
		return maxVal
	}
	return i
}
