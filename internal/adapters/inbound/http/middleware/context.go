package middleware

import "context"

type ctxKey int

const (
	keyProject ctxKey = iota
)

// WithProject returns a derived context carrying the resolved project name.
func WithProject(ctx context.Context, project string) context.Context {
	return context.WithValue(ctx, keyProject, project)
}

// ProjectFromContext extracts the project name set by APIKey middleware.
// Returns "" if not set.
func ProjectFromContext(ctx context.Context) string {
	v, _ := ctx.Value(keyProject).(string)
	return v
}
