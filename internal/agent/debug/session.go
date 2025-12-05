package debug

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	coreebpf "github.com/coral-mesh/coral/internal/agent/ebpf"
	"github.com/coral-mesh/coral/internal/agent/ebpf/uprobe"
	"github.com/coral-mesh/coral/internal/config"
)

// ServiceResolver resolves service name to address.
type ServiceResolver interface {
	Resolve(serviceName string) (string, error)
	ResolveSDK(serviceName string) (string, error)
}

// DebugSession represents an active debug session.
type DebugSession struct {
	ID        string
	PID       int
	Function  string
	StartTime time.Time

	// eBPF resources
	AttachResult *uprobe.AttachResult
	BPFObjects   *uprobe_monitorObjects // BPF programs and maps
	Reader       *ringbuf.Reader        // Cached from AttachResult for convenience
}

// SessionManager manages active debug sessions.
type SessionManager struct {
	cfg      config.DebugConfig
	logger   zerolog.Logger
	sessions map[string]*DebugSession
	resolver ServiceResolver
	eventCh  chan *agentv1.DebugEvent
	mu       sync.RWMutex
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(cfg config.DebugConfig, logger zerolog.Logger, resolver ServiceResolver) *SessionManager {
	return &SessionManager{
		cfg:      cfg,
		logger:   logger,
		sessions: make(map[string]*DebugSession),
		resolver: resolver,
		eventCh:  make(chan *agentv1.DebugEvent, 1000), // Buffer events
	}
}

// Subscribe returns a read-only channel of debug events.
func (m *SessionManager) Subscribe() <-chan *agentv1.DebugEvent {
	return m.eventCh
}

// StartSession starts a new debug session for a service function.
func (m *SessionManager) StartSession(sessionID string, serviceName string, functionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.cfg.Enabled {
		return fmt.Errorf("debug feature is disabled")
	}

	if len(m.sessions) >= m.cfg.Limits.MaxConcurrentSessions {
		return fmt.Errorf("max concurrent sessions reached (%d)", m.cfg.Limits.MaxConcurrentSessions)
	}

	if _, exists := m.sessions[sessionID]; exists {
		return fmt.Errorf("session already exists: %s", sessionID)
	}

	// 1. Resolve SDK address
	addr, err := m.resolver.ResolveSDK(serviceName)
	if err != nil {
		return fmt.Errorf("resolve sdk %s: %w", serviceName, err)
	}

	// 2. Query SDK
	sdkClient := coreebpf.NewSDKClient(m.logger, addr)
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.SDKAPI.Timeout)
	defer cancel()

	metadata, err := sdkClient.GetFunctionMetadata(ctx, functionName)
	if err != nil {
		return fmt.Errorf("get function metadata: %w", err)
	}

	// 3. Create session object
	session := &DebugSession{
		ID:        sessionID,
		PID:       int(metadata.Pid),
		Function:  functionName,
		StartTime: time.Now(),
	}

	// Let's call a private method attachUprobeLocked
	if err := m.attachUprobeLocked(session, int(metadata.Pid), metadata.BinaryPath, metadata.Offset); err != nil {
		return fmt.Errorf("attach uprobe: %w", err)
	}

	m.sessions[sessionID] = session
	m.logger.Info().
		Str("session_id", sessionID).
		Str("service", serviceName).
		Str("function", functionName).
		Int("pid", int(metadata.Pid)).
		Msg("Debug session started")

	// Schedule auto-detach
	time.AfterFunc(m.cfg.Limits.MaxSessionDuration, func() {
		if err := m.CloseSession(sessionID); err != nil {
			m.logger.Warn().Err(fmt.Errorf("unable to close session %s: %w", sessionID, err))
		}
	})

	return nil
}

// CreateSession is deprecated, use StartSession.
func (m *SessionManager) CreateSession(sessionID string, pid int, function string) error {
	return fmt.Errorf("use StartSession instead")
}

// GetSession retrieves a debug session by ID.
func (m *SessionManager) GetSession(sessionID string) (*DebugSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[sessionID]
	return session, ok
}

// CloseSession closes a debug session.
func (m *SessionManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Detach eBPF probes
	if err := m.DetachUprobe(sessionID); err != nil {
		m.logger.Error().Err(err).Str("session_id", sessionID).Msg("Failed to detach uprobe")
		// Continue cleanup anyway
	}

	delete(m.sessions, sessionID)
	m.logger.Info().Str("session_id", sessionID).Msg("Debug session closed")
	return nil
}
