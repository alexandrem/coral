package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

func TestEvidenceLayerLabel(t *testing.T) {
	tests := []struct {
		layer colonypb.EvidenceLayer
		want  string
	}{
		{colonypb.EvidenceLayer_EVIDENCE_LAYER_L7_TRACE, "L7"},
		{colonypb.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK, "L4"},
		{colonypb.EvidenceLayer_EVIDENCE_LAYER_BOTH, "BOTH"},
		{colonypb.EvidenceLayer_EVIDENCE_LAYER_UNSPECIFIED, "L7"}, // legacy default
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, evidenceLayerLabel(tc.layer), "layer=%v", tc.layer)
	}
}

func TestFilterConnections_IncludeL4True(t *testing.T) {
	conns := []*colonypb.Connection{
		{SourceId: "a", TargetId: "b", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_L7_TRACE},
		{SourceId: "a", TargetId: "c", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK},
		{SourceId: "a", TargetId: "d", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_BOTH},
	}
	got := filterConnections(conns, true)
	assert.Len(t, got, 3, "all connections returned when includeL4=true")
}

func TestFilterConnections_IncludeL4False(t *testing.T) {
	conns := []*colonypb.Connection{
		{SourceId: "a", TargetId: "b", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_L7_TRACE},
		{SourceId: "a", TargetId: "c", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK},
		{SourceId: "a", TargetId: "d", EvidenceLayer: colonypb.EvidenceLayer_EVIDENCE_LAYER_BOTH},
	}
	got := filterConnections(conns, false)
	require.Len(t, got, 2, "L4-only connections are filtered out when includeL4=false")
	assert.Equal(t, "b", got[0].TargetId)
	assert.Equal(t, "d", got[1].TargetId)
}

func TestFilterConnections_Empty(t *testing.T) {
	assert.Empty(t, filterConnections(nil, false))
	assert.Empty(t, filterConnections(nil, true))
}
