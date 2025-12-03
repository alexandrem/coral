package ebpf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	sdkv1 "github.com/coral-mesh/coral/coral/sdk/v1"
	"github.com/rs/zerolog"
)

// SDKClient wraps the SDK debug service client for querying function metadata.
type SDKClient struct {
	logger zerolog.Logger
	client *http.Client
	addr   string
}

// NewSDKClient creates a new SDK client for the given address.
func NewSDKClient(logger zerolog.Logger, addr string) *SDKClient {
	return &SDKClient{
		logger: logger.With().Str("component", "sdk_client").Str("addr", addr).Logger(),
		client: &http.Client{},
		addr:   addr,
	}
}

// GetFunctionMetadata queries the SDK for function metadata.
func (c *SDKClient) GetFunctionMetadata(ctx context.Context, functionName string) (*sdkv1.FunctionMetadata, error) {
	c.logger.Debug().Str("function", functionName).Msg("Querying SDK for function metadata")

	url := fmt.Sprintf("http://%s/debug/functions/%s", c.addr, functionName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("function not found: %s", functionName)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Define struct to match JSON response from SDK
	type ArgumentMetadata struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Offset int64  `json:"offset"`
	}
	type ReturnValueMetadata struct {
		Type   string `json:"type"`
		Offset int64  `json:"offset"`
	}
	type FunctionMetadata struct {
		Name         string                 `json:"name"`
		BinaryPath   string                 `json:"binary_path"`
		Offset       uint64                 `json:"offset"`
		PID          int                    `json:"pid"`
		Arguments    []*ArgumentMetadata    `json:"arguments"`
		ReturnValues []*ReturnValueMetadata `json:"return_values"`
	}

	var meta FunctionMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Debug().
		Str("function", functionName).
		Uint64("offset", meta.Offset).
		Str("binary", meta.BinaryPath).
		Msg("Got function metadata from SDK")

	// Map to protobuf struct
	pbMeta := &sdkv1.FunctionMetadata{
		Name:       meta.Name,
		BinaryPath: meta.BinaryPath,
		Offset:     meta.Offset,
		Pid:        uint32(meta.PID),
	}

	for _, arg := range meta.Arguments {
		pbMeta.Arguments = append(pbMeta.Arguments, &sdkv1.ArgumentMetadata{
			Name:   arg.Name,
			Type:   arg.Type,
			Offset: uint64(arg.Offset),
		})
	}

	for _, ret := range meta.ReturnValues {
		pbMeta.ReturnValues = append(pbMeta.ReturnValues, &sdkv1.ReturnValueMetadata{
			Type:   ret.Type,
			Offset: uint64(ret.Offset),
		})
	}

	return pbMeta, nil
}

// ListFunctions queries the SDK for available functions.
func (c *SDKClient) ListFunctions(ctx context.Context, packagePattern string) ([]string, error) {
	baseURL := fmt.Sprintf("http://%s/debug/functions", c.addr)
	u, _ := url.Parse(baseURL)
	q := u.Query()
	if packagePattern != "" {
		q.Set("pattern", packagePattern)
	}
	q.Set("limit", "1000") // Reasonable default limit
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	type BasicInfo struct {
		Name string `json:"name"`
	}
	type ListFunctionsResponse struct {
		Functions []*BasicInfo `json:"functions"`
	}

	var listResp ListFunctionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	names := make([]string, len(listResp.Functions))
	for i, fn := range listResp.Functions {
		names[i] = fn.Name
	}

	return names, nil
}
