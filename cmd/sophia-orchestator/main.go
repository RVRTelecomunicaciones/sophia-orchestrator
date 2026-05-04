// Command sophia-orchestator is the deterministic SDD workflow coordinator.
// It boots the HTTP server, wires all dependencies, and runs until SIGINT
// or SIGTERM is received.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sophia-orchestator exited with error", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := bootstrap.Wire(ctx, cfg)
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}
	defer app.Close()

	return app.Run(ctx)
}
