// Package httpapi provides tests for RBAC permission mappings (RFD 031).
package httpapi

import (
	"testing"

	"github.com/coral-mesh/coral/internal/auth"
)

func TestGetRequiredPermission(t *testing.T) {
	tests := []struct {
		method string
		want   auth.Permission
	}{
		// Status operations.
		{"/coral.colony.v1.ColonyService/GetStatus", auth.PermissionStatus},
		{"/coral.colony.v1.ColonyService/ListAgents", auth.PermissionStatus},
		{"/coral.colony.v1.ColonyService/GetTopology", auth.PermissionStatus},
		{"/coral.colony.v1.ColonyService/ListServices", auth.PermissionStatus},
		{"/coral.colony.v1.ColonyService/ListTools", auth.PermissionStatus},

		// Query operations.
		{"/coral.colony.v1.ColonyService/QueryUnifiedSummary", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyService/QueryUnifiedTraces", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyService/QueryUnifiedMetrics", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyService/QueryUnifiedLogs", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyService/ExecuteQuery", auth.PermissionQuery},

		// Analyze operations.
		{"/coral.colony.v1.ColonyService/CallTool", auth.PermissionAnalyze},
		{"/coral.colony.v1.ColonyService/StreamTool", auth.PermissionAnalyze},

		// Debug operations.
		{"/coral.colony.v1.ColonyDebugService/StartSession", auth.PermissionDebug},
		{"/coral.colony.v1.ColonyDebugService/StopSession", auth.PermissionDebug},
		{"/coral.colony.v1.ColonyDebugService/AttachProbe", auth.PermissionDebug},
		{"/coral.colony.v1.ColonyDebugService/DetachProbe", auth.PermissionDebug},
		{"/coral.colony.v1.ColonyDebugService/GetResults", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyDebugService/ListSessions", auth.PermissionQuery},
		{"/coral.colony.v1.ColonyDebugService/StreamEvents", auth.PermissionDebug},

		// Admin operations.
		{"/coral.colony.v1.ColonyService/RequestCertificate", auth.PermissionAdmin},
		{"/coral.colony.v1.ColonyService/RevokeCertificate", auth.PermissionAdmin},

		// Unknown methods default to Status.
		{"/coral.colony.v1.ColonyService/UnknownMethod", auth.PermissionStatus},
		{"/some.other.Service/Method", auth.PermissionStatus},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := GetRequiredPermission(tt.method)
			if got != tt.want {
				t.Errorf("GetRequiredPermission(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}

func TestGetMCPToolPermission(t *testing.T) {
	tests := []struct {
		toolName string
		want     auth.Permission
	}{
		// Status tools.
		{"coral_list_services", auth.PermissionStatus},
		{"coral_get_topology", auth.PermissionStatus},
		{"coral_get_status", auth.PermissionStatus},

		// Query tools.
		{"coral_query_summary", auth.PermissionQuery},
		{"coral_query_traces", auth.PermissionQuery},
		{"coral_query_metrics", auth.PermissionQuery},
		{"coral_query_logs", auth.PermissionQuery},
		{"coral_list_debug_sessions", auth.PermissionQuery},
		{"coral_get_debug_results", auth.PermissionQuery},
		{"coral_discover_functions", auth.PermissionQuery},

		// Analyze tools.
		{"coral_shell_exec", auth.PermissionAnalyze},
		{"coral_container_exec", auth.PermissionAnalyze},

		// Debug tools.
		{"coral_attach_uprobe", auth.PermissionDebug},
		{"coral_detach_uprobe", auth.PermissionDebug},
		{"coral_trace_request_path", auth.PermissionDebug},
		{"coral_profile_functions", auth.PermissionDebug},
		{"coral_start_debug_session", auth.PermissionDebug},
		{"coral_stop_debug_session", auth.PermissionDebug},

		// Unknown tools default to Analyze.
		{"coral_unknown_tool", auth.PermissionAnalyze},
		{"some_custom_tool", auth.PermissionAnalyze},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := GetMCPToolPermission(tt.toolName)
			if got != tt.want {
				t.Errorf("GetMCPToolPermission(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestRBACPermissionHierarchy(t *testing.T) {
	// Verify that admin permission grants access to all operations.
	adminToken := &auth.APIToken{
		TokenID:     "admin",
		Permissions: []auth.Permission{auth.PermissionAdmin},
	}

	// Admin should have access to all mapped methods.
	for method := range MethodPermissions {
		required := GetRequiredPermission(method)
		if !auth.HasPermission(adminToken, required) {
			t.Errorf("Admin token should have access to %q (requires %v)", method, required)
		}
	}

	// Admin should have access to all MCP tools.
	for tool := range MCPToolPermissions {
		required := GetMCPToolPermission(tool)
		if !auth.HasPermission(adminToken, required) {
			t.Errorf("Admin token should have access to tool %q (requires %v)", tool, required)
		}
	}
}

func TestRBACMinimalPermissions(t *testing.T) {
	// Token with only status permission.
	statusToken := &auth.APIToken{
		TokenID:     "status-only",
		Permissions: []auth.Permission{auth.PermissionStatus},
	}

	// Should have access to status methods.
	if !auth.HasPermission(statusToken, auth.PermissionStatus) {
		t.Error("Status token should have Status permission")
	}

	// Should not have access to query methods.
	if auth.HasPermission(statusToken, auth.PermissionQuery) {
		t.Error("Status token should not have Query permission")
	}

	// Should not have access to analyze methods.
	if auth.HasPermission(statusToken, auth.PermissionAnalyze) {
		t.Error("Status token should not have Analyze permission")
	}

	// Should not have access to debug methods.
	if auth.HasPermission(statusToken, auth.PermissionDebug) {
		t.Error("Status token should not have Debug permission")
	}

	// Should not have admin access.
	if auth.HasPermission(statusToken, auth.PermissionAdmin) {
		t.Error("Status token should not have Admin permission")
	}
}

func TestRBACCombinedPermissions(t *testing.T) {
	// Token with status and query permissions.
	combinedToken := &auth.APIToken{
		TokenID:     "combined",
		Permissions: []auth.Permission{auth.PermissionStatus, auth.PermissionQuery},
	}

	// Should have access to status methods.
	if !auth.HasPermission(combinedToken, auth.PermissionStatus) {
		t.Error("Combined token should have Status permission")
	}

	// Should have access to query methods.
	if !auth.HasPermission(combinedToken, auth.PermissionQuery) {
		t.Error("Combined token should have Query permission")
	}

	// Should not have access to analyze methods.
	if auth.HasPermission(combinedToken, auth.PermissionAnalyze) {
		t.Error("Combined token should not have Analyze permission")
	}

	// Should not have access to debug methods.
	if auth.HasPermission(combinedToken, auth.PermissionDebug) {
		t.Error("Combined token should not have Debug permission")
	}
}
