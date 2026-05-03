package outbound

import (
	"context"
	"time"
)

// RuntimeClient is the outbound port for sophia-runtime-adapters.
// V1 capabilities used by sophia-orchestator:
//   - shell.exec@v1            — used to launch the OpenCode dispatcher subprocess
//   - filesystem.write@v1     — write prompt files / drop drafts in worktrees
//   - filesystem.read@v1      — read agent outputs
//   - git.worktree.create@v1  — create per-Group worktree (Phase 2 capability)
//   - git.worktree.remove@v1  — cleanup worktree on success
//   - git.commit@v1           — commit implement-agent changes
//   - git.merge@v1            — merge group worktree into change branch
//   - lock.acquire@v1         — file_reserve probe + lock (Phase 2)
//   - lock.release@v1
//   - mailbox.send@v1         — msg_broadcast / msg_request (Phase 2)
//   - mailbox.read_inbox@v1
//   - tb.create_board@v1      — task board (Phase 2; alternative if we move
//                                board to runtime in V2; V1 keeps it in
//                                orchestrator Postgres per ADR Approach C)
//
// All capabilities are invoked via Execute with a typed payload.
type RuntimeClient interface {
	Execute(ctx context.Context, req ExecutionRequest) (*ExecutionReceipt, error)
}

// ExecutionRequest is the request shape for runtime.Execute.
type ExecutionRequest struct {
	Capability     string // e.g. "shell.exec@v1"
	Payload        []byte // capability-specific JSON
	TimeoutMS      int
	IdempotencyKey string // optional; runtime replay-everything semantics
}

// ExecutionReceipt is the canonical runtime result. Mirrors the runtime-
// adapters domain envelope (closed status enum).
type ExecutionReceipt struct {
	Status     ReceiptStatus
	Stdout     []byte
	Stderr     []byte
	ExitCode   int
	DurationMS int
	ReceiptID  string
	RetryHint  RetryHint
	StartedAt  time.Time
	EndedAt    time.Time
}

// ReceiptStatus is the runtime-adapters closed status enum (R15).
type ReceiptStatus string

// Receipt statuses.
const (
	ReceiptSuccess   ReceiptStatus = "success"
	ReceiptFailure   ReceiptStatus = "failure"
	ReceiptTimeout   ReceiptStatus = "timeout"
	ReceiptCancelled ReceiptStatus = "cancelled"
	ReceiptPartial   ReceiptStatus = "partial"
)

// RetryHint is the runtime-adapters signal: the runtime classifies, the
// caller decides (D1.5 in runtime spec).
type RetryHint string

// Retry hints.
const (
	RetryRetryable    RetryHint = "retryable"
	RetryNonRetryable RetryHint = "non_retryable"
	RetryUnknown      RetryHint = "unknown"
)
