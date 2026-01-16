// Package httpapi provides RBAC permission mappings for the HTTP API endpoint (RFD 031).
package httpapi

import (
	"github.com/coral-mesh/coral/internal/auth"
)

// MethodPermissions maps RPC method paths to required permissions.
// Methods not in this map default to PermissionStatus.
var MethodPermissions = map[string]auth.Permission{
	// Colony status operations (PermissionStatus).
	"/coral.colony.v1.ColonyService/GetStatus":    auth.PermissionStatus,
	"/coral.colony.v1.ColonyService/ListAgents":   auth.PermissionStatus,
	"/coral.colony.v1.ColonyService/GetTopology":  auth.PermissionStatus,
	"/coral.colony.v1.ColonyService/ListServices": auth.PermissionStatus,
	"/coral.colony.v1.ColonyService/ListTools":    auth.PermissionStatus,

	// Query operations (PermissionQuery).
	"/coral.colony.v1.ColonyService/QueryUnifiedSummary": auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/QueryUnifiedTraces":  auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/QueryUnifiedMetrics": auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/QueryUnifiedLogs":    auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/GetMetricPercentile": auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/GetServiceActivity":  auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/ListServiceActivity": auth.PermissionQuery,
	"/coral.colony.v1.ColonyService/ExecuteQuery":        auth.PermissionQuery,

	// MCP tool operations (PermissionAnalyze by default, may vary by tool).
	"/coral.colony.v1.ColonyService/CallTool":   auth.PermissionAnalyze,
	"/coral.colony.v1.ColonyService/StreamTool": auth.PermissionAnalyze,

	// Debug operations (PermissionDebug).
	"/coral.colony.v1.ColonyDebugService/StartSession":  auth.PermissionDebug,
	"/coral.colony.v1.ColonyDebugService/StopSession":   auth.PermissionDebug,
	"/coral.colony.v1.ColonyDebugService/AttachProbe":   auth.PermissionDebug,
	"/coral.colony.v1.ColonyDebugService/DetachProbe":   auth.PermissionDebug,
	"/coral.colony.v1.ColonyDebugService/GetResults":    auth.PermissionQuery,
	"/coral.colony.v1.ColonyDebugService/ListSessions":  auth.PermissionQuery,
	"/coral.colony.v1.ColonyDebugService/StreamEvents":  auth.PermissionDebug,
	"/coral.colony.v1.ColonyDebugService/ListFunctions": auth.PermissionQuery,

	// Certificate operations (PermissionAdmin).
	"/coral.colony.v1.ColonyService/RequestCertificate": auth.PermissionAdmin,
	"/coral.colony.v1.ColonyService/RevokeCertificate":  auth.PermissionAdmin,
}

// MCPToolPermissions maps MCP tool names to required permissions.
// Tools not in this map default to PermissionAnalyze.
var MCPToolPermissions = map[string]auth.Permission{
	// Status tools (PermissionStatus).
	"coral_list_services": auth.PermissionStatus,
	"coral_get_topology":  auth.PermissionStatus,
	"coral_get_status":    auth.PermissionStatus,

	// Query tools (PermissionQuery).
	"coral_query_summary":       auth.PermissionQuery,
	"coral_query_traces":        auth.PermissionQuery,
	"coral_query_metrics":       auth.PermissionQuery,
	"coral_query_logs":          auth.PermissionQuery,
	"coral_list_debug_sessions": auth.PermissionQuery,
	"coral_get_debug_results":   auth.PermissionQuery,
	"coral_discover_functions":  auth.PermissionQuery,

	// Analyze tools (PermissionAnalyze) - may trigger shell commands.
	"coral_shell_exec":     auth.PermissionAnalyze,
	"coral_container_exec": auth.PermissionAnalyze,

	// Debug tools (PermissionDebug) - attach eBPF probes.
	"coral_attach_uprobe":       auth.PermissionDebug,
	"coral_detach_uprobe":       auth.PermissionDebug,
	"coral_trace_request_path":  auth.PermissionDebug,
	"coral_profile_functions":   auth.PermissionDebug,
	"coral_start_debug_session": auth.PermissionDebug,
	"coral_stop_debug_session":  auth.PermissionDebug,
}

// GetRequiredPermission returns the required permission for a method path.
// Returns PermissionStatus if the method is not explicitly mapped.
func GetRequiredPermission(method string) auth.Permission {
	if perm, ok := MethodPermissions[method]; ok {
		return perm
	}
	return auth.PermissionStatus
}

// GetMCPToolPermission returns the required permission for an MCP tool.
// Returns PermissionAnalyze if the tool is not explicitly mapped.
func GetMCPToolPermission(toolName string) auth.Permission {
	if perm, ok := MCPToolPermissions[toolName]; ok {
		return perm
	}
	return auth.PermissionAnalyze
}
