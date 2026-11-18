package idempotency_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fitan/fxkit/authz"
	"github.com/fitan/fxkit/gormx"
	"github.com/fitan/fxkit/idempotency"
)

func testStore(t *testing.T) *idempotency.DBStore {
	store, _ := testStoreWithClient(t)
	return store
}

func testStoreWithClient(t *testing.T) (*idempotency.DBStore, *gormx.Client) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "idem.db")
	client, err := gormx.New(&gormx.Config{Driver: "sqlite", DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	store := idempotency.NewDBStore(client)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, client
}

func testConfig() idempotency.Config {
	return idempotency.Config{
		Enabled:      true,
		Header:       "Idempotency-Key",
		TTL:          time.Hour,
		Methods:      []string{"POST"},
		MaxBodyBytes: 1 << 20,
		Store:        "db",
	}
}

func TestMiddlewareScopesKeyByPathAndSubject(t *testing.T) {
	store := testStore(t)
	calls := 0
	h := idempotency.Middleware(testConfig(), store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))

	do := func(path, subject, key string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", key)
		if subject != "" {
			req = req.WithContext(authz.WithSubject(req.Context(), authz.Subject{ID: subject}))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do("/a", "alice", "same"); code != http.StatusOK {
		t.Fatalf("first code=%d", code)
	}
	// 同路径、同主体、同 key：重放，不再执行。
	if code := do("/a", "alice", "same"); code != http.StatusOK {
		t.Fatalf("replay code=%d", code)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
	// 不同路径：独立作用域。
	do("/b", "alice", "same")
	// 不同主体：独立作用域。
	do("/a", "bob", "same")
	if calls != 3 {
		t.Fatalf("calls=%d, want 3 (path/subject scoped)", calls)
	}
}

func TestMiddlewareReplaysAndRejectsDifferentBody(t *testing.T) {
	store := testStore(t)
	calls := 0
	h := idempotency.Middleware(testConfig(), store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("first status=%d calls=%d", rec.Code, calls)
	}

	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req.Header.Set("Idempotency-Key", "k1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("replay status=%d calls=%d", rec.Code, calls)
	}
	if rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("missing Idempotent-Replayed")
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("replayed body=%q", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":2}`))
	req.Header.Set("Idempotency-Key", "k1")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("different body status=%d, want 409", rec.Code)
	}
}

func TestMiddlewareAbortsOn5xx(t *testing.T) {
	store := testStore(t)
	calls := 0
	h := idempotency.Middleware(testConfig(), store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", "k2")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d", rec.Code)
		}
	}
	if calls != 2 {
		t.Fatalf("5xx must not be cached; calls=%d", calls)
	}
}

func TestMiddlewareInProgress(t *testing.T) {
	store := testStore(t)
	if _, created, err := store.Begin(context.Background(), idempotency.ScopeKey(http.MethodPost, "/x", "", "k3"), "other", time.Hour); err != nil || !created {
		t.Fatalf("begin created=%v err=%v", created, err)
	}
	h := idempotency.Middleware(testConfig(), store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "k3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", rec.Code)
	}
}

func TestPurgeExpired(t *testing.T) {
	store, client := testStoreWithClient(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := []idempotency.KeyRecord{
		{Key: "expired", RequestHash: "h", ExpiresAt: now.Add(-time.Hour)},
		{Key: "active", RequestHash: "h", ExpiresAt: now.Add(time.Hour)},
	}
	for i := range rows {
		if err := client.Conn(ctx).Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.PurgeExpired(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged=%d, want 1", n)
	}
	var count int64
	if err := client.Conn(ctx).Model(&idempotency.KeyRecord{}).Where("key = ?", "expired").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expired row remains")
	}
	if _, err := store.PurgeExpired(ctx, time.Time{}); err == nil {
		t.Fatal("zero cutoff must fail")
	}
}
