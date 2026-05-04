package db

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // register postgres driver
	_ "github.com/golang-migrate/migrate/v4/source/file"       // register file source
)

// MigrateUp applies all pending migrations from sourceDir to the database
// reachable at databaseURL. Returns nil if no migrations were needed.
func MigrateUp(sourceDir, databaseURL string) error {
	m, err := migrate.New("file://"+sourceDir, databaseURL)
	if err != nil {
		return fmt.Errorf("db.migrations: open: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db.migrations: up: %w", err)
	}
	return nil
}

// MigrateDown rolls back exactly one migration step. For test isolation only.
func MigrateDown(sourceDir, databaseURL string, steps int) error {
	m, err := migrate.New("file://"+sourceDir, databaseURL)
	if err != nil {
		return fmt.Errorf("db.migrations: open: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()
	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db.migrations: down: %w", err)
	}
	return nil
}
