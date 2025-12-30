package database

import (
	"time"
)

// ServiceConnection represents a discovered connection between services.
type ServiceConnection struct {
	FromService     string    `duckdb:"from_service,pk"`
	ToService       string    `duckdb:"to_service,pk"`
	Protocol        string    `duckdb:"protocol,pk"`
	FirstObserved   time.Time `duckdb:"first_observed"`
	LastObserved    time.Time `duckdb:"last_observed"`
	ConnectionCount int       `duckdb:"connection_count"`
}
