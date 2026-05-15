// Package factory implements outbound.DispatcherFactory and the
// FactoryWrappingDispatcher that lets phase.Service and apply.RunService
// keep talking to a single AgentDispatcher while the request gets
// routed to the correct provider per-phase.
//
// V2.0 ships with one registered provider ("opencode"). Future versions
// register more adapters (e.g. "aider", "ollama") via Register without
// touching the call sites in service.go / teamlead.go.
package factory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Factory implements outbound.DispatcherFactory backed by an in-memory
// map of provider-name → AgentDispatcher. Concurrent-safe.
type Factory struct {
	mu       sync.RWMutex
	adapters map[string]outbound.AgentDispatcher
	defName  string
}

// New constructs a Factory with the given default provider name and a
// mandatory default dispatcher. Additional adapters are added via
// Register before the factory is exposed to consumers (registration
// is intentionally not safe-for-use after Default() is consumed).
//
// defName MUST match a registered provider; if defaultDispatcher is nil
// or defName is empty, New panics — these are programmer errors that
// must surface at boot, not at first request.
func New(defName string, defaultDispatcher outbound.AgentDispatcher) *Factory {
	if defName == "" {
		panic("factory.New: default provider name is required")
	}
	if defaultDispatcher == nil {
		panic("factory.New: default dispatcher is required")
	}
	return &Factory{
		adapters: map[string]outbound.AgentDispatcher{defName: defaultDispatcher},
		defName:  defName,
	}
}

// Register adds an adapter under the given provider name. If the name
// already has an adapter, Register replaces it (last-write-wins; useful
// for tests). Names are case-sensitive and follow lowercase convention
// ("opencode", "aider", "ollama").
func (f *Factory) Register(provider string, adapter outbound.AgentDispatcher) {
	if provider == "" || adapter == nil {
		panic("factory.Register: provider name and adapter required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adapters[provider] = adapter
}

// Get implements outbound.DispatcherFactory.
func (f *Factory) Get(provider string) (outbound.AgentDispatcher, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if a, ok := f.adapters[provider]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("%w: %q (registered: %v)",
		outbound.ErrUnknownDispatcherProvider, provider, f.providersLocked())
}

// Default implements outbound.DispatcherFactory.
func (f *Factory) Default() outbound.AgentDispatcher {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.adapters[f.defName]
}

// Providers implements outbound.DispatcherFactory.
func (f *Factory) Providers() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.providersLocked()
}

func (f *Factory) providersLocked() []string {
	out := make([]string, 0, len(f.adapters))
	for k := range f.adapters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// WrappingDispatcher is the AgentDispatcher injected into phase.Service
// and apply.RunService. It implements the AgentDispatcher interface but
// internally resolves the actual adapter per Dispatch call by consulting
// the Factory based on req.PhaseType + ProviderByPhase config.
//
// This keeps the call sites in service.go and teamlead.go IDENTICAL —
// they keep calling s.d.Dispatcher.Dispatch(ctx, req). The routing
// decision lives entirely inside the wrapper.
type WrappingDispatcher struct {
	factory         outbound.DispatcherFactory
	providerByPhase map[string]string
}

// NewWrappingDispatcher builds a wrapper that routes Dispatch calls to
// providerByPhase[req.PhaseType] when set, or to factory.Default()
// otherwise. providerByPhase nil/empty means "always use default" — this
// is the V1 backward-compat path.
func NewWrappingDispatcher(factory outbound.DispatcherFactory, providerByPhase map[string]string) *WrappingDispatcher {
	if factory == nil {
		panic("factory.NewWrappingDispatcher: factory is required")
	}
	return &WrappingDispatcher{
		factory:         factory,
		providerByPhase: providerByPhase,
	}
}

// Provider returns the default adapter's Provider value. Used by
// session.New when recording which agent ran the phase. Per-request
// adapter Provider differences are NOT exposed here — sessions record
// the *intent* (which dispatcher was wired at boot), not the actual
// route taken on a given call. If V2.1 needs per-request session
// provenance, add a separate field to DispatchResult.
func (w *WrappingDispatcher) Provider() session.Provider {
	return w.factory.Default().Provider()
}

// SuggestedMaxConcurrent returns the default adapter's suggestion.
func (w *WrappingDispatcher) SuggestedMaxConcurrent() int {
	return w.factory.Default().SuggestedMaxConcurrent()
}

// HealthCheck delegates to the default adapter.
func (w *WrappingDispatcher) HealthCheck(ctx context.Context) error {
	return w.factory.Default().HealthCheck(ctx)
}

// Dispatch resolves the provider for req.PhaseType and delegates. If
// the provider is unknown OR no per-phase override matches, falls back
// to the factory's Default — never errors out at routing time, because
// the operator might typo an env var and we don't want that to break
// every phase.
func (w *WrappingDispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	adapter := w.adapterFor(req.PhaseType)
	return adapter.Dispatch(ctx, req)
}

// adapterFor returns the AgentDispatcher to use for the given phase.
// Lookup order: providerByPhase[phaseType] → factory.Default().
// An unknown provider name falls back to default with no error (typo
// tolerance); the misconfig becomes visible in operator-facing
// healthcheck logs at startup, not at runtime.
func (w *WrappingDispatcher) adapterFor(phaseType string) outbound.AgentDispatcher {
	if phaseType != "" {
		if name, ok := w.providerByPhase[phaseType]; ok && name != "" {
			if a, err := w.factory.Get(name); err == nil {
				return a
			}
		}
	}
	return w.factory.Default()
}
