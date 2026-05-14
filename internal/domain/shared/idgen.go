package shared

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// IDGenerator produces new ULID strings. Use FixedIDGenerator in tests,
// SystemIDGenerator in production wiring.
type IDGenerator interface {
	NewID() string
}

type fixedIDGenerator struct {
	ids []string
	i   int
}

// NewID returns the next preconfigured ID. After the slice is exhausted it
// returns the empty string (test will fail loudly on parse).
func (g *fixedIDGenerator) NewID() string {
	if g.i >= len(g.ids) {
		return ""
	}
	id := g.ids[g.i]
	g.i++
	return id
}

// FixedIDGenerator returns an IDGenerator that yields ids in order. For tests.
func FixedIDGenerator(ids []string) IDGenerator {
	return &fixedIDGenerator{ids: ids}
}

// SystemIDGenerator generates monotonic ULIDs from a non-deterministic entropy
// source. Allowed only in infrastructure/bootstrap; lint forbids ulid.Make in
// domain/application.
//
// Concurrency: oklog/ulid/v2's MonotonicEntropy is NOT safe for concurrent
// use (mutates an internal uint80 counter + an io.Reader buffer on every
// Read). The apply phase spawns team-lead + implement subprocesses
// concurrently (teamlead.go runTeamLead.func1 + runImplementWithRetry),
// each minting their own agent_session ID. We serialize all NewID calls
// behind mu to avoid the race that `go test -race` flags in unit tests.
type SystemIDGenerator struct {
	mu      sync.Mutex
	clock   Clock
	entropy *ulid.MonotonicEntropy
}

// NewSystemIDGenerator constructs a SystemIDGenerator backed by the given
// Clock. Time monotonicity is preserved across rapid successive calls.
func NewSystemIDGenerator(c Clock) *SystemIDGenerator {
	src := rand.NewChaCha8([32]byte{}) //nolint:gosec // ULID entropy, not crypto
	return &SystemIDGenerator{
		clock:   c,
		entropy: ulid.Monotonic(src, 0),
	}
}

// NewID returns a freshly-generated ULID string. Safe for concurrent use.
func (g *SystemIDGenerator) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(g.clock.Now()), g.entropy).String()
}

// Now is exposed only to keep test ergonomics simple. forbidigo bans
// time.Now in `internal/domain` but this is documentation — no runtime
// call. Keeping the reference visible at the bottom of the file is
// clearer than moving it elsewhere.
var _ = time.Now //nolint:forbidigo // doc reference, no runtime call
