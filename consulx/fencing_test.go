package consulx_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/consulx"
)

func TestMemoryLock_FencingToken(t *testing.T) {
	locker := consulx.NewMemoryLocker()
	lock, err := locker.NewLock("fencing")
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.FencingToken(); got != 0 {
		t.Fatalf("token before Lock=%d, want 0", got)
	}
	if err := lock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := lock.FencingToken()
	if first == 0 {
		t.Fatal("expected non-zero fencing token after Lock")
	}
	if err := lock.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := lock.FencingToken(); got != 0 {
		t.Fatalf("token after Unlock=%d, want 0", got)
	}

	if err := lock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := lock.FencingToken()
	if second <= first {
		t.Fatalf("fencing token must increase: first=%d second=%d", first, second)
	}
	_ = lock.Unlock(context.Background())
}
