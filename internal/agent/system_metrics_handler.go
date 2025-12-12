package agent

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/collector"
)

// SystemMetricsHandler implements the QuerySystemMetrics RPC for agents (RFD 071).
type SystemMetricsHandler struct {
	storage *collector.Storage
}

// NewSystemMetricsHandler creates a new system metrics handler.
func NewSystemMetricsHandler(storage *collector.Storage) *SystemMetricsHandler {
	return &SystemMetricsHandler{
		storage: storage,
	}
}

// QuerySystemMetrics implements the QuerySystemMetrics RPC.
// Colony calls this to query system metrics from agent's local storage.
func (h *SystemMetricsHandler) QuerySystemMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QuerySystemMetricsRequest],
) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	// Convert Unix seconds to time.Time.
	startTime := time.Unix(req.Msg.StartTime, 0)
	endTime := time.Unix(req.Msg.EndTime, 0)

	// Query metrics from local storage.
	metrics, err := h.storage.QueryMetrics(ctx, startTime, endTime, req.Msg.MetricNames)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert internal metrics to protobuf metrics.
	pbMetrics := make([]*agentv1.SystemMetric, 0, len(metrics))
	for _, metric := range metrics {
		// Convert attributes map to JSON string.
		attributesJSON := ""
		if len(metric.Attributes) > 0 {
			attrBytes, err := json.Marshal(metric.Attributes)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			attributesJSON = string(attrBytes)
		}

		pbMetric := &agentv1.SystemMetric{
			Timestamp:  metric.Timestamp.UnixMilli(),
			Name:       metric.Name,
			Value:      metric.Value,
			Unit:       metric.Unit,
			MetricType: metric.MetricType,
			Attributes: attributesJSON,
		}
		pbMetrics = append(pbMetrics, pbMetric)
	}

	return connect.NewResponse(&agentv1.QuerySystemMetricsResponse{
		Metrics:      pbMetrics,
		TotalMetrics: int32(len(pbMetrics)),
	}), nil
}
