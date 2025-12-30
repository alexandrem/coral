package startup

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/marcboeker/go-duckdb"

	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/agent/beyla"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
)

// StorageResult contains the results of storage initialization.
type StorageResult struct {
	SharedDB      *sql.DB
	SharedDBPath  string
	BeylaConfig   *beyla.Config
	FunctionCache *agent.FunctionCache
}

// StorageManager handles DuckDB initialization and database setup.
type StorageManager struct {
	logger       logging.Logger
	agentCfg     *config.AgentConfig
	serviceSpecs []*types.ServiceSpec
	monitorAll   bool
	agentID      string
}

// NewStorageManager creates a new storage manager.
func NewStorageManager(
	logger logging.Logger,
	agentCfg *config.AgentConfig,
	serviceSpecs []*types.ServiceSpec,
	monitorAll bool,
	agentID string,
) *StorageManager {
	return &StorageManager{
		logger:       logger,
		agentCfg:     agentCfg,
		serviceSpecs: serviceSpecs,
		monitorAll:   monitorAll,
		agentID:      agentID,
	}
}

// Initialize creates and initializes shared DuckDB database.
func (s *StorageManager) Initialize() (*StorageResult, error) {
	result := &StorageResult{}

	// Create shared DuckDB database for all agent data (telemetry + Beyla + custom).
	var sharedDB *sql.DB
	var sharedDBPath string
	homeDir, err := os.UserHomeDir()
	if err == nil {
		// Create parent directories if they don't exist.
		dbDir := homeDir + "/.coral/agent"
		if err := os.MkdirAll(dbDir, 0750); err != nil {
			s.logger.Warn().Err(err).Msg("Failed to create agent directory - using in-memory storage")
		} else {
			sharedDBPath = dbDir + "/metrics.duckdb"
			sharedDB, err = sql.Open("duckdb", sharedDBPath)
			if err != nil {
				s.logger.Warn().Err(err).Msg("Failed to create shared metrics database - using in-memory storage")
				sharedDB = nil
				sharedDBPath = ""
			} else {
				s.logger.Info().
					Str("db_path", sharedDBPath).
					Msg("Initialized shared metrics database")
			}
		}
	} else {
		s.logger.Warn().Err(err).Msg("Failed to get user home directory - using in-memory storage")
	}

	result.SharedDB = sharedDB
	result.SharedDBPath = sharedDBPath

	// Initialize Beyla configuration (RFD 032 + RFD 053).
	var beylaConfig *beyla.Config
	if sharedDB != nil && !s.agentCfg.Beyla.Disabled {
		// Check if we have any services to monitor (configured, dynamic, or monitor-all).
		hasConfiguredServices := len(s.agentCfg.Beyla.Discovery.Services) > 0
		hasDynamicServices := len(s.serviceSpecs) > 0

		if s.monitorAll || hasConfiguredServices || hasDynamicServices {
			s.logger.Info().Msg("Initializing Beyla configuration")

			// Convert config.BeylaConfig to beyla.Config.
			beylaConfig = &beyla.Config{
				Enabled:      true,
				OTLPEndpoint: s.agentCfg.Beyla.OTLPEndpoint,
				Protocols: beyla.ProtocolsConfig{
					HTTPEnabled:  s.agentCfg.Beyla.Protocols.HTTP.Enabled,
					GRPCEnabled:  s.agentCfg.Beyla.Protocols.GRPC.Enabled,
					SQLEnabled:   s.agentCfg.Beyla.Protocols.SQL.Enabled,
					KafkaEnabled: s.agentCfg.Beyla.Protocols.Kafka.Enabled,
					RedisEnabled: s.agentCfg.Beyla.Protocols.Redis.Enabled,
				},
				Attributes:            s.agentCfg.Beyla.Attributes,
				SamplingRate:          s.agentCfg.Beyla.Sampling.Rate,
				DB:                    sharedDB,
				DBPath:                sharedDBPath,
				StorageRetentionHours: 1, // Default: 1 hour (TODO: make configurable)
				MonitorAll:            s.monitorAll,
			}

			// Add configured services from config file to discovery.
			for _, svc := range s.agentCfg.Beyla.Discovery.Services {
				if svc.OpenPort > 0 {
					beylaConfig.Discovery.OpenPorts = append(beylaConfig.Discovery.OpenPorts, svc.OpenPort)
				}
				// TODO: Support K8s discovery mapping when available in beyla.DiscoveryConfig
			}

			// Add dynamic ports from services (RFD 053).
			for _, spec := range s.serviceSpecs {
				beylaConfig.Discovery.OpenPorts = append(beylaConfig.Discovery.OpenPorts, int(spec.Port))
			}

			if s.monitorAll {
				s.logger.Info().Msg("Monitor-all mode enabled - Beyla will instrument all listening processes")
			}
		} else {
			s.logger.Info().Msg("No services configured - Beyla will not start (use --monitor-all or --connect to enable)")
		}
	} else if s.agentCfg.Beyla.Disabled {
		s.logger.Info().Msg("Beyla explicitly disabled in configuration")
	}

	result.BeylaConfig = beylaConfig

	// Create function cache with agent's DuckDB (RFD 063).
	if sharedDB != nil {
		functionCache, err := agent.NewFunctionCache(sharedDB, s.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create function cache: %w", err)
		}
		result.FunctionCache = functionCache
	}

	return result, nil
}
