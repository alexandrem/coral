package netobs

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// defaultPollInterval is how often the poller queries active connections.
	defaultPollInterval = 30 * time.Second
)

// URLProvider is a function that returns the current colony base URL.
// It returns an empty string if the colony is not yet reachable.
// The Manager calls this before each send so it picks up reconnection events.
type URLProvider func() string

// Manager observes outbound TCP connections and streams aggregated batches to
// the colony. It implements the agent.Lifecycle interface (Start/Stop).
type Manager struct {
	agentID      string
	urlProvider  URLProvider
	pollInterval time.Duration
	logger       zerolog.Logger

	aggregator *Aggregator
	poller     *poller
	streamer   *streamer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Config holds configuration for the netobs Manager.
type Config struct {
	// AgentID is the reporting agent identifier (required).
	AgentID string

	// URLProvider returns the colony base URL on demand.
	// The manager gracefully skips sends when this returns "".
	URLProvider URLProvider

	// PollInterval controls how often connections are sampled (default: 30s).
	PollInterval time.Duration

	// Logger is the zerolog logger for this component.
	Logger zerolog.Logger
}

// NewManager creates a new netobs Manager. Call Start to begin observation.
func NewManager(cfg Config) *Manager {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}

	return &Manager{
		agentID:      cfg.AgentID,
		urlProvider:  cfg.URLProvider,
		pollInterval: cfg.PollInterval,
		logger:       cfg.Logger.With().Str("component", "netobs").Logger(),
		aggregator:   newAggregator(),
		poller:       newPoller(cfg.Logger),
		streamer:     nil, // Created lazily in the run loop once a URL is available.
	}
}

// Start begins the poll-aggregate-stream loop. It returns immediately; the loop
// runs in a background goroutine until Stop is called.
func (m *Manager) Start() error {
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.wg.Add(1)
	go m.run()
	m.logger.Info().
		Dur("poll_interval", m.pollInterval).
		Msg("Network observation started")
	return nil
}

// Stop signals the background goroutine to exit and waits for it to finish.
func (m *Manager) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.logger.Info().Msg("Network observation stopped")
	return nil
}

// run is the main poll-aggregate-stream loop.
func (m *Manager) run() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.tick()
		}
	}
}

// tick performs one poll cycle: collect → aggregate → flush & send.
func (m *Manager) tick() {
	entries, err := m.poller.Poll(m.ctx)
	if err != nil {
		if m.ctx.Err() == nil {
			m.logger.Warn().Err(err).Msg("Failed to poll network connections")
		}
		return
	}

	for _, e := range entries {
		m.aggregator.Record(e)
	}

	flushed := m.aggregator.Flush()
	if len(flushed) == 0 {
		return
	}

	colonyURL := ""
	if m.urlProvider != nil {
		colonyURL = m.urlProvider()
	}

	if colonyURL == "" {
		m.logger.Debug().Msg("Colony URL not yet available, skipping connection report")
		return
	}

	// Create or recreate the streamer if the URL changed.
	if m.streamer == nil || m.streamer.colonyURL != colonyURL {
		m.streamer = newStreamer(m.agentID, colonyURL, newColonyHTTPClient(), m.logger)
	}

	if err := m.streamer.Send(m.ctx, flushed); err != nil {
		if m.ctx.Err() == nil {
			m.logger.Warn().
				Err(err).
				Int("entries", len(flushed)).
				Msg("Failed to report connections to colony")
		}
	}
}
