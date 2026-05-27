package recovery_test

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/recovery"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// Spec #68 (BUG-23) — pin the boot recovery service contract: every
// phase left at PhaseStatusRunning by a crashed process must be
// marked PhaseStatusInterrupted and persisted before the operator
// can take any action. The scan is fail-soft: a single broken row
// must not block the others.

func TestRecovery_NoStuckPhases_ReturnsZero(t *testing.T) {
	repo := newFakePhaseRepo()
	svc := recovery.NewService(repo, nil)
	n, err := svc.MarkStuckInterrupted(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.Empty(t, repo.savedIDs(), "no Save calls when nothing stuck")
}

func TestRecovery_OneStuckPhase_MarksAndSaves(t *testing.T) {
	repo := newFakePhaseRepo()
	p := newRunningPhase(t)
	repo.add(p)

	svc := recovery.NewService(repo, nil)
	n, err := svc.MarkStuckInterrupted(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	require.Equal(t, phase.PhaseStatusInterrupted, p.Status(),
		"the in-memory phase reference must transition")
	require.Equal(t, []string{p.ID().String()}, repo.savedIDs(),
		"the phase must be persisted exactly once")
}

func TestRecovery_MultipleStuckPhases_AllProcessed(t *testing.T) {
	repo := newFakePhaseRepo()
	phases := []*phase.Phase{
		newRunningPhase(t),
		newRunningPhase(t),
		newRunningPhase(t),
	}
	for _, p := range phases {
		repo.add(p)
	}

	svc := recovery.NewService(repo, nil)
	n, err := svc.MarkStuckInterrupted(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	for _, p := range phases {
		require.Equal(t, phase.PhaseStatusInterrupted, p.Status())
	}
	require.Len(t, repo.savedIDs(), 3)
}

func TestRecovery_ListError_PropagatedAndZeroMarked(t *testing.T) {
	repo := newFakePhaseRepo()
	repo.listErr = errors.New("db connection refused")

	svc := recovery.NewService(repo, nil)
	n, err := svc.MarkStuckInterrupted(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "list running phases")
	require.Equal(t, 0, n)
}

func TestRecovery_SaveError_OneFailsDoesNotBlockOthers(t *testing.T) {
	repo := newFakePhaseRepo()
	good1 := newRunningPhase(t)
	bad := newRunningPhase(t)
	good2 := newRunningPhase(t)
	repo.add(good1)
	repo.add(bad)
	repo.add(good2)
	repo.saveErrFor[bad.ID().String()] = errors.New("constraint violation")

	svc := recovery.NewService(repo, nil)
	n, err := svc.MarkStuckInterrupted(context.Background())
	require.Error(t, err, "first save error must be surfaced")
	require.Equal(t, 2, n, "other phases must still be persisted")

	// The bad phase is still marked in memory (domain transition
	// succeeded) but the Save failed — we record the partial state
	// rather than rolling back.
	require.Equal(t, phase.PhaseStatusInterrupted, bad.Status())
	require.Equal(t, phase.PhaseStatusInterrupted, good1.Status())
	require.Equal(t, phase.PhaseStatusInterrupted, good2.Status())
}

// --- fakes ---

type fakePhaseRepo struct {
	mu         sync.Mutex
	byID       map[string]*phase.Phase
	listErr    error
	saveErrFor map[string]error
	saved      []string
}

func newFakePhaseRepo() *fakePhaseRepo {
	return &fakePhaseRepo{
		byID:       map[string]*phase.Phase{},
		saveErrFor: map[string]error{},
	}
}

func (r *fakePhaseRepo) add(p *phase.Phase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[p.ID().String()] = p
}

func (r *fakePhaseRepo) savedIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.saved))
	copy(out, r.saved)
	return out
}

func (r *fakePhaseRepo) Save(_ context.Context, p *phase.Phase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.saveErrFor[p.ID().String()]; ok {
		return err
	}
	r.saved = append(r.saved, p.ID().String())
	return nil
}

func (r *fakePhaseRepo) FindByID(_ context.Context, id ids.PhaseID) (*phase.Phase, error) {
	return nil, outbound.ErrNotFound
}

func (r *fakePhaseRepo) FindByChangeAndType(_ context.Context, _ ids.ChangeID, _ phase.PhaseType) (*phase.Phase, error) {
	return nil, outbound.ErrNotFound
}

func (r *fakePhaseRepo) FindRunningByChange(_ context.Context, _ ids.ChangeID) (*phase.Phase, error) {
	return nil, outbound.ErrNotFound
}

func (r *fakePhaseRepo) FindAllRunning(_ context.Context) ([]*phase.Phase, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*phase.Phase, 0, len(r.byID))
	for _, p := range r.byID {
		if p.Status() == phase.PhaseStatusRunning {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *fakePhaseRepo) LockByChange(_ context.Context, _ ids.ChangeID) error { return nil }

// newRunningPhase constructs a domain Phase already advanced to
// PhaseStatusRunning so MarkInterrupted's source-status guard is
// satisfied. Each call generates a fresh random ULID-like identifier
// to avoid collisions when the same test inserts multiple phases.
func newRunningPhase(t *testing.T) *phase.Phase {
	t.Helper()
	pid, err := ids.ParsePhaseID(freshULID(t))
	require.NoError(t, err)
	cid, err := ids.ParseChangeID(freshULID(t))
	require.NoError(t, err)
	p, err := phase.New(pid, cid, phase.PhaseSpec, 3)
	require.NoError(t, err)
	require.NoError(t, p.Start(time.Now()))
	require.Equal(t, phase.PhaseStatusRunning, p.Status())
	return p
}

// freshULID returns a Crockford-base32 26-char string that
// ids.Parse{Phase,Change}ID accepts. We don't need real time/random
// ordering — only a syntactically valid, collision-free identifier.
// The Crockford alphabet omits I/L/O/U; we draw from the legal set
// and prefix with the "01" timestamp-low byte so the result always
// parses regardless of the parser's internal time-component check.
func freshULID(t *testing.T) string {
	t.Helper()
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var raw [12]byte
	_, err := crand.Read(raw[:])
	require.NoError(t, err)
	// 24 random crockford chars + "01" prefix = 26 chars exactly.
	hexRandom := hex.EncodeToString(raw[:])[:24] // hex chars subset of crockford alphabet
	// Map hex digits/letters into crockford by upcasing — hex 0-9 + a-f
	// fit inside crockford alphabet, just need uppercase + skip rules
	// don't apply since we picked from alphabet[].
	b := []byte(hexRandom)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
		// crockford excludes I, L, O — replace if present.
		switch b[i] {
		case 'I':
			b[i] = alphabet[18]
		case 'L':
			b[i] = alphabet[20]
		case 'O':
			b[i] = alphabet[24]
		}
	}
	return "01" + string(b)
}
