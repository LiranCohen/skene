package retry

import (
	"errors"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	p := Default()

	if p.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", p.MaxAttempts)
	}
	if p.InitialDelay != 1*time.Second {
		t.Errorf("InitialDelay = %v, want 1s", p.InitialDelay)
	}
	if p.MaxDelay != 30*time.Second {
		t.Errorf("MaxDelay = %v, want 30s", p.MaxDelay)
	}
	if p.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want 2.0", p.Multiplier)
	}
	if p.Jitter != 0.1 {
		t.Errorf("Jitter = %v, want 0.1", p.Jitter)
	}
}

func TestNoRetry(t *testing.T) {
	p := NoRetry()

	if p.MaxAttempts != 1 {
		t.Errorf("MaxAttempts = %d, want 1", p.MaxAttempts)
	}
	if p.InitialDelay != 0 {
		t.Errorf("InitialDelay = %v, want 0", p.InitialDelay)
	}
}

func TestNextDelay(t *testing.T) {
	tests := []struct {
		name    string
		policy  *Policy
		attempt int
		wantMin time.Duration
		wantMax time.Duration
	}{
		{
			name: "attempt 0 returns 0",
			policy: &Policy{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: 0,
			wantMin: 0,
			wantMax: 0,
		},
		{
			name: "attempt 1 returns initial delay",
			policy: &Policy{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: 1,
			wantMin: 1 * time.Second,
			wantMax: 1 * time.Second,
		},
		{
			name: "attempt 2 applies multiplier",
			policy: &Policy{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: 2,
			wantMin: 2 * time.Second,
			wantMax: 2 * time.Second,
		},
		{
			name: "attempt 3 applies multiplier squared",
			policy: &Policy{
				MaxAttempts:  4,
				InitialDelay: 1 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: 3,
			wantMin: 4 * time.Second,
			wantMax: 4 * time.Second,
		},
		{
			name: "caps at max delay",
			policy: &Policy{
				MaxAttempts:  5,
				InitialDelay: 1 * time.Second,
				MaxDelay:     3 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: 3,
			wantMin: 3 * time.Second, // Would be 4s but capped
			wantMax: 3 * time.Second,
		},
		{
			name: "jitter adds variation",
			policy: &Policy{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Second,
				Multiplier:   1.0,
				Jitter:       0.1,
			},
			attempt: 1,
			wantMin: 900 * time.Millisecond, // 1s * 0.9
			wantMax: 1100 * time.Millisecond, // 1s * 1.1
		},
		{
			name: "negative attempt returns 0",
			policy: &Policy{
				MaxAttempts:  3,
				InitialDelay: 1 * time.Second,
				Multiplier:   2.0,
				Jitter:       0,
			},
			attempt: -1,
			wantMin: 0,
			wantMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run multiple times to test jitter
			for i := 0; i < 100; i++ {
				got := tt.policy.NextDelay(tt.attempt)
				if got < tt.wantMin || got > tt.wantMax {
					t.Errorf("NextDelay(%d) = %v, want between %v and %v",
						tt.attempt, got, tt.wantMin, tt.wantMax)
					break
				}
			}
		})
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name        string
		maxAttempts int
		attempt     int
		want        bool
	}{
		{
			name:        "first attempt failed, should retry",
			maxAttempts: 3,
			attempt:     1,
			want:        true,
		},
		{
			name:        "second attempt failed, should retry",
			maxAttempts: 3,
			attempt:     2,
			want:        true,
		},
		{
			name:        "third attempt failed, max reached",
			maxAttempts: 3,
			attempt:     3,
			want:        false,
		},
		{
			name:        "no retry policy",
			maxAttempts: 1,
			attempt:     1,
			want:        false,
		},
		{
			name:        "attempt 0 should retry",
			maxAttempts: 3,
			attempt:     0,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Policy{MaxAttempts: tt.maxAttempts}
			err := errors.New("test error")
			got := p.ShouldRetry(tt.attempt, err)
			if got != tt.want {
				t.Errorf("ShouldRetry(%d, err) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestExponentialBackoff(t *testing.T) {
	p := &Policy{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
		Jitter:       0,
	}

	expected := []time.Duration{
		0,                      // attempt 0
		100 * time.Millisecond, // attempt 1
		200 * time.Millisecond, // attempt 2
		400 * time.Millisecond, // attempt 3
		800 * time.Millisecond, // attempt 4
	}

	for attempt, want := range expected {
		got := p.NextDelay(attempt)
		if got != want {
			t.Errorf("NextDelay(%d) = %v, want %v", attempt, got, want)
		}
	}
}

func TestJitterDistribution(t *testing.T) {
	p := &Policy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Second,
		Multiplier:   1.0,
		Jitter:       0.2, // 20% jitter
	}

	// Run many iterations and check distribution
	minDelay := 1 * time.Second
	maxDelay := time.Duration(0)
	samples := 1000

	for i := 0; i < samples; i++ {
		delay := p.NextDelay(1)
		if delay < minDelay {
			minDelay = delay
		}
		if delay > maxDelay {
			maxDelay = delay
		}
	}

	// With 20% jitter, we expect delays between 0.8s and 1.2s
	expectedMin := 800 * time.Millisecond
	expectedMax := 1200 * time.Millisecond

	// Allow some tolerance for randomness
	if minDelay > 850*time.Millisecond {
		t.Errorf("minDelay = %v, expected closer to %v", minDelay, expectedMin)
	}
	if maxDelay < 1150*time.Millisecond {
		t.Errorf("maxDelay = %v, expected closer to %v", maxDelay, expectedMax)
	}
}
