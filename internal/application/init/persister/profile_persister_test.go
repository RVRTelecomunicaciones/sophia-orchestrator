package persister_test

// profile_persister_test.go — PP.1–PP.4 (Strict TDD)
//
// Tests for ProfileMemoryPersister.PersistProfile:
//   PP.1 Ingest called with Type="convention_profile" and correct topic key.
//   PP.2 Ingest called with JSON payload containing ProjectID and Framework.
//   PP.3 Ingest error is surfaced to caller (non-nil error returned).
//   PP.4 Compile-time: ProfileMemoryPersister satisfies initphase.ProfilePersister.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/persister"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// PP.4: compile-time assertion.
var _ initphase.ProfilePersister = (*persister.ProfileMemoryPersister)(nil)

// capturingMemoryClient records the most recent Ingest call arguments.
type capturingMemoryClient struct {
	mu        sync.Mutex
	lastInput outbound.IngestMemoryInput
	calls     int
	err       error
}

func (c *capturingMemoryClient) Ingest(_ context.Context, in outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastInput = in
	if c.err != nil {
		return nil, c.err
	}
	return &outbound.MemoryRecord{TopicKey: in.TopicKey}, nil
}

func (c *capturingMemoryClient) Get(_ context.Context, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (c *capturingMemoryClient) GetByTopicKey(_ context.Context, _ outbound.MemoryScope, _ string) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (c *capturingMemoryClient) Archive(_ context.Context, _, _, _ string) error { return nil }
func (c *capturingMemoryClient) Search(_ context.Context, _ outbound.SearchQuery) (*outbound.SearchResults, error) {
	return nil, nil
}
func (c *capturingMemoryClient) BuildContext(_ context.Context, _ outbound.ContextRequest) (*outbound.ContextBundle, error) {
	return nil, nil
}
func (c *capturingMemoryClient) RecordDecision(_ context.Context, _ outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	return nil, nil
}
func (c *capturingMemoryClient) RecordRelation(_ context.Context, _ outbound.RecordRelationInput) error {
	return nil
}

// makeProfileForPP builds a minimal valid ConventionProfile for persister tests.
func makeProfileForPP(t *testing.T, projectID, framework string) convention.ConventionProfile {
	t.Helper()
	clock := shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	p, err := convention.NewConventionProfile(
		projectID,
		framework,
		"11",
		clock,
		[]convention.PatternEntry{
			{
				Pattern:    "nestjs-extends-crudservice",
				Source:     convention.SourceDetectedFromCode,
				Confidence: 0.85,
				Evidence:   []string{"motivo/motivo.service.ts"},
				Rule:       "Services MUST extend CrudService.",
			},
		},
	)
	require.NoError(t, err)
	return *p
}

// PP.1: Ingest called with Type="convention_profile" and topic key "convention/<projectID>/<framework>".
func TestProfileMemoryPersister_IngestArgs(t *testing.T) {
	client := &capturingMemoryClient{}
	pp := persister.NewProfileMemoryPersister(client, nil, "tenant-1", "dev")

	profile := makeProfileForPP(t, "cajachica", "nestjs")
	err := pp.PersistProfile(context.Background(), profile)

	require.NoError(t, err)
	require.Equal(t, 1, client.calls, "Ingest must be called exactly once")

	client.mu.Lock()
	in := client.lastInput
	client.mu.Unlock()

	require.Equal(t, "convention_profile", in.Type, "Type must be convention_profile")
	require.Equal(t, "convention/cajachica/nestjs", in.TopicKey, "TopicKey must follow convention/<projectID>/<framework>")
}

// PP.2: JSON payload contains ProjectID and Framework from the profile.
func TestProfileMemoryPersister_PayloadContent(t *testing.T) {
	client := &capturingMemoryClient{}
	pp := persister.NewProfileMemoryPersister(client, nil, "tenant-1", "dev")

	profile := makeProfileForPP(t, "cajachica", "nestjs")
	require.NoError(t, pp.PersistProfile(context.Background(), profile))

	client.mu.Lock()
	content := client.lastInput.Content
	client.mu.Unlock()

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(content), &payload))
	require.Equal(t, "cajachica", payload["project_id"])
	require.Equal(t, "nestjs", payload["framework"])
	require.NotEmpty(t, payload["detected_at"], "detected_at must be set")
	patterns, ok := payload["patterns"].([]interface{})
	require.True(t, ok, "patterns must be an array")
	require.Len(t, patterns, 1, "one pattern expected in payload")
}

// PP.3: Ingest error is returned to the caller (non-fatal contract: caller logs WARN).
func TestProfileMemoryPersister_IngestError_Surfaced(t *testing.T) {
	ingestErr := errors.New("memory-engine: 503")
	client := &capturingMemoryClient{err: ingestErr}
	pp := persister.NewProfileMemoryPersister(client, nil, "tenant-1", "dev")

	profile := makeProfileForPP(t, "proj", "nestjs")
	err := pp.PersistProfile(context.Background(), profile)

	require.Error(t, err, "Ingest error must be surfaced to caller")
	require.True(t, strings.Contains(err.Error(), "memory-engine: 503"),
		"error must wrap the original Ingest error")
}

// PP.4 is the compile-time var above; no runtime assertion needed.
func TestProfileMemoryPersister_SatisfiesPort(_ *testing.T) {
	// satisfied by the var _ at package level.
}
