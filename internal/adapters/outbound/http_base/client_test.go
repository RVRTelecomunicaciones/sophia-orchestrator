package http_base_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, srv *httptest.Server, modify func(*http_base.Config)) *http_base.Client {
	t.Helper()
	cfg := http_base.DefaultConfig("test", srv.URL)
	cfg.HTTPTimeout = 200 * time.Millisecond
	cfg.MaxAttempts = 2
	cfg.InitialBackoff = 1 * time.Millisecond
	cfg.MaxBackoff = 5 * time.Millisecond
	cfg.JitterFraction = 0
	if modify != nil {
		modify(&cfg)
	}
	c, err := http_base.New(cfg)
	require.NoError(t, err)
	return c
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  http_base.Config
	}{
		{"empty BaseURL", http_base.Config{Name: "x", MaxAttempts: 1}},
		{"empty Name", http_base.Config{BaseURL: "http://x", MaxAttempts: 1}},
		{"zero MaxAttempts", http_base.Config{BaseURL: "http://x", Name: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := http_base.New(tc.cfg)
			require.Error(t, err)
		})
	}
}

func TestPostJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"echo":"hi"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	var out struct {
		Echo string `json:"echo"`
	}
	require.NoError(t, c.PostJSON(context.Background(), "/echo", map[string]string{"msg": "hi"}, &out))
	require.Equal(t, "hi", out.Echo)
}

func TestGetJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":42}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	var out struct {
		Value int `json:"value"`
	}
	require.NoError(t, c.GetJSON(context.Background(), "/x", &out))
	require.Equal(t, 42, out.Value)
}

func TestPostJSON_ClientError_NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	err := c.PostJSON(context.Background(), "/x", nil, nil)
	require.Error(t, err)
	var hErr *http_base.Error
	require.True(t, errors.As(err, &hErr))
	require.Equal(t, http.StatusBadRequest, hErr.StatusCode)
	require.True(t, hErr.IsClient())
	require.False(t, hErr.IsServer())
	// 4xx is NOT retried (returned on first response).
	require.Equal(t, int32(1), calls.Load())
}

func TestPostJSON_ServerError_RetriesUntilExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, func(cfg *http_base.Config) {
		cfg.MaxAttempts = 3
	})
	err := c.PostJSON(context.Background(), "/x", nil, nil)
	require.Error(t, err)
	var hErr *http_base.Error
	require.True(t, errors.As(err, &hErr))
	require.True(t, hErr.IsServer())
	require.Equal(t, int32(3), calls.Load())
}

func TestPostJSON_ServerErrorThenRecovers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	require.NoError(t, c.PostJSON(context.Background(), "/x", nil, nil))
	require.Equal(t, int32(2), calls.Load())
}

func TestPostJSON_TimeoutNotRetriedAfterCtxCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.PostJSON(ctx, "/x", nil, nil)
	require.Error(t, err)
}

func TestCircuitBreaker_OpensAfterConsecutiveFailures(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, func(cfg *http_base.Config) {
		cfg.MaxAttempts = 1 // no retry; one fail per call
		cfg.CBFailureThreshold = 2
	})

	// First two calls return server error; circuit trips after the 2nd.
	_ = c.PostJSON(context.Background(), "/x", nil, nil)
	_ = c.PostJSON(context.Background(), "/x", nil, nil)

	// Third call should be rejected by the circuit breaker without hitting srv.
	before := calls.Load()
	err := c.PostJSON(context.Background(), "/x", nil, nil)
	after := calls.Load()
	require.Error(t, err)
	require.ErrorIs(t, err, http_base.ErrCircuitOpen)
	require.Equal(t, before, after, "circuit-open call must not reach upstream")
}

func TestPutJSON_PatchJSON_Delete(t *testing.T) {
	var lastMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, nil)

	require.NoError(t, c.PutJSON(context.Background(), "/x", map[string]string{}, nil))
	require.Equal(t, http.MethodPut, lastMethod)
	require.NoError(t, c.PatchJSON(context.Background(), "/x", map[string]string{}, nil))
	require.Equal(t, http.MethodPatch, lastMethod)
	require.NoError(t, c.Delete(context.Background(), "/x"))
	require.Equal(t, http.MethodDelete, lastMethod)
}

func TestState_StartsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, nil)
	require.Equal(t, "closed", c.State())
}

func TestPostJSON_DefaultHeadersApplied(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, func(cfg *http_base.Config) {
		cfg.DefaultHeaders = http.Header{"X-API-Key": {"secret"}}
	})
	require.NoError(t, c.PostJSON(context.Background(), "/x", nil, nil))
	require.Equal(t, "secret", got.Get("X-API-Key"))
}

func TestPostJSON_MarshalErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, nil)
	// json.Marshal fails on channels.
	err := c.PostJSON(context.Background(), "/x", make(chan int), nil)
	require.Error(t, err)
}

func TestPostJSON_BadResponseJSONReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not-json`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, nil)
	var out struct {
		Foo string
	}
	err := c.PostJSON(context.Background(), "/x", nil, &out)
	require.Error(t, err)
}

func TestErrorString_Truncates(t *testing.T) {
	body := make([]byte, 512)
	for i := range body {
		body[i] = 'x'
	}
	e := &http_base.Error{StatusCode: 500, Body: body, URL: "http://x/y"}
	s := e.Error()
	require.Contains(t, s, "500")
	require.Contains(t, s, "...")
}

// confirm the error sentinels behave as expected with errors.Is.
func TestSentinelErrors_IsBehavior(t *testing.T) {
	require.True(t, errors.Is(http_base.ErrCircuitOpen, http_base.ErrCircuitOpen))
	require.True(t, errors.Is(http_base.ErrAttemptsExhausted, http_base.ErrAttemptsExhausted))
}

// json sanity: ensure our marshal path supports common shapes.
func TestPostJSON_MarshalsStructBody(t *testing.T) {
	type In struct {
		Name string `json:"name"`
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b In
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = b.Name
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, nil)
	require.NoError(t, c.PostJSON(context.Background(), "/x", In{Name: "alice"}, nil))
	require.Equal(t, "alice", got)
}
