package workflow

import (
	"encoding/json"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
)

// MapOptions configures map execution.
type MapOptions struct {
	MaxConcurrency int // 0 = unlimited
}

// Run executes a child workflow synchronously.
// The child workflow runs with its own RunID and event stream.
// On replay, completed children are skipped and their output is loaded from events.
func Run[In, Out any](ctx Context, wf *WorkflowDef, input In) (Out, error) {
	var zero Out

	// Get the replayer from context
	replayer := getReplayerFromContext(ctx)
	if replayer == nil {
		return zero, fmt.Errorf("workflow.Run: no replayer in context")
	}

	// Generate child RunID
	childIndex := replayer.nextChildIndex()
	childRunID := fmt.Sprintf("%s-child-%d", ctx.RunID(), childIndex)

	// Marshal input for storage
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("workflow.Run: marshal input: %w", err)
	}

	// Check if child already completed (for replay)
	if output, ok := replayer.history.GetChildOutput(childRunID); ok {
		var result Out
		if err := json.Unmarshal(output, &result); err != nil {
			return zero, fmt.Errorf("workflow.Run: unmarshal child output: %w", err)
		}
		return result, nil
	}

	// Check if child failed previously
	if errMsg, ok := replayer.history.GetChildError(childRunID); ok {
		return zero, fmt.Errorf("child workflow failed: %s", errMsg)
	}

	// Record child.spawned event
	replayer.RecordChildSpawned(childRunID, wf.Name(), inputJSON)

	// Execute child workflow with fresh history
	childReplayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    childRunID,
		Input:    input,
		Logger:   replayer.logger,
	})

	// Execute the child workflow using the parent's context for cancellation
	output, err := childReplayer.Replay(ctx)
	if err != nil {
		replayer.RecordChildFailed(childRunID, err)
		return zero, fmt.Errorf("workflow.Run: child workflow error: %w", err)
	}

	if output.Result == ReplayFailed {
		replayer.RecordChildFailed(childRunID, output.Error)
		return zero, fmt.Errorf("workflow.Run: child workflow failed: %w", output.Error)
	}

	// Collect child events for storage
	// The child's events are stored in the child's event stream (different RunID)
	// For now, they're captured in the ReplayOutput.NewEvents

	// Marshal output for event
	var outputJSON json.RawMessage
	if output.FinalOutput != nil {
		outputJSON, err = json.Marshal(output.FinalOutput)
		if err != nil {
			return zero, fmt.Errorf("workflow.Run: marshal output: %w", err)
		}
	}

	// Record child.completed event
	replayer.RecordChildCompleted(childRunID, outputJSON)

	// Deserialize to target type
	var result Out
	if output.FinalOutput != nil {
		if outputJSON != nil {
			if err := json.Unmarshal(outputJSON, &result); err != nil {
				return zero, fmt.Errorf("workflow.Run: unmarshal output: %w", err)
			}
		}
	}

	return result, nil
}

// replayerAccessor is an internal interface for accessing the replayer.
type replayerAccessor interface {
	getReplayer() *Replayer
}

// getReplayerFromContext extracts the replayer from the context.
func getReplayerFromContext(ctx Context) *Replayer {
	if accessor, ok := ctx.(replayerAccessor); ok {
		return accessor.getReplayer()
	}
	return nil
}

// Map executes a child workflow for each item (fan-out).
// All children run concurrently with no limit.
// Results are collected in the same order as the input items.
func Map[In, Out any](ctx Context, items []In, wf *WorkflowDef) ([]Out, error) {
	return MapWithOptions[In, Out](ctx, items, wf, MapOptions{})
}

// MapWithOptions executes a child workflow for each item with configuration.
// MaxConcurrency limits the number of concurrent child executions (0 = unlimited).
// Results are collected in the same order as the input items.
func MapWithOptions[In, Out any](ctx Context, items []In, wf *WorkflowDef, opts MapOptions) ([]Out, error) {
	if len(items) == 0 {
		return []Out{}, nil
	}

	// Get the replayer from context
	replayer := getReplayerFromContext(ctx)
	if replayer == nil {
		return nil, fmt.Errorf("workflow.Map: no replayer in context")
	}

	// Generate map identifier
	mapIndex := replayer.nextMapIndex()

	// Check if map already completed (for replay)
	if resultsJSON, ok := replayer.history.GetMapResults(mapIndex); ok {
		var results []Out
		if err := json.Unmarshal(resultsJSON, &results); err != nil {
			return nil, fmt.Errorf("workflow.Map: unmarshal results: %w", err)
		}
		return results, nil
	}

	// Check if map failed previously
	if mapErr := replayer.history.GetMapError(mapIndex); mapErr != nil {
		return nil, fmt.Errorf("workflow.Map: child %d failed: %s", mapErr.FailedIndex, mapErr.Error)
	}

	// Record map.started event
	replayer.RecordMapStarted(mapIndex, len(items))

	// Prepare results slice with pre-allocated capacity
	results := make([]Out, len(items))
	var mu sync.Mutex

	// Use errgroup for concurrent execution with optional limit
	g, gCtx := errgroup.WithContext(ctx)
	if opts.MaxConcurrency > 0 {
		g.SetLimit(opts.MaxConcurrency)
	}

	// Track first failure for event recording
	var firstFailedIndex int
	var firstError error
	var failMu sync.Mutex

	for i, item := range items {
		i, item := i, item // capture loop variables

		// Generate unique child RunID for this map item
		childRunID := fmt.Sprintf("%s-map-%d-%d", ctx.RunID(), mapIndex, i)

		// Check if this specific child already completed (partial replay)
		if output, ok := replayer.history.GetChildOutput(childRunID); ok {
			var result Out
			if err := json.Unmarshal(output, &result); err != nil {
				return nil, fmt.Errorf("workflow.Map: unmarshal child %d output: %w", i, err)
			}
			mu.Lock()
			results[i] = result
			mu.Unlock()
			continue
		}

		// Check if this child failed previously
		if errMsg, ok := replayer.history.GetChildError(childRunID); ok {
			failMu.Lock()
			if firstError == nil {
				firstFailedIndex = i
				firstError = fmt.Errorf("child workflow failed: %s", errMsg)
			}
			failMu.Unlock()
			continue
		}

		g.Go(func() error {
			// Check for cancellation
			select {
			case <-gCtx.Done():
				return gCtx.Err()
			default:
			}

			// Marshal input for storage
			inputJSON, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("workflow.Map: marshal input for child %d: %w", i, err)
			}

			// Record child.spawned event (thread-safe through replayer)
			replayer.RecordChildSpawned(childRunID, wf.Name(), inputJSON)

			// Execute child workflow with fresh history
			childReplayer := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    childRunID,
				Input:    item,
				Logger:   replayer.logger,
			})

			output, err := childReplayer.Replay(gCtx)
			if err != nil {
				replayer.RecordChildFailed(childRunID, err)
				failMu.Lock()
				if firstError == nil {
					firstFailedIndex = i
					firstError = err
				}
				failMu.Unlock()
				return fmt.Errorf("child %d error: %w", i, err)
			}

			if output.Result == ReplayFailed {
				replayer.RecordChildFailed(childRunID, output.Error)
				failMu.Lock()
				if firstError == nil {
					firstFailedIndex = i
					firstError = output.Error
				}
				failMu.Unlock()
				return fmt.Errorf("child %d failed: %w", i, output.Error)
			}

			// Marshal output for event
			var outputJSON json.RawMessage
			if output.FinalOutput != nil {
				outputJSON, err = json.Marshal(output.FinalOutput)
				if err != nil {
					return fmt.Errorf("workflow.Map: marshal output for child %d: %w", i, err)
				}
			}

			// Record child.completed event
			replayer.RecordChildCompleted(childRunID, outputJSON)

			// Deserialize to target type
			var result Out
			if output.FinalOutput != nil && outputJSON != nil {
				if err := json.Unmarshal(outputJSON, &result); err != nil {
					return fmt.Errorf("workflow.Map: unmarshal child %d output: %w", i, err)
				}
			}

			mu.Lock()
			results[i] = result
			mu.Unlock()

			return nil
		})
	}

	// Wait for all children to complete
	if err := g.Wait(); err != nil {
		// Record map.failed event with first failure
		replayer.RecordMapFailed(mapIndex, firstFailedIndex, firstError)
		return nil, fmt.Errorf("workflow.Map: %w", err)
	}

	// Check if there was a pre-existing failure from history
	if firstError != nil {
		replayer.RecordMapFailed(mapIndex, firstFailedIndex, firstError)
		return nil, fmt.Errorf("workflow.Map: child %d failed: %w", firstFailedIndex, firstError)
	}

	// Marshal results for event
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return nil, fmt.Errorf("workflow.Map: marshal results: %w", err)
	}

	// Record map.completed event
	replayer.RecordMapCompleted(mapIndex, resultsJSON)

	return results, nil
}

