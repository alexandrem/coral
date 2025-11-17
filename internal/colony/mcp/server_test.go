package mcp

import (
	"testing"
	"time"

	"github.com/coral-io/coral/internal/logging"
	"github.com/stretchr/testify/assert"
)

func TestConfig(t *testing.T) {
	// Test that MCP config structure is valid.
	config := Config{
		ColonyID:        "test-colony",
		ApplicationName: "test-app",
		Environment:     "test",
		Disabled:        false,
		EnabledTools:    []string{"coral_get_service_health"},
		AuditEnabled:    true,
	}

	assert.Equal(t, "test-colony", config.ColonyID)
	assert.Equal(t, "test-app", config.ApplicationName)
	assert.Equal(t, "test", config.Environment)
	assert.False(t, config.Disabled)
	assert.True(t, config.AuditEnabled)
	assert.Len(t, config.EnabledTools, 1)
}

func TestIsToolEnabled(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	tests := []struct {
		name         string
		enabledTools []string
		toolName     string
		want         bool
	}{
		{
			name:         "empty list enables all tools",
			enabledTools: []string{},
			toolName:     "coral_get_service_health",
			want:         true,
		},
		{
			name:         "specific tool is enabled",
			enabledTools: []string{"coral_get_service_health"},
			toolName:     "coral_get_service_health",
			want:         true,
		},
		{
			name:         "specific tool is not enabled",
			enabledTools: []string{"coral_get_service_health"},
			toolName:     "coral_query_events",
			want:         false,
		},
		{
			name:         "multiple tools",
			enabledTools: []string{"coral_get_service_health", "coral_query_events"},
			toolName:     "coral_query_events",
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				config: Config{
					EnabledTools: tt.enabledTools,
				},
				logger: logger,
			}
			got := s.isToolEnabled(tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestListToolNames(t *testing.T) {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error",
		Pretty: false,
	}, "mcp-test")

	s := &Server{
		config: Config{},
		logger: logger,
	}

	tools := s.listToolNames()
	assert.NotEmpty(t, tools)
	assert.Contains(t, tools, "coral_get_service_health")
	assert.Contains(t, tools, "coral_query_beyla_http_metrics")
	assert.Contains(t, tools, "coral_get_service_topology")
}

func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		pattern string
		want    bool
	}{
		{
			name:    "exact match",
			s:       "api",
			pattern: "api",
			want:    true,
		},
		{
			name:    "wildcard all",
			s:       "anything",
			pattern: "*",
			want:    true,
		},
		{
			name:    "prefix match",
			s:       "api-service",
			pattern: "api*",
			want:    true,
		},
		{
			name:    "suffix match",
			s:       "my-api",
			pattern: "*api",
			want:    true,
		},
		{
			name:    "no match",
			s:       "database",
			pattern: "api*",
			want:    false,
		},
		{
			name:    "empty pattern matches all",
			s:       "anything",
			pattern: "",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPattern(tt.s, tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "seconds only",
			duration: 45 * time.Second,
			want:     "45s",
		},
		{
			name:     "minutes",
			duration: 2*time.Minute + 30*time.Second,
			want:     "2m",
		},
		{
			name:     "hours",
			duration: 3*time.Hour + 25*time.Minute,
			want:     "3h",
		},
		{
			name:     "days",
			duration: 2*24*time.Hour + 5*time.Hour,
			want:     "2d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			assert.Equal(t, tt.want, got)
		})
	}
}
