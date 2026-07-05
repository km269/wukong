// Package retry provides retry logic for operations with configurable backoff.
package retry

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Options configures the retry behavior.
type Options struct {
	MaxAttempts int // Maximum number of attempts (default: 3)
	InitialDelay time.Duration // Initial delay between retries (default: 1s)
	MaxDelay time.Duration // Maximum delay between retries (default: 10s)
	BackoffFactor float64 // Backoff multiplier (default: 2.0)
	JitterFactor float64 // Jitter factor to randomize delays (default: 0.2)
	Retryable func(error) bool // Function to determine if an error is retryable (default: all errors)
}

// DefaultOptions returns the default retry options.
func DefaultOptions() Options {
	return Options{
		MaxAttempts:   3,
		InitialDelay:  time.Second,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 2.0,
		JitterFactor:  0.2,
		Retryable:     func(_ error) bool { return true }, // By default, retry all errors
	}
}

// Do executes the given function with retry logic.
// It will retry up to MaxAttempts times with exponential backoff.
func Do(ctx context.Context, opts Options, fn func(context.Context) error) error {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = DefaultOptions().MaxAttempts
	}
	if opts.InitialDelay <= 0 {
		opts.InitialDelay = DefaultOptions().InitialDelay
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = DefaultOptions().MaxDelay
	}
	if opts.BackoffFactor <= 0 {
		opts.BackoffFactor = DefaultOptions().BackoffFactor
	}
	if opts.JitterFactor < 0 {
		opts.JitterFactor = DefaultOptions().JitterFactor
	}
	if opts.Retryable == nil {
		opts.Retryable = DefaultOptions().Retryable
	}

	var lastErr error
	for attempt := 0; attempt < opts.MaxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if attempt > 0 {
			// Wait with backoff and jitter
			delay := computeDelay(opts, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if this error should be retried
		if !opts.Retryable(err) {
			return err
		}
	}

	return lastErr
}

// computeDelay calculates the delay with backoff and jitter.
func computeDelay(opts Options, attempt int) time.Duration {
	baseDelay := float64(opts.InitialDelay) * math.Pow(opts.BackoffFactor, float64(attempt))
	
	// Cap at MaxDelay
	if baseDelay > float64(opts.MaxDelay) {
		baseDelay = float64(opts.MaxDelay)
	}
	
	// Apply jitter
	if opts.JitterFactor > 0 {
		jitter := (rand.Float64()*2 - 1) * opts.JitterFactor // -0.2 to +0.2 by default
		baseDelay = baseDelay * (1 + jitter)
	}
	
	return time.Duration(baseDelay)
}
