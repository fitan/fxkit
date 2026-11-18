package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticChecker 是可控的 HealthChecker 假实现。
type staticChecker struct {
	name string
	err  error
}

func (c staticChecker) Name() string { return c.name }
func (c staticChecker) Check(context.Context) error {
	return c.err
}

func newHealthMux(checkers ...HealthChecker) *chi.Mux {
	mux := chi.NewRouter()
	if err := RegisterHealth(HealthParams{Mux: mux, Checkers: checkers}); err != nil {
		panic(err)
	}
	return mux
}

func doGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealth_LivenessAlwaysOK(t *testing.T) {
	mux := newHealthMux(staticChecker{name: "db", err: errors.New("down")})
	rec := doGet(t, mux, "/healthz")
	assert.Equal(t, http.StatusOK, rec.Code, "liveness must not depend on dependencies")
}

func TestReadyz_AllHealthy(t *testing.T) {
	mux := newHealthMux(
		staticChecker{name: "db"},
		staticChecker{name: "cache"},
	)
	rec := doGet(t, mux, "/readyz")
	require.Equal(t, http.StatusOK, rec.Code)

	var body healthReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, map[string]string{"db": "ok", "cache": "ok"}, body.Checks)
}

func TestReadyz_OneFailingReturns503(t *testing.T) {
	mux := newHealthMux(
		staticChecker{name: "db", err: errors.New("connection refused")},
		staticChecker{name: "cache"},
	)
	rec := doGet(t, mux, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body healthReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body.Status)
	assert.Contains(t, body.Checks["db"], "connection refused")
	assert.Equal(t, "ok", body.Checks["cache"], "healthy checks still reported")
}

func TestReadyz_NoCheckers(t *testing.T) {
	mux := newHealthMux()
	rec := doGet(t, mux, "/readyz")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// slowChecker 用于验证聚合超时。
type slowChecker struct{ delay time.Duration }

func (c slowChecker) Name() string { return "slow" }
func (c slowChecker) Check(ctx context.Context) error {
	select {
	case <-time.After(c.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestReadyz_RespectsTimeout(t *testing.T) {
	mux := chi.NewRouter()
	require.NoError(t, RegisterHealth(HealthParams{
		Mux:      mux,
		Checkers: []HealthChecker{slowChecker{delay: 5 * time.Second}},
		Timeout:  50 * time.Millisecond,
	}))

	start := time.Now()
	rec := doGet(t, mux, "/readyz")
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Less(t, elapsed, 2*time.Second, "readyz must honor its aggregate timeout")
}

// TestReadyz_CheckersRunConcurrently 并发检查：3 个 200ms 的检查应在 ~200ms 内完成。
func TestReadyz_CheckersRunConcurrently(t *testing.T) {
	mux := chi.NewRouter()
	require.NoError(t, RegisterHealth(HealthParams{
		Mux: mux,
		Checkers: []HealthChecker{
			slowChecker{delay: 200 * time.Millisecond},
			slowChecker{delay: 200 * time.Millisecond},
			slowChecker{delay: 200 * time.Millisecond},
		},
		Timeout: 2 * time.Second,
	}))

	start := time.Now()
	rec := doGet(t, mux, "/readyz")
	elapsed := time.Since(start)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Less(t, elapsed, 500*time.Millisecond, "checkers should run concurrently, took %v", elapsed)
}

func TestVersion_Route(t *testing.T) {
	mux := newHealthMux()
	rec := doGet(t, mux, "/version")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestRegisterHealth_NilMuxReturnsError(t *testing.T) {
	assert.Error(t, RegisterHealth(HealthParams{}))
}
