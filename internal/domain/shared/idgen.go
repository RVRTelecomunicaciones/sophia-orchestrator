package shared

import (
	"math/rand/v2"
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
type SystemIDGenerator struct {
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

// NewID returns a freshly-generated ULID string.
func (g *SystemIDGenerator) NewID() string {
	return ulid.MustNew(ulid.Timestamp(g.clock.Now()), g.entropy).String()
}

// Now is exposed only to keep test ergonomics simple.
var _ = time.Now
