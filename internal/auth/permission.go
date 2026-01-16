package auth

// Permission defines access levels for API tokens.
type Permission string

const (
	// PermissionStatus allows reading colony status, agents, and topology.
	PermissionStatus Permission = "status"

	// PermissionQuery allows querying metrics, traces, and logs.
	PermissionQuery Permission = "query"

	// PermissionAnalyze allows AI analysis which may trigger shell commands or probes.
	PermissionAnalyze Permission = "analyze"

	// PermissionDebug allows attaching live eBPF probes and debugging.
	PermissionDebug Permission = "debug"

	// PermissionAdmin grants full administrative access including config changes.
	PermissionAdmin Permission = "admin"
)

// AllPermissions returns all defined permissions.
func AllPermissions() []Permission {
	return []Permission{
		PermissionStatus,
		PermissionQuery,
		PermissionAnalyze,
		PermissionDebug,
		PermissionAdmin,
	}
}

// ParsePermission converts a string to a Permission.
// Returns empty string if the permission is invalid.
func ParsePermission(s string) Permission {
	switch s {
	case "status":
		return PermissionStatus
	case "query":
		return PermissionQuery
	case "analyze":
		return PermissionAnalyze
	case "debug":
		return PermissionDebug
	case "admin":
		return PermissionAdmin
	default:
		return ""
	}
}
