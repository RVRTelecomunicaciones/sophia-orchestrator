// Package pg implements outbound repository ports against Postgres via pgx/v5.
// All repos accept a *pgxpool.Pool; transaction boundaries are managed by
// the application layer (or with explicit Begin/Commit blocks here when
// needed for advisory locks + reads/writes).
package pg

import (
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// wrapErr maps pgx errors to canonical outbound sentinels.
func wrapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, outbound.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isUniqueViolation reports whether err is a Postgres 23505 unique violation.
//
//nolint:unused // exported via internal helper; used by future repos.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// nullableInt converts a *int to a sql-friendly value (nil when nil).
func nullableInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

// mustTime returns a zero-value time.Time used as a scan placeholder.
func mustTime() time.Time { return time.Time{} }
