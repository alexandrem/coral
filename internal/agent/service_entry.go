package agent

// NamingSource records how a ServiceEntry's name was determined.
type NamingSource string

const (
	// NamingSourceAuto means the name came from the ServiceNameAdaptor chain
	// (RFD 104), derived from process metadata without user input.
	NamingSourceAuto NamingSource = "auto"
	// NamingSourceAuthoritative means the name was explicitly provided via
	// ConnectService (e.g. `coral connect` or `coral services watch`).
	NamingSourceAuthoritative NamingSource = "authoritative"
)

// Tier describes how much the agent knows about, and does for, a service.
type Tier int

const (
	// TierObserved (0) means the port was seen via Beyla's OTLP feedback but
	// has no active ServiceMonitor.
	TierObserved Tier = iota
	// TierWatched (1) means the service has a running ServiceMonitor, either
	// because it was explicitly connected with a health endpoint or was
	// promoted from TierObserved by ConnectService.
	TierWatched
)

// ServiceEntry is the agent's single record of truth for a port (RFD 104).
// Auto-discovered and explicitly connected services differ only in which
// fields are populated, not in which map they live.
type ServiceEntry struct {
	Port int32

	// ExePattern identifies a portless process by executable pattern (RFD
	// 111) instead of a listening port. Empty for port-based entries;
	// mutually exclusive with Port being non-zero.
	ExePattern string

	// AutoName is derived by the ServiceNameAdaptor chain on first
	// observation. It is never cleared once set.
	AutoName string
	// AuthoritativeName is set by ConnectService and always takes priority
	// over AutoName once present.
	AuthoritativeName string
	NamingSource      NamingSource

	Tier Tier

	PID        int32
	BinaryPath string
	BinaryHash string

	// Monitor is non-nil only once the service has been watched with a
	// health endpoint (TierWatched).
	Monitor *ServiceMonitor
}

// Name returns the authoritative name if one has been set, otherwise the
// auto-derived name.
func (e *ServiceEntry) Name() string {
	if e.AuthoritativeName != "" {
		return e.AuthoritativeName
	}
	return e.AutoName
}
