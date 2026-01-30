package river

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lirancohen/skene/event"
)

// Default configuration values.
const (
	// DefaultWorkers is the default number of worker goroutines.
	// Use -1 to auto-detect (runtime.NumCPU()), 0 for insert-only mode.
	DefaultWorkers = -1

	// DefaultJobTimeout is the default timeout for job execution.
	DefaultJobTimeout = 30 * time.Second

	// DefaultShutdownTimeout is the default timeout for graceful shutdown.
	DefaultShutdownTimeout = 30 * time.Second
)

// Logger defines the logging interface for the runner.
// Implementations should be safe for concurrent use.
type Logger interface {
	// Debug logs a debug message with optional key-value pairs.
	Debug(msg string, keysAndValues ...any)

	// Info logs an informational message with optional key-value pairs.
	Info(msg string, keysAndValues ...any)

	// Warn logs a warning message with optional key-value pairs.
	Warn(msg string, keysAndValues ...any)

	// Error logs an error message with optional key-value pairs.
	Error(msg string, keysAndValues ...any)
}

// Config configures the Runner.
type Config struct {
	// Pool is the PostgreSQL connection pool.
	// Required.
	Pool *pgxpool.Pool

	// EventStore is the event persistence layer.
	// Required.
	EventStore event.EventStore

	// Registry contains registered workflow definitions.
	// Required.
	Registry *Registry

	// Logger is the logging interface. If nil, a no-op logger is used.
	Logger Logger

	// Workers is the number of worker goroutines for processing jobs.
	// If zero, runs in insert-only mode (no job processing).
	// If negative, defaults to runtime.NumCPU().
	Workers int

	// JobTimeout is the maximum duration for a single job execution.
	// If zero, defaults to DefaultJobTimeout (30s).
	JobTimeout time.Duration

	// ShutdownTimeout is the maximum duration to wait for graceful shutdown.
	// If zero, defaults to DefaultShutdownTimeout (30s).
	ShutdownTimeout time.Duration
}

// Validate checks that the configuration is valid.
// Returns an error if any required fields are missing or invalid.
func (c *Config) Validate() error {
	if c.Pool == nil {
		return errors.New("river: Pool is required")
	}
	if c.EventStore == nil {
		return errors.New("river: EventStore is required")
	}
	if c.Registry == nil {
		return errors.New("river: Registry is required")
	}
	return nil
}

// withDefaults returns a copy of the config with default values applied.
// Note: Workers=0 means insert-only mode and is preserved.
func (c *Config) withDefaults() Config {
	cfg := *c

	// Workers=0 means insert-only mode, preserve it
	// Workers<0 means use default (NumCPU)
	if cfg.Workers < 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = DefaultJobTimeout
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = noopLogger{}
	}

	return cfg
}

// noopLogger is a Logger that discards all log messages.
type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// TxEventStore extends EventStore with transaction support.
// This interface is used internally by the runner for atomic operations.
type TxEventStore interface {
	event.EventStore

	// AppendBatchTx appends events within the given transaction.
	AppendBatchTx(ctx context.Context, tx Tx, events []event.Event) error

	// LoadTx loads events within the given transaction.
	LoadTx(ctx context.Context, tx Tx, runID string) ([]event.Event, error)
}

// Tx represents a database transaction.
// This interface allows the runner to work with different transaction types.
type Tx interface {
	// Commit commits the transaction.
	Commit(ctx context.Context) error

	// Rollback aborts the transaction.
	Rollback(ctx context.Context) error
}
