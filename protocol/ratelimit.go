package protocol

import (
	"context"
	"sync"
	"time"
)

type RateLimiter struct {
	rpm    int
	tpm    int
	mu     sync.Mutex
	window time.Time
	rCount int
	tCount int
}

func NewRateLimiter(rpm, tpm int) *RateLimiter {
	return &RateLimiter{rpm: rpm, tpm: tpm}
}

func (rl *RateLimiter) Wait(ctx context.Context, tokens int) error {
	if rl.rpm <= 0 && rl.tpm <= 0 {
		return nil
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if now.Sub(rl.window) >= time.Minute {
		rl.window = now
		rl.rCount = 0
		rl.tCount = 0
	}
	rl.rCount++
	rl.tCount += tokens
	var wait time.Duration
	if rl.rpm > 0 && rl.rCount > rl.rpm {
		wait = time.Minute - now.Sub(rl.window)
	}
	if rl.tpm > 0 && rl.tCount > rl.tpm {
		d := time.Minute - now.Sub(rl.window)
		if d > wait {
			wait = d
		}
	}
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		rl.window = time.Now()
		rl.rCount = 0
		rl.tCount = 0
		return nil
	}
}
