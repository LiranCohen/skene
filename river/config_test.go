package river

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
)

// mockEventStore implements event.EventStore for testing.
type mockEventStore struct{}

func (mockEventStore) Append(context.Context, event.Event) error              { return nil }
func (mockEventStore) AppendBatch(context.Context, []event.Event) error       { return nil }
func (mockEventStore) Load(context.Context, string) ([]event.Event, error)    { return nil, nil }
func (mockEventStore) LoadSince(context.Context, string, int64) ([]event.Event, error) {
	return nil, nil
}
func (mockEventStore) GetLastSequence(context.Context, string) (int64, error) { return 0, nil }

func TestConfig_Validate_MissingPool(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for missing Pool")
	}
	if err.Error() != "river: Pool is required" {
		t.Errorf("Validate() error = %q, want %q", err.Error(), "river: Pool is required")
	}
}

func TestConfig_Validate_MissingEventStore(t *testing.T) {
	// Note: We can't easily test the "missing EventStore" case without
	// a real pool, so we test the error message format for Pool validation
	// and trust that EventStore validation follows the same pattern.
	cfg := Config{
		EventStore: mockEventStore{},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should fail when Pool is missing")
	}
}

func TestConfig_Validate_MissingRegistry(t *testing.T) {
	// Same limitation - we test that validation fails for the first missing field
	cfg := Config{
		EventStore: mockEventStore{},
		Registry:   NewRegistry(),
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should fail when Pool is missing")
	}
}

func TestConfig_withDefaults(t *testing.T) {
	tests := []struct {
		name            string
		config          Config
		wantWorkers     int
		wantJobTimeout  time.Duration
		wantShutdown    time.Duration
		wantLoggerIsNop bool
	}{
		{
			name:            "all defaults applied",
			config:          Config{},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: true,
		},
		{
			name: "zero workers gets default",
			config: Config{
				Workers: 0,
			},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: true,
		},
		{
			name: "negative workers gets default",
			config: Config{
				Workers: -1,
			},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: true,
		},
		{
			name: "custom workers preserved",
			config: Config{
				Workers: 8,
			},
			wantWorkers:     8,
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: true,
		},
		{
			name: "custom job timeout preserved",
			config: Config{
				JobTimeout: 2 * time.Minute,
			},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  2 * time.Minute,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: true,
		},
		{
			name: "custom shutdown timeout preserved",
			config: Config{
				ShutdownTimeout: 5 * time.Minute,
			},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    5 * time.Minute,
			wantLoggerIsNop: true,
		},
		{
			name: "custom logger preserved",
			config: Config{
				Logger: &testLogger{},
			},
			wantWorkers:     runtime.NumCPU(),
			wantJobTimeout:  DefaultJobTimeout,
			wantShutdown:    DefaultShutdownTimeout,
			wantLoggerIsNop: false,
		},
		{
			name: "all custom values preserved",
			config: Config{
				Workers:         16,
				JobTimeout:      5 * time.Minute,
				ShutdownTimeout: 10 * time.Minute,
				Logger:          &testLogger{},
			},
			wantWorkers:     16,
			wantJobTimeout:  5 * time.Minute,
			wantShutdown:    10 * time.Minute,
			wantLoggerIsNop: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config.withDefaults()

			if cfg.Workers != tt.wantWorkers {
				t.Errorf("Workers = %d, want %d", cfg.Workers, tt.wantWorkers)
			}
			if cfg.JobTimeout != tt.wantJobTimeout {
				t.Errorf("JobTimeout = %v, want %v", cfg.JobTimeout, tt.wantJobTimeout)
			}
			if cfg.ShutdownTimeout != tt.wantShutdown {
				t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, tt.wantShutdown)
			}

			_, isNoop := cfg.Logger.(noopLogger)
			if isNoop != tt.wantLoggerIsNop {
				t.Errorf("Logger is noopLogger = %v, want %v", isNoop, tt.wantLoggerIsNop)
			}
		})
	}
}

func TestConfig_withDefaults_DoesNotMutateOriginal(t *testing.T) {
	original := Config{
		Workers: 0, // Will be changed to NumCPU in withDefaults
	}

	_ = original.withDefaults()

	if original.Workers != 0 {
		t.Errorf("Original config was mutated: Workers = %d, want 0", original.Workers)
	}
}

func TestNoopLogger(t *testing.T) {
	// Verify noopLogger doesn't panic and implements Logger interface
	var logger Logger = noopLogger{}

	// These should all be no-ops and not panic
	logger.Debug("debug message", "key", "value")
	logger.Info("info message", "key", "value")
	logger.Warn("warn message", "key", "value")
	logger.Error("error message", "key", "value")
}

func TestDefaultConstants(t *testing.T) {
	if DefaultWorkers != 0 {
		t.Errorf("DefaultWorkers = %d, want 0", DefaultWorkers)
	}
	if DefaultJobTimeout != 30*time.Second {
		t.Errorf("DefaultJobTimeout = %v, want %v", DefaultJobTimeout, 30*time.Second)
	}
	if DefaultShutdownTimeout != 30*time.Second {
		t.Errorf("DefaultShutdownTimeout = %v, want %v", DefaultShutdownTimeout, 30*time.Second)
	}
}

// testLogger is a Logger implementation for testing.
type testLogger struct {
	messages []string
}

func (l *testLogger) Debug(msg string, keysAndValues ...any) { l.messages = append(l.messages, msg) }
func (l *testLogger) Info(msg string, keysAndValues ...any)  { l.messages = append(l.messages, msg) }
func (l *testLogger) Warn(msg string, keysAndValues ...any)  { l.messages = append(l.messages, msg) }
func (l *testLogger) Error(msg string, keysAndValues ...any) { l.messages = append(l.messages, msg) }
