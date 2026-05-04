// Package db provides Postgres connection helpers (pgxpool) and migration
// runners (golang-migrate). The pool is shared across all pg adapters.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config tunes the pgx pool.
type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	ConnectTimeout  time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig(url string) Config {
	return Config{
		URL:             url,
		MaxConns:        16,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		ConnectTimeout:  5 * time.Second,
	}
}

// Open creates a pgxpool.Pool, applies the configured tuning, and verifies
// connectivity with a ping. Caller is responsible for Close().
func Open(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	if cfg.URL == "" {
		return nil, errors.New("db: empty Postgres URL")
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("db: parse pg url: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}

	connectCtx := ctx
	if cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		connectCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer cancel()
	}

	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// AdvisoryLock acquires a Postgres session-level advisory lock keyed by the
// FNV-style hash of name. Returns a release function. Used by the SpawnGovernor
// and per-Change phase mutex to coordinate across orchestrator instances.
func AdvisoryLock(ctx context.Context, pool *pgxpool.Pool, name string) (func(context.Context) error, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquire conn: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", name); err != nil {
		conn.Release()
		return nil, fmt.Errorf("db: advisory_lock: %w", err)
	}
	release := func(rctx context.Context) error {
		defer conn.Release()
		if _, err := conn.Exec(rctx, "SELECT pg_advisory_unlock(hashtext($1))", name); err != nil {
			return fmt.Errorf("db: advisory_unlock: %w", err)
		}
		return nil
	}
	return release, nil
}
