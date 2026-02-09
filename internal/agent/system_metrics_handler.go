package agent

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/collector"
)

// SystemMetricsHandler implements the QuerySystemMetrics RPC for agents (RFD 071).
type SystemMetricsHandler struct {
	storage   *collector.Storage
	sessionID string // Database session UUID for checkpoint tracking (RFD 089).
}

// NewSystemMetricsHandler creates a new system metrics handler.
func NewSystemMetricsHandler(storage *collector.Storage) *SystemMetricsHandler {
	return &SystemMetricsHandler{
		storage: storage,
	}
}

// SetSessionID sets the database session UUID for checkpoint tracking (RFD 089).
func (h *SystemMetricsHandler) SetSessionID(sessionID string) {
	h.sessionID = sessionID
}

// QuerySystemMetrics implements the QuerySystemMetrics RPC.
// Colony calls this to query system metrics from agent's local storage using sequence-based polling.
func (h *SystemMetricsHandler) QuerySystemMetrics(
	ctx context.Context,
	req *connect.Request[agentv1.QuerySystemMetricsRequest],
) (*connect.Response[agentv1.QuerySystemMetricsResponse], error) {
	metrics, maxSeqID, err := h.storage.QueryMetricsBySeqID(ctx, req.Msg.StartSeqId, req.Msg.MaxRecords, req.Msg.MetricNames)
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
			SeqId:      metric.SeqID,
		}
		pbMetrics = append(pbMetrics, pbMetric)
	}

	return connect.NewResponse(&agentv1.QuerySystemMetricsResponse{
		Metrics:      pbMetrics,
		TotalMetrics: int32(len(pbMetrics)), // #nosec G115
		MaxSeqId:     maxSeqID,
		SessionId:    h.sessionID,
	}), nil
}
