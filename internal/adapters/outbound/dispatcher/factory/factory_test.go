package factory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/factory"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// stubDispatcher is a minimal AgentDispatcher that records the last call
// it received and returns a tag identifying which instance handled it.
type stubDispatcher struct {
	tag      string
	provider session.Provider
	suggest  int
	last     outbound.DispatchRequest
	calls    int
}

func (s *stubDispatcher) Provider() session.Provider           { return s.provider }
func (s *stubDispatcher) SuggestedMaxConcurrent() int          { return s.suggest }
func (s *stubDispatcher) HealthCheck(_ context.Context) error  { return nil }
func (s *stubDispatcher) Dispatch(_ context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	s.last = req
	s.calls++
	return &outbound.DispatchResult{Stdout: []byte(s.tag)}, nil
}

func newStub(tag string) *stubDispatcher {
	return &stubDispatcher{tag: tag, provider: session.ProviderOpenCode, suggest: 4}
}

// --- Factory ---

func TestFactory_DefaultRegistered(t *testing.T) {
	d := newStub("default")
	f := factory.New("opencode", d)

	require.Same(t, d, f.Default())
	require.ElementsMatch(t, []string{"opencode"}, f.Providers())
}

func TestFactory_RegisterAndGet(t *testing.T) {
	def := newStub("opencode")
	other := newStub("aider")
	f := factory.New("opencode", def)
	f.Register("aider", other)

	got, err := f.Get("aider")
	require.NoError(t, err)
	require.Same(t, other, got)

	require.ElementsMatch(t, []string{"opencode", "aider"}, f.Providers())
}

func TestFactory_GetUnknownReturnsTypedError(t *testing.T) {
	f := factory.New("opencode", newStub("default"))

	_, err := f.Get("nope")
	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrUnknownDispatcherProvider),
		"err must wrap ErrUnknownDispatcherProvider, got: %v", err)
}

func TestFactory_DuplicateRegisterReplaces(t *testing.T) {
	first := newStub("first")
	second := newStub("second")
	f := factory.New("p", first)
	f.Register("p", second)

	got, err := f.Get("p")
	require.NoError(t, err)
	require.Same(t, second, got, "last Register wins")
}

func TestFactory_NewPanicsOnEmptyName(t *testing.T) {
	require.Panics(t, func() {
		factory.New("", newStub("x"))
	})
}

func TestFactory_NewPanicsOnNilDispatcher(t *testing.T) {
	require.Panics(t, func() {
		factory.New("opencode", nil)
	})
}

// --- WrappingDispatcher ---

func TestWrappingDispatcher_NoOverridesUsesDefault(t *testing.T) {
	def := newStub("default")
	f := factory.New("opencode", def)
	w := factory.NewWrappingDispatcher(f, nil)

	_, err := w.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "p", PhaseType: "explore",
	})
	require.NoError(t, err)
	require.Equal(t, 1, def.calls, "default adapter must receive the call when no override matches")
}

func TestWrappingDispatcher_PerPhaseProviderRoutes(t *testing.T) {
	def := newStub("opencode")
	aider := newStub("aider")
	f := factory.New("opencode", def)
	f.Register("aider", aider)

	w := factory.NewWrappingDispatcher(f, map[string]string{
		"apply": "aider",
	})

	// apply → aider (override matches)
	_, err := w.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "p", PhaseType: "apply",
	})
	require.NoError(t, err)
	require.Equal(t, 0, def.calls, "default adapter must NOT be called when override matches")
	require.Equal(t, 1, aider.calls, "aider adapter must be called for apply override")

	// spec → opencode (no override for spec)
	_, err = w.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "p", PhaseType: "spec",
	})
	require.NoError(t, err)
	require.Equal(t, 1, def.calls, "default adapter must be called when no override for phase")
	require.Equal(t, 1, aider.calls, "aider adapter not called again")
}

func TestWrappingDispatcher_UnknownProviderFallsBackToDefault(t *testing.T) {
	def := newStub("opencode")
	f := factory.New("opencode", def)

	// Operator typo — provider name "ader" instead of "aider".
	w := factory.NewWrappingDispatcher(f, map[string]string{
		"apply": "ader",
	})

	_, err := w.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "p", PhaseType: "apply",
	})
	require.NoError(t, err, "typo in provider name must NOT break the phase")
	require.Equal(t, 1, def.calls, "fallback to default adapter")
}

func TestWrappingDispatcher_EmptyPhaseTypeUsesDefault(t *testing.T) {
	def := newStub("opencode")
	other := newStub("aider")
	f := factory.New("opencode", def)
	f.Register("aider", other)

	w := factory.NewWrappingDispatcher(f, map[string]string{
		"apply": "aider",
	})

	// Backward-compat: pre-existing callers that don't set PhaseType
	// must keep getting the default adapter.
	_, err := w.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "p",
	})
	require.NoError(t, err)
	require.Equal(t, 1, def.calls)
	require.Equal(t, 0, other.calls)
}

func TestWrappingDispatcher_HealthCheckDelegatesToDefault(t *testing.T) {
	def := newStub("opencode")
	other := newStub("aider")
	f := factory.New("opencode", def)
	f.Register("aider", other)
	w := factory.NewWrappingDispatcher(f, nil)

	require.NoError(t, w.HealthCheck(context.Background()))
	// Note: HealthCheck on stub returns nil and doesn't increment calls;
	// this test asserts no panic + no error rather than which adapter ran.
}

func TestWrappingDispatcher_NilFactoryPanics(t *testing.T) {
	require.Panics(t, func() {
		factory.NewWrappingDispatcher(nil, nil)
	})
}
