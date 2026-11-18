package consulx_test

import (
	"context"
	"testing"

	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/consulx/internal/fakeconsul"
)

// 真实 Consul 的 KV PUT 不返回 X-Consul-Index；此时仍必须给出非 0 的 fencing token，
// 否则「0 = 未持有」的约定会把持锁状态报告成未持有。
func TestConsulLock_FencingTokenWhenPutHasNoIndex(t *testing.T) {
	fake := fakeconsul.New()
	fake.SuppressAcquireIndex = true
	locker, err := consulx.NewConsulLocker("http://consul.test", fake.Client())
	if err != nil {
		t.Fatal(err)
	}

	lock, err := locker.NewLock("fence")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock(context.Background()) }()

	if got := lock.FencingToken(); got == 0 {
		t.Fatal("expected non-zero fencing token when acquire response has no X-Consul-Index")
	}
}
