package webhook

import (
	"context"

	phaseapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
)

// PhaseBridge wraps Adapter and implements phase.WebhookNotifier.
// It converts phase.PhaseArchivedWebhookPayload → webhook.PhaseArchivedWebhookPayload,
// keeping the adapter layer free of application-layer type dependencies.
type PhaseBridge struct {
	adapter *Adapter
}

// NewPhaseBridge constructs a PhaseBridge around the given Adapter.
func NewPhaseBridge(a *Adapter) *PhaseBridge {
	return &PhaseBridge{adapter: a}
}

// Notify implements phase.WebhookNotifier by bridging the payload types.
func (b *PhaseBridge) Notify(ctx context.Context, p phaseapp.PhaseArchivedWebhookPayload) {
	b.adapter.Notify(ctx, PhaseArchivedWebhookPayload{
		ChangeID:   p.ChangeID,
		ChangeName: p.ChangeName,
		PhaseType:  p.PhaseType,
		ArchivedAt: p.ArchivedAt,
	})
}

// Verify PhaseBridge satisfies phase.WebhookNotifier at compile time.
var _ phaseapp.WebhookNotifier = (*PhaseBridge)(nil)
