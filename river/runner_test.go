//go:build integration

package river_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lirancohen/skene/event/pgstore"
	"github.com/lirancohen/skene/river"
	"github.com/lirancohen/skene/workflow"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// testLogger implements river.Logger for tests.
type testLogger struct {
	t *testing.T
}

func (l *testLogger) Debug(msg string, keysAndValues ...any) {
	l.t.Helper()
	l.t.Logf("DEBUG: %s %v", msg, keysAndValues)
}

func (l *testLogger) Info(msg string, keysAndValues ...any) {
	l.t.Helper()
	l.t.Logf("INFO: %s %v", msg, keysAndValues)
}

func (l *testLogger) Warn(msg string, keysAndValues ...any) {
	l.t.Helper()
	l.t.Logf("WARN: %s %v", msg, keysAndValues)
}

func (l *testLogger) Error(msg string, keysAndValues ...any) {
	l.t.Helper()
	l.t.Logf("ERROR: %s %v", msg, keysAndValues)
}

// setupTestDB creates a PostgreSQL container and returns a connection pool.
func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("skene_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to create pool: %v", err)
	}

	// Run River migrations
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		pool.Close()
		container.Terminate(ctx)
		t.Fatalf("failed to create River migrator: %v", err)
	}

	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		pool.Close()
		container.Terminate(ctx)
		t.Fatalf("failed to run River migrations: %v", err)
	}

	// Create skene_events table
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS skene_events (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			sequence BIGINT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			type TEXT NOT NULL,
			step_name TEXT,
			data JSONB,
			output JSONB,
			metadata JSONB,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT skene_events_run_sequence UNIQUE (run_id, sequence)
		);
		CREATE INDEX IF NOT EXISTS idx_skene_events_run_id ON skene_events (run_id, sequence);
	`)
	if err != nil {
		pool.Close()
		container.Terminate(ctx)
		t.Fatalf("failed to create events table: %v", err)
	}

	cleanup := func() {
		pool.Close()
		container.Terminate(ctx)
	}

	return pool, cleanup
}

// Simple workflow for testing: greet -> farewell
type GreetInput struct {
	Name string `json:"name"`
}

type GreetOutput struct {
	Greeting string `json:"greeting"`
}

type FarewellOutput struct {
	Message string `json:"message"`
}

var greetStep = workflow.NewStep("greet", func(ctx workflow.Context) (GreetOutput, error) {
	input := workflow.Input[GreetInput](ctx)
	return GreetOutput{Greeting: "Hello, " + input.Name}, nil
})

var farewellStep = workflow.NewStep("farewell", func(ctx workflow.Context) (FarewellOutput, error) {
	greet := greetStep.Output(ctx)
	return FarewellOutput{Message: greet.Greeting + ". Goodbye!"}, nil
})

var greetWorkflow = workflow.Define("greet-workflow",
	greetStep.After(),
	farewellStep.After(greetStep),
)

// Failing workflow for testing error handling
type FailInput struct {
	ShouldFail bool `json:"should_fail"`
}

type FailOutput struct {
	Result string `json:"result"`
}

var failStep = workflow.NewStep("fail", func(ctx workflow.Context) (FailOutput, error) {
	input := workflow.Input[FailInput](ctx)
	if input.ShouldFail {
		return FailOutput{}, errors.New("intentional failure")
	}
	return FailOutput{Result: "success"}, nil
})

var failWorkflow = workflow.Define("fail-workflow",
	failStep.After(),
)

func TestRunner_Lifecycle(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	tests := []struct {
		name    string
		setup   func(*river.Config)
		wantErr bool
	}{
		{
			name:    "start and stop",
			setup:   func(c *river.Config) {},
			wantErr: false,
		},
		{
			name: "start with custom workers",
			setup: func(c *river.Config) {
				c.Workers = 2
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := river.Config{
				Pool:       pool,
				EventStore: eventStore,
				Registry:   registry,
				Logger:     &testLogger{t: t},
			}
			tt.setup(&config)

			r, err := river.NewRunner(config)
			if err != nil {
				t.Fatalf("NewRunner() error = %v", err)
			}

			ctx := context.Background()

			err = r.Start(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("Start() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				err = r.Stop(ctx)
				if err != nil {
					t.Errorf("Stop() error = %v", err)
				}
			}
		})
	}
}

func TestRunner_StartWorkflow(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer r.Stop(ctx)

	tests := []struct {
		name         string
		workflowName string
		input        any
		wantErr      bool
	}{
		{
			name:         "valid workflow",
			workflowName: "greet-workflow",
			input:        GreetInput{Name: "Test"},
			wantErr:      false,
		},
		{
			name:         "unknown workflow",
			workflowName: "unknown-workflow",
			input:        nil,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, _ := json.Marshal(tt.input)
			runID, err := r.StartWorkflow(ctx, tt.workflowName, inputJSON)

			if (err != nil) != tt.wantErr {
				t.Errorf("StartWorkflow() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if runID == "" {
					t.Error("StartWorkflow() returned empty runID")
				}

				// Verify run exists
				run, err := r.GetRun(ctx, runID)
				if err != nil {
					t.Errorf("GetRun() error = %v", err)
					return
				}
				if run.WorkflowName != tt.workflowName {
					t.Errorf("Run.WorkflowName = %v, want %v", run.WorkflowName, tt.workflowName)
				}
			}
		})
	}
}

func TestRunner_WorkflowCompletion(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
		Workers:    2,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer r.Stop(ctx)

	// Start workflow
	input := GreetInput{Name: "Integration Test"}
	inputJSON, _ := json.Marshal(input)
	runID, err := r.StartWorkflow(ctx, "greet-workflow", inputJSON)
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}

	// Wait for completion (poll with timeout)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := r.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}

		if run.Status == river.RunStatusCompleted {
			// Verify events
			history, err := r.GetHistory(ctx, runID)
			if err != nil {
				t.Fatalf("GetHistory() error = %v", err)
			}
			if len(history) < 3 {
				t.Errorf("Expected at least 3 events, got %d", len(history))
			}
			return
		}

		if run.Status == river.RunStatusFailed {
			t.Fatalf("Workflow failed: %s", run.Error)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("Workflow did not complete within timeout")
}

func TestRunner_WorkflowFailure(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(failWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
		Workers:    2,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer r.Stop(ctx)

	// Start workflow that will fail
	input := FailInput{ShouldFail: true}
	inputJSON, _ := json.Marshal(input)
	runID, err := r.StartWorkflow(ctx, "fail-workflow", inputJSON)
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}

	// Wait for failure (poll with timeout)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := r.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}

		if run.Status == river.RunStatusFailed {
			if run.Error == "" {
				t.Error("Expected error message but got empty string")
			}
			return
		}

		if run.Status == river.RunStatusCompleted {
			t.Fatal("Expected workflow to fail but it completed")
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("Workflow did not fail within timeout")
}

func TestRunner_CancelWorkflow(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
		Workers:    1,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer r.Stop(ctx)

	// Start workflow
	input := GreetInput{Name: "Cancel Test"}
	inputJSON, _ := json.Marshal(input)
	runID, err := r.StartWorkflow(ctx, "greet-workflow", inputJSON)
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}

	// Cancel immediately (may or may not succeed depending on timing)
	reason := "user requested cancellation"
	err = r.CancelWorkflow(ctx, runID, reason)
	if err != nil && err != river.ErrInvalidRunStatus {
		// ErrInvalidRunStatus is acceptable if workflow already completed
		t.Fatalf("CancelWorkflow() error = %v", err)
	}

	// If cancel succeeded, verify status
	if err == nil {
		run, err := r.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetRun() error = %v", err)
		}
		if run.Status != river.RunStatusCancelled {
			// May have completed before cancel took effect
			if run.Status != river.RunStatusCompleted {
				t.Errorf("Expected status cancelled or completed, got %v", run.Status)
			}
		}
	}
}

func TestRunner_GetHistory(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
		Workers:    2,
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer r.Stop(ctx)

	// Start and wait for completion
	input := GreetInput{Name: "History Test"}
	inputJSON, _ := json.Marshal(input)
	runID, err := r.StartWorkflow(ctx, "greet-workflow", inputJSON)
	if err != nil {
		t.Fatalf("StartWorkflow() error = %v", err)
	}

	// Wait for completion
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, _ := r.GetRun(ctx, runID)
		if run.Status == river.RunStatusCompleted || run.Status == river.RunStatusFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Get history
	history, err := r.GetHistory(ctx, runID)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}

	// Verify events are in order
	for i := 1; i < len(history); i++ {
		if history[i].Sequence <= history[i-1].Sequence {
			t.Errorf("Events not in order: seq %d <= %d", history[i].Sequence, history[i-1].Sequence)
		}
	}

	// Verify workflow.started is first
	if len(history) > 0 && history[0].Type != "workflow.started" {
		t.Errorf("Expected first event to be workflow.started, got %s", history[0].Type)
	}
}

func TestRunner_NotStarted(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()

	// Try to start workflow without starting runner
	_, err = r.StartWorkflow(ctx, "greet-workflow", nil)
	if err != river.ErrRunnerNotStarted {
		t.Errorf("Expected ErrRunnerNotStarted, got %v", err)
	}
}

func TestRunner_DoubleStart(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()
	registry.Register(greetWorkflow)

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()

	if err := r.Start(ctx); err != nil {
		t.Fatalf("First Start() error = %v", err)
	}
	defer r.Stop(ctx)

	// Try to start again
	err = r.Start(ctx)
	if err != river.ErrRunnerAlreadyStarted {
		t.Errorf("Expected ErrRunnerAlreadyStarted, got %v", err)
	}
}

func TestRunner_RunNotFound(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	eventStore := pgstore.New(pool)
	registry := river.NewRegistry()

	r, err := river.NewRunner(river.Config{
		Pool:       pool,
		EventStore: eventStore,
		Registry:   registry,
		Logger:     &testLogger{t: t},
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	ctx := context.Background()

	// GetRun for non-existent run
	_, err = r.GetRun(ctx, "non-existent-run-id")
	if err != river.ErrRunNotFound {
		t.Errorf("Expected ErrRunNotFound, got %v", err)
	}

	// GetHistory for non-existent run
	_, err = r.GetHistory(ctx, "non-existent-run-id")
	if err != river.ErrRunNotFound {
		t.Errorf("Expected ErrRunNotFound, got %v", err)
	}
}
