package discipline

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SpawnGovernorConfig parameterizes the governor. Defaults per spec § 6.2:
// Max=4 (ceiling 6), StaggerMin=200ms, StaggerMax=500ms (jitter ±30%),
// WaitInterval=250ms, MaxWait=30s.
type SpawnGovernorConfig struct {
	Max          int
	StaggerMin   time.Duration
	StaggerMax   time.Duration
	WaitInterval time.Duration
	MaxWait      time.Duration
}

// Validate ensures the config is sane.
func (c SpawnGovernorConfig) Validate() error {
	if c.Max <= 0 {
		return fmt.Errorf("%w: Max must be > 0", ErrInvalidConfig)
	}
	if c.StaggerMin < 0 || c.StaggerMax < c.StaggerMin {
		return fmt.Errorf("%w: StaggerMax must be >= StaggerMin >= 0", ErrInvalidConfig)
	}
	if c.WaitInterval <= 0 {
		return fmt.Errorf("%w: WaitInterval must be > 0", ErrInvalidConfig)
	}
	if c.MaxWait < 0 {
		return fmt.Errorf("%w: MaxWait must be >= 0", ErrInvalidConfig)
	}
	return nil
}

// Waiter is an injectable wait abstraction so tests can drive the polling
// loop deterministically.
type Waiter interface {
	Wait(ctx context.Context, d time.Duration) error
}

type realWaiter struct{}

// Wait blocks until d elapses or ctx is cancelled. Returns ctx.Err() on cancel.
func (realWaiter) Wait(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // canonical context error
	case <-time.After(d):
		return nil
	}
}

// Sleeper is an injectable sleep abstraction for the post-acquire stagger.
type Sleeper interface {
	Sleep(d time.Duration)
}

type realSleeper struct{}

// Sleep blocks the calling goroutine for d.
func (realSleeper) Sleep(d time.Duration) { time.Sleep(d) }

// Jitter returns a non-negative pseudo-random duration in [0, span).
type Jitter interface {
	Jitter(span time.Duration) time.Duration
}

type realJitter struct{}

// Jitter returns a random duration in [0, span). Uses math/rand/v2 (not crypto).
func (realJitter) Jitter(span time.Duration) time.Duration {
	if span <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(span))) //nolint:gosec // jitter, not crypto
}

// SpawnGovernor enforces the global cap on concurrent dispatcher subprocesses
// (default 4, ceiling 6 per spec § 6) and applies stagger+jitter at acquire
// time to spread spawn timings (mitigates Anthropic burst-rate-limit per
// issue #53922).
type SpawnGovernor struct {
	repo    outbound.SpawnGovernorRepo
	cfg     SpawnGovernorConfig
	clock   shared.Clock
	waiter  Waiter
	sleeper Sleeper
	jitter  Jitter
	metrics *obs.Metrics // optional; nil ⇒ no-op recording
}

// NewSpawnGovernor constructs a SpawnGovernor with production defaults
// (real wait, sleep, jitter). Returns ErrInvalidConfig if cfg fails Validate.
// metrics is optional; pass nil to disable metric recording (safe for tests
// that don't need metrics assertions).
func NewSpawnGovernor(repo outbound.SpawnGovernorRepo, cfg SpawnGovernorConfig, clock shared.Clock, metrics *obs.Metrics) (*SpawnGovernor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SpawnGovernor{
		repo:    repo,
		cfg:     cfg,
		clock:   clock,
		waiter:  realWaiter{},
		sleeper: realSleeper{},
		jitter:  realJitter{},
		metrics: metrics,
	}, nil
}

// WithDeps replaces the real wait/sleep/jitter with test doubles. For tests only.
func (sg *SpawnGovernor) WithDeps(w Waiter, s Sleeper, j Jitter) *SpawnGovernor {
	sg.waiter = w
	sg.sleeper = s
	sg.jitter = j
	return sg
}

// Acquire reserves a dispatcher slot. Blocks (polling repo with WaitInterval)
// until a slot is free, ctx is cancelled, or MaxWait elapses (returns
// ErrSaturated). On success, applies a stagger+jitter sleep before returning.
func (sg *SpawnGovernor) Acquire(ctx context.Context) error {
	start := sg.clock.Now()
	deadline := start.Add(sg.cfg.MaxWait)
	for {
		ok, _, err := sg.repo.Acquire(ctx, sg.cfg.Max)
		if err != nil {
			return fmt.Errorf("spawn governor acquire: %w", err)
		}
		if ok {
			sg.applyStagger()
			// Record wait duration and bump active gauge on successful acquire.
			if sg.metrics != nil {
				sg.metrics.SpawnGovernorWaitMS.Observe(float64(sg.clock.Now().Sub(start).Milliseconds()))
				sg.metrics.SpawnGovernorActive.Inc()
			}
			return nil
		}
		if !sg.clock.Now().Before(deadline) {
			return ErrSaturated
		}
		if err := sg.waiter.Wait(ctx, sg.cfg.WaitInterval); err != nil {
			return fmt.Errorf("spawn governor wait: %w", err)
		}
	}
}

// Release returns the slot to the pool. Idempotent on the repo side.
func (sg *SpawnGovernor) Release(ctx context.Context) error {
	if err := sg.repo.Release(ctx); err != nil {
		return fmt.Errorf("spawn governor release: %w", err)
	}
	// Decrement active gauge on successful release.
	if sg.metrics != nil {
		sg.metrics.SpawnGovernorActive.Dec()
	}
	return nil
}

// applyStagger sleeps for StaggerMin + jitter (in [0, StaggerMax-StaggerMin)).
func (sg *SpawnGovernor) applyStagger() {
	span := sg.cfg.StaggerMax - sg.cfg.StaggerMin
	jitter := sg.jitter.Jitter(span)
	sg.sleeper.Sleep(sg.cfg.StaggerMin + jitter)
}

// DefaultConfig returns the V1 production defaults: Max=4, ceiling 6,
// stagger 200-500ms, wait 250ms, max-wait 30s.
func DefaultConfig() SpawnGovernorConfig {
	return SpawnGovernorConfig{
		Max:          4,
		StaggerMin:   200 * time.Millisecond,
		StaggerMax:   500 * time.Millisecond,
		WaitInterval: 250 * time.Millisecond,
		MaxWait:      30 * time.Second,
	}
}
