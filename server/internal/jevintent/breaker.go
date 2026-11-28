package jevintent

import (
	"sync"
	"time"
)

// BreakerState represents the circuit breaker state.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerConfig configures the sliding-window circuit breaker.
type BreakerConfig struct {
	Window              time.Duration // default 60s
	MinRequests         int           // default 5
	ErrorRateThreshold  float64       // default 0.50
	ConsecutiveFailures int           // default 5
	Cooldown            time.Duration // default 30s
	MaxSamples          int           // bounded ring cap, default 256
}

type outcomeSample struct {
	at time.Time
	ok bool
}

// slidingBreaker is a bounded-memory sliding-window circuit breaker.
type slidingBreaker struct {
	mu           sync.Mutex
	cfg          BreakerConfig
	nowFn        func() time.Time
	state        BreakerState
	openedAt     time.Time
	halfOpenBusy bool
	consecFails  int
	samples      []outcomeSample
}

func newSlidingBreaker(cfg BreakerConfig, nowFn func() time.Time) *slidingBreaker {
	if cfg.Window <= 0 {
		cfg.Window = 60 * time.Second
	}
	if cfg.MinRequests <= 0 {
		cfg.MinRequests = 5
	}
	if cfg.ErrorRateThreshold <= 0 || cfg.ErrorRateThreshold > 1 {
		cfg.ErrorRateThreshold = 0.50
	}
	if cfg.ConsecutiveFailures <= 0 {
		cfg.ConsecutiveFailures = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.MaxSamples <= 0 {
		cfg.MaxSamples = 256
	}
	if nowFn == nil {
		nowFn = time.Now
	}
	return &slidingBreaker{
		cfg:     cfg,
		nowFn:   nowFn,
		state:   BreakerClosed,
		samples: make([]outcomeSample, 0, min(cfg.MaxSamples, 64)),
	}
}

// Allow reports whether a request may proceed to upstream JEV.
func (b *slidingBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.nowFn()
	switch b.state {
	case BreakerClosed:
		return true
	case BreakerOpen:
		if now.Sub(b.openedAt) >= b.cfg.Cooldown {
			b.state = BreakerHalfOpen
			b.halfOpenBusy = true
			return true
		}
		return false
	case BreakerHalfOpen:
		if b.halfOpenBusy {
			return false
		}
		b.halfOpenBusy = true
		return true
	default:
		return true
	}
}

// Record updates the sliding window and transitions breaker state.
func (b *slidingBreaker) Record(ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.nowFn()

	if b.state == BreakerHalfOpen {
		b.halfOpenBusy = false
		if ok {
			b.state = BreakerClosed
			b.consecFails = 0
			b.samples = b.samples[:0]
			b.appendSampleLocked(now, true)
			return
		}
		b.state = BreakerOpen
		b.openedAt = now
		return
	}

	b.pruneLocked(now)
	b.appendSampleLocked(now, ok)

	if ok {
		b.consecFails = 0
		return
	}
	b.consecFails++
	if b.consecFails >= b.cfg.ConsecutiveFailures {
		b.state = BreakerOpen
		b.openedAt = now
		return
	}
	if len(b.samples) >= b.cfg.MinRequests {
		fails := 0
		for _, s := range b.samples {
			if !s.ok {
				fails++
			}
		}
		if float64(fails)/float64(len(b.samples)) >= b.cfg.ErrorRateThreshold {
			b.state = BreakerOpen
			b.openedAt = now
		}
	}
}

// State returns the current breaker state (accounting for elapsed cooldown).
func (b *slidingBreaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == BreakerOpen && b.nowFn().Sub(b.openedAt) >= b.cfg.Cooldown {
		return BreakerHalfOpen
	}
	return b.state
}

func (b *slidingBreaker) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.cfg.Window)
	idx := 0
	for idx < len(b.samples) && b.samples[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		copy(b.samples, b.samples[idx:])
		b.samples = b.samples[:len(b.samples)-idx]
	}
}

func (b *slidingBreaker) appendSampleLocked(now time.Time, ok bool) {
	if len(b.samples) >= b.cfg.MaxSamples {
		copy(b.samples, b.samples[1:])
		b.samples[len(b.samples)-1] = outcomeSample{at: now, ok: ok}
		return
	}
	b.samples = append(b.samples, outcomeSample{at: now, ok: ok})
}
