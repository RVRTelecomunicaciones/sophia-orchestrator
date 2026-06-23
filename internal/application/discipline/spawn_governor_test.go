package discipline_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

// fakeRepo lets tests script a sequence of Acquire outcomes (true/false) and
// counts Release calls.
type fakeRepo struct {
	mu          sync.Mutex
	acquireResp []bool // sequence of acquire results
	idx         int
	releaseN    int
	acquireErr  error
}

func (r *fakeRepo) Acquire(_ context.Context, _ int) (bool, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.acquireErr != nil {
		return false, 0, r.acquireErr
	}
	if r.idx >= len(r.acquireResp) {
		return false, 0, nil // saturate forever
	}
	ok := r.acquireResp[r.idx]
	r.idx++
	return ok, 0, nil
}

func (r *fakeRepo) Release(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseN++
	return nil
}

func (r *fakeRepo) Active(_ context.Context) (int, error) {
	return 0, nil
}

// fakeWaiter does not actually sleep; it returns immediately. An optional
// onWait callback lets tests advance the test clock or inject failures.
type fakeWaiter struct {
	calls  int
	mu     sync.Mutex
	onWait func()
}

func (w *fakeWaiter) Wait(ctx context.Context, _ time.Duration) error {
	w.mu.Lock()
	w.calls++
	cb := w.onWait
	w.mu.Unlock()
	if cb != nil {
		cb()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// fakeSleeper records sleep durations without actually sleeping.
type fakeSleeper struct {
	calls []time.Duration
	mu    sync.Mutex
}

func (s *fakeSleeper) Sleep(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, d)
}

// fixedJitter returns a constant jitter value.
type fixedJitter struct{ d time.Duration }

func (j fixedJitter) Jitter(_ time.Duration) time.Duration { return j.d }

// advanceableClock returns a time that the test can advance manually.
type advanceableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *advanceableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *advanceableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newSGForTest(t *testing.T, repo *fakeRepo) (*discipline.SpawnGovernor, *fakeWaiter, *fakeSleeper, *advanceableClock) {
	t.Helper()
	clk := &advanceableClock{t: time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)}
	cfg := discipline.SpawnGovernorConfig{
		Max:          2,
		StaggerMin:   100 * time.Millisecond,
		StaggerMax:   300 * time.Millisecond,
		WaitInterval: 50 * time.Millisecond,
		MaxWait:      1 * time.Second,
	}
	sg, err := discipline.NewSpawnGovernor(repo, cfg, clk, nil)
	require.NoError(t, err)
	w := &fakeWaiter{}
	s := &fakeSleeper{}
	sg.WithDeps(w, s, fixedJitter{d: 50 * time.Millisecond})
	return sg, w, s, clk
}

func TestSpawnGovernor_Acquire_Success(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{true}}
	sg, _, sleeper, _ := newSGForTest(t, repo)
	require.NoError(t, sg.Acquire(context.Background()))
	require.Len(t, sleeper.calls, 1, "stagger should fire once on success")
	require.Equal(t, 150*time.Millisecond, sleeper.calls[0], "min + fixed jitter (100 + 50)")
}

func TestSpawnGovernor_Acquire_BlocksUntilSlotFree(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{false, false, true}}
	sg, waiter, sleeper, _ := newSGForTest(t, repo)
	require.NoError(t, sg.Acquire(context.Background()))
	require.Equal(t, 2, waiter.calls, "should poll twice before success")
	require.Len(t, sleeper.calls, 1, "stagger only after success")
}

func TestSpawnGovernor_Acquire_SaturatedTimesOut(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{false}} // always saturated
	sg, waiter, _, clk := newSGForTest(t, repo)
	// Each Wait call advances the test clock past the MaxWait deadline so
	// the next Acquire iteration trips the saturation check.
	waiter.onWait = func() { clk.advance(2 * time.Second) }
	err := sg.Acquire(context.Background())
	require.ErrorIs(t, err, discipline.ErrSaturated)
	require.GreaterOrEqual(t, waiter.calls, 1)
}

func TestSpawnGovernor_Acquire_RepoErrorPropagates(t *testing.T) {
	myErr := errors.New("boom")
	repo := &fakeRepo{acquireErr: myErr}
	sg, _, _, _ := newSGForTest(t, repo)
	err := sg.Acquire(context.Background())
	require.ErrorIs(t, err, myErr)
}

func TestSpawnGovernor_Acquire_ContextCancelled(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{false, false, false}}
	sg, _, _, _ := newSGForTest(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sg.Acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSpawnGovernor_Release(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{true}}
	sg, _, _, _ := newSGForTest(t, repo)
	require.NoError(t, sg.Acquire(context.Background()))
	require.NoError(t, sg.Release(context.Background()))
	require.Equal(t, 1, repo.releaseN)
}

func TestSpawnGovernor_Release_RepoErrorPropagates(t *testing.T) {
	repo := &errReleaseRepo{}
	clk := shared.FixedClock(time.Now())
	sg, err := discipline.NewSpawnGovernor(repo, discipline.DefaultConfig(), clk, nil)
	require.NoError(t, err)
	err = sg.Release(context.Background())
	require.Error(t, err)
}

type errReleaseRepo struct{}

func (errReleaseRepo) Acquire(_ context.Context, _ int) (bool, int, error) {
	return true, 0, nil
}
func (errReleaseRepo) Release(_ context.Context) error { return errors.New("release boom") }
func (errReleaseRepo) Active(_ context.Context) (int, error) {
	return 0, nil
}

func TestSpawnGovernorConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		cfg  discipline.SpawnGovernorConfig
		ok   bool
	}{
		{"valid default", discipline.DefaultConfig(), true},
		{"zero max", discipline.SpawnGovernorConfig{Max: 0, StaggerMin: 0, StaggerMax: 0, WaitInterval: 1, MaxWait: 1}, false},
		{"negative max", discipline.SpawnGovernorConfig{Max: -1, WaitInterval: 1}, false},
		{"stagger inverted", discipline.SpawnGovernorConfig{Max: 1, StaggerMin: 200, StaggerMax: 100, WaitInterval: 1}, false},
		{"stagger negative", discipline.SpawnGovernorConfig{Max: 1, StaggerMin: -1, StaggerMax: 0, WaitInterval: 1}, false},
		{"zero wait interval", discipline.SpawnGovernorConfig{Max: 1, WaitInterval: 0}, false},
		{"negative max wait", discipline.SpawnGovernorConfig{Max: 1, WaitInterval: 1, MaxWait: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, discipline.ErrInvalidConfig)
			}
		})
	}
}

func TestNewSpawnGovernor_RejectsBadConfig(t *testing.T) {
	repo := &fakeRepo{}
	clk := shared.FixedClock(time.Now())
	_, err := discipline.NewSpawnGovernor(repo, discipline.SpawnGovernorConfig{Max: 0}, clk, nil)
	require.ErrorIs(t, err, discipline.ErrInvalidConfig)
}

func TestDefaultConfig_Values(t *testing.T) {
	c := discipline.DefaultConfig()
	require.Equal(t, 4, c.Max)
	require.Equal(t, 200*time.Millisecond, c.StaggerMin)
	require.Equal(t, 500*time.Millisecond, c.StaggerMax)
	require.Equal(t, 250*time.Millisecond, c.WaitInterval)
	require.Equal(t, 30*time.Second, c.MaxWait)
}

// realWaiter / realSleeper / realJitter exercise (no-op tests for coverage)
func TestRealWaiter_RespectsContextCancel(t *testing.T) {
	repo := &fakeRepo{acquireResp: []bool{false, false}}
	clk := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	cfg := discipline.SpawnGovernorConfig{
		Max: 1, StaggerMin: 0, StaggerMax: 0,
		WaitInterval: 50 * time.Millisecond,
		MaxWait:      1 * time.Hour, // large; expect cancel-driven exit
	}
	sg, err := discipline.NewSpawnGovernor(repo, cfg, clk, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = sg.Acquire(ctx)
	require.Error(t, err)
}
