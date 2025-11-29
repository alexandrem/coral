package debug

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	sdkv1 "github.com/coral-mesh/coral/coral/sdk/v1"
	"github.com/coral-mesh/coral/coral/sdk/v1/sdkv1connect"
)

// Server provides the SDK Debug Service gRPC server.
type Server struct {
	logger   zerolog.Logger
	provider *FunctionMetadataProvider
	listener net.Listener
	server   *http.Server
	addr     string
}

// NewServer creates a new SDK debug server.
func NewServer(logger zerolog.Logger, provider *FunctionMetadataProvider) (*Server, error) {
	return &Server{
		logger:   logger.With().Str("component", "sdk-debug-server").Logger(),
		provider: provider,
	}, nil
}

// Start starts the gRPC server on an auto-selected port.
func (s *Server) Start() error {
	// Listen on localhost with auto-selected port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	s.listener = listener
	s.addr = listener.Addr().String()

	// Create Connect-RPC handler.
	mux := http.NewServeMux()
	path, handler := sdkv1connect.NewSDKDebugServiceHandler(s)
	mux.Handle(path, handler)

	// Create HTTP/2 server.
	s.server = &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	// Start serving in background.
	go func() {
		s.logger.Info().Str("addr", s.addr).Msg("SDK debug server started")
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error().Err(err).Msg("SDK debug server error")
		}
	}()

	return nil
}

// Stop stops the gRPC server.
func (s *Server) Stop() error {
	s.logger.Info().Msg("Stopping SDK debug server")
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// Addr returns the server's listen address.
func (s *Server) Addr() string {
	return s.addr
}

// GetFunctionMetadata implements the SDKDebugService RPC.
func (s *Server) GetFunctionMetadata(
	ctx context.Context,
	req *connect.Request[sdkv1.GetFunctionMetadataRequest],
) (*connect.Response[sdkv1.GetFunctionMetadataResponse], error) {
	functionName := req.Msg.FunctionName

	s.logger.Debug().Str("function", functionName).Msg("GetFunctionMetadata request")

	// Get metadata from provider.
	metadata, err := s.provider.GetFunctionMetadata(functionName)
	if err != nil {
		s.logger.Warn().Err(err).Str("function", functionName).Msg("Function not found")
		return connect.NewResponse(&sdkv1.GetFunctionMetadataResponse{
			Found: false,
			Error: err.Error(),
		}), nil
	}

	// Convert to protobuf.
	resp := &sdkv1.GetFunctionMetadataResponse{
		Found: true,
		Metadata: &sdkv1.FunctionMetadata{
			Name:         metadata.Name,
			BinaryPath:   metadata.BinaryPath,
			Offset:       metadata.Offset,
			Pid:          metadata.PID,
			Arguments:    convertArguments(metadata.Arguments),
			ReturnValues: convertReturnValues(metadata.ReturnValues),
		},
	}

	s.logger.Info().
		Str("function", functionName).
		Uint64("offset", metadata.Offset).
		Msg("Returned function metadata")

	return connect.NewResponse(resp), nil
}

// ListFunctions implements the SDKDebugService RPC.
func (s *Server) ListFunctions(
	ctx context.Context,
	req *connect.Request[sdkv1.ListFunctionsRequest],
) (*connect.Response[sdkv1.ListFunctionsResponse], error) {
	pattern := req.Msg.PackagePattern

	s.logger.Debug().Str("pattern", pattern).Msg("ListFunctions request")

	// Get function list from provider.
	functions, err := s.provider.ListFunctions(pattern)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logger.Info().
		Str("pattern", pattern).
		Int("count", len(functions)).
		Msg("Returned function list")

	return connect.NewResponse(&sdkv1.ListFunctionsResponse{
		Functions: functions,
	}), nil
}

// convertArguments converts internal argument metadata to protobuf.
func convertArguments(args []*ArgumentMetadata) []*sdkv1.ArgumentMetadata {
	result := make([]*sdkv1.ArgumentMetadata, len(args))
	for i, arg := range args {
		result[i] = &sdkv1.ArgumentMetadata{
			Name:   arg.Name,
			Type:   arg.Type,
			Offset: arg.Offset,
		}
	}
	return result
}

// convertReturnValues converts internal return value metadata to protobuf.
func convertReturnValues(retVals []*ReturnValueMetadata) []*sdkv1.ReturnValueMetadata {
	result := make([]*sdkv1.ReturnValueMetadata, len(retVals))
	for i, rv := range retVals {
		result[i] = &sdkv1.ReturnValueMetadata{
			Type:   rv.Type,
			Offset: rv.Offset,
		}
	}
	return result
}
