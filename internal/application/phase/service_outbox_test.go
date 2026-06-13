package phase_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/outbox"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// fakeCompletionTx records the change + outbox event written atomically at
// archive completion and lets a test force a rollback (commit failure).
type fakeCompletionTx struct {
	mu       sync.Mutex
	saved    *domainchange.Change
	event    *outbox.Event
	calls    int
	forceErr error
}

func (f *fakeCompletionTx) SaveCompletedWithOutbox(_ context.Context, c *domainchange.Change, ev *outbox.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.forceErr != nil {
		return f.forceErr
	}
	f.saved = c
	f.event = ev
	return nil
}

// trackingNotifier records whether the legacy fire-and-forget path was used.
type trackingNotifier struct {
	mu     sync.Mutex
	called int
}

func (n *trackingNotifier) Notify(_ context.Context, _ appphase.ArchivedWebhookPayload) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.called++
}

// D.1 — reaching archive enqueues exactly one pending outbox row with
// event_type='phase.archived' and a byte-identical PhaseArchivedPayload,
// via the completion transaction. The legacy Notify goroutine is NOT called.
func TestAdvanceChange_Archive_EnqueuesOutbox_NoFireAndForget(t *testing.T) {
	h := archiveHarness(t)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C02")

	completion := &fakeCompletionTx{}
	notifier := &trackingNotifier{}

	clock := shared.FixedClock(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5PA1",
		"01ARZ3NDEKTSV4RRFFQ69G5SA1",
		"01ARZ3NDEKTSV4RRFFQ69G5OB9", // outbox event id
	})

	val := discipline.NewValidator()
	il := discipline.NewIronLawChecker()
	pb := discipline.NewPromptBuilder()

	svc := appphase.New(appphase.Deps{
		ChangeRepo:      h.changeRepo,
		PhaseRepo:       h.phaseRepo,
		SessionRepo:     h.sessRepo,
		Governance:      h.governance,
		Memory:          h.memory,
		Dispatcher:      h.dispatcher,
		SpawnGov:        h.spawn,
		Validator:       val,
		IronLaw:         il,
		Prompts:         pb,
		Audit:           h.audit,
		Events:          h.events,
		Clock:           clock,
		IDGen:           idGen,
		Scheduler:       appphase.SyncScheduler,
		OutboxEnqueuer:  completion,
		WebhookNotifier: notifier,
	})

	_, err := svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseArchive,
		TaskDescription: "archive the change",
		RetryBudget:     3,
	})
	require.NoError(t, err)

	completion.mu.Lock()
	defer completion.mu.Unlock()
	require.Equal(t, 1, completion.calls, "exactly one completion-tx enqueue")
	require.NotNil(t, completion.event)
	assert.Equal(t, outbox.EventPhaseArchived, completion.event.EventType())
	assert.Equal(t, outbox.StatusPending, completion.event.Status())
	assert.Equal(t, 0, completion.event.Attempts())

	// Payload byte-identical to PhaseArchivedPayload.
	want, _ := json.Marshal(inbound.PhaseArchivedPayload{
		ChangeID:   cid.String(),
		ChangeName: "archive-test",
		PhaseType:  string(phase.PhaseArchive),
		ArchivedAt: clock.Now(),
	})
	assert.Equal(t, want, completion.event.Payload())

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	assert.Equal(t, 0, notifier.called, "legacy fire-and-forget Notify must NOT be called")

	// Loop-hardening D-LH-1: the change-row write and the outbox enqueue must
	// land in ONE transaction. On the archive path with an enqueuer wired,
	// ChangeRepo.Save must NOT be called separately — only
	// SaveCompletedWithOutbox writes the (now-completed) change row. A separate
	// pre-save would reopen a death-between-writes window that drops the event.
	h.changeRepo.mu.Lock()
	defer h.changeRepo.mu.Unlock()
	assert.Equal(t, 0, h.changeRepo.saveCalls,
		"archive path must not call ChangeRepo.Save; the completion tx writes the change row")
}
