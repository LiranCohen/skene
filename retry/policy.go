// Package retry provides configurable retry policies with exponential backoff
// for workflow step execution.
package retry

import (
	"math"
	"math/rand"
	"time"
)

// Policy defines retry behavior for a step.
type Policy struct {
	// MaxAttempts is the maximum number of attempts (including the first).
	// Must be at least 1.
	MaxAttempts int

	// InitialDelay is the delay before the first retry.
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries.
	MaxDelay time.Duration

	// Multiplier is the backoff multiplier applied after each retry.
	// For example, 2.0 doubles the delay each time.
	Multiplier float64

	// Jitter is a random factor (0-1) applied to the delay.
	// For example, 0.1 adds up to 10% random variation.
	Jitter float64
}

// Default returns a sensible default retry policy.
// 3 attempts, 1 second initial delay, 30 second max, 2x multiplier, 10% jitter.
func Default() *Policy {
	return &Policy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
	}
}

// NoRetry returns a policy that doesn't retry.
func NoRetry() *Policy {
	return &Policy{
		MaxAttempts:  1,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   1.0,
		Jitter:       0,
	}
}

// NextDelay calculates the delay for the given attempt.
// Attempt is 1-indexed (attempt 1 is the first retry, after the initial try).
// Returns 0 for attempt 0 or negative attempts.
func (p *Policy) NextDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// Calculate base delay with exponential backoff
	// attempt 1 -> InitialDelay
	// attempt 2 -> InitialDelay * Multiplier
	// attempt 3 -> InitialDelay * Multiplier^2
	multiplier := math.Pow(p.Multiplier, float64(attempt-1))
	delay := time.Duration(float64(p.InitialDelay) * multiplier)

	// Cap at MaxDelay
	if p.MaxDelay > 0 && delay > p.MaxDelay {
		delay = p.MaxDelay
	}

	// Apply jitter
	if p.Jitter > 0 {
		// Jitter adds random variation: delay * (1 +/- jitter)
		// We use (1 - jitter + 2*jitter*rand) to get range [1-jitter, 1+jitter]
		jitterFactor := 1 - p.Jitter + 2*p.Jitter*rand.Float64()
		delay = time.Duration(float64(delay) * jitterFactor)
	}

	return delay
}

// ShouldRetry returns true if another attempt should be made.
// Attempt is the number of the attempt that just failed (1-indexed).
// The error parameter is provided for future extensibility (e.g., non-retryable errors).
func (p *Policy) ShouldRetry(attempt int, _ error) bool {
	return attempt < p.MaxAttempts
}
