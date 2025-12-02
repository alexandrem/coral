package ebpf

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	sdkv1 "github.com/coral-mesh/coral/coral/sdk/v1"
	"github.com/coral-mesh/coral/coral/sdk/v1/sdkv1connect"
	"github.com/rs/zerolog"
)

// SDKClient wraps the SDK debug service client for querying function metadata.
type SDKClient struct {
	logger zerolog.Logger
	client sdkv1connect.SDKDebugServiceClient
	addr   string
}

// NewSDKClient creates a new SDK client for the given address.
func NewSDKClient(logger zerolog.Logger, addr string) *SDKClient {
	httpClient := &http.Client{}
	client := sdkv1connect.NewSDKDebugServiceClient(
		httpClient,
		fmt.Sprintf("http://%s", addr),
	)

	return &SDKClient{
		logger: logger.With().Str("component", "sdk_client").Str("addr", addr).Logger(),
		client: client,
		addr:   addr,
	}
}

// GetFunctionMetadata queries the SDK for function metadata.
func (c *SDKClient) GetFunctionMetadata(ctx context.Context, functionName string) (*sdkv1.FunctionMetadata, error) {
	c.logger.Debug().Str("function", functionName).Msg("Querying SDK for function metadata")

	req := connect.NewRequest(&sdkv1.GetFunctionMetadataRequest{
		FunctionName: functionName,
	})

	resp, err := c.client.GetFunctionMetadata(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to query SDK: %w", err)
	}

	if !resp.Msg.Found {
		return nil, fmt.Errorf("function not found: %s (error: %s)", functionName, resp.Msg.Error)
	}

	c.logger.Debug().
		Str("function", functionName).
		Uint64("offset", resp.Msg.Metadata.Offset).
		Str("binary", resp.Msg.Metadata.BinaryPath).
		Msg("Got function metadata from SDK")

	return resp.Msg.Metadata, nil
}

// ListFunctions queries the SDK for available functions.
func (c *SDKClient) ListFunctions(ctx context.Context, packagePattern string) ([]string, error) {
	req := connect.NewRequest(&sdkv1.ListFunctionsRequest{
		PackagePattern: packagePattern,
	})

	resp, err := c.client.ListFunctions(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}

	return resp.Msg.Functions, nil
}
