package trace

import "context"

// ctxKey is an unexported type for the trace context key.
// Using a typed key prevents collisions with other packages.
type ctxKey struct{}

// NewContext returns a new context carrying t. Downstream handlers and
// outbound adapters retrieve it with FromContext.
func NewContext(ctx context.Context, t Trace) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromContext retrieves the Trace stored by NewContext.
// Returns the zero Trace and false if none is present.
func FromContext(ctx context.Context) (Trace, bool) {
	t, ok := ctx.Value(ctxKey{}).(Trace)
	return t, ok
}

// ChildSpan creates a child context and a new Trace that shares the same
// TraceID as the parent but has a fresh random SpanID. This is the
// single call-site pattern for outbound HTTP adapters:
//
//	childCtx, child, err := trace.ChildSpan(ctx, rand)
//	// set Traceparent: child.String() on the outbound request
//
// If no Trace is found in ctx a fresh top-level Trace is generated instead,
// which prevents propagation failures from silently losing correlation.
func ChildSpan(ctx context.Context, rand interface{ Read([]byte) (int, error) }) (context.Context, Trace, error) {
	parent, ok := FromContext(ctx)
	var child Trace
	var err error
	if ok {
		child, err = parent.WithNewSpan(rand)
	} else {
		child, err = New(rand)
	}
	if err != nil {
		return ctx, Trace{}, err
	}
	return NewContext(ctx, child), child, nil
}
