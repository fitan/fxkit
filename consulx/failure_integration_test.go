package consulx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// roundTripFunc 让测试可以注入传输层故障（模拟网络分区）。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestConsulLockFailureModes 在真实 Consul 上验证锁丢失路径：
//  1. 续租连续失败 → IsHeld=false、OnLost 触发、Unlock 返回 ErrLockLost
//  2. 会话被外部销毁（404）→ 立即判定丢失
func TestConsulLockFailureModes(t *testing.T) {
	addr := consulAddrForTest(t)
	ctx := context.Background()

	t.Run("renew failures", func(t *testing.T) {
		var failRenew atomic.Bool
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if failRenew.Load() && strings.Contains(r.URL.Path, "/v1/session/renew/") {
				return nil, errors.New("simulated network failure")
			}
			return http.DefaultTransport.RoundTrip(r)
		})}

		l, err := NewConsulLocker(addr, client)
		require.NoError(t, err)

		lost := make(chan error, 1)
		lock, err := l.NewLock("fxkit-itest/fail-renew", WithTTL(10*time.Second),
			WithOnLost(func(_ string, e error) {
				select {
				case lost <- e:
				default:
				}
			}))
		require.NoError(t, err)
		cl, ok := lock.(*consulLock)
		require.True(t, ok)
		cl.renewInterval = 50 * time.Millisecond // 加速续租，必须在 TryLock 前设置

		require.NoError(t, lock.TryLock(ctx))
		require.True(t, lock.IsHeld())

		failRenew.Store(true)
		select {
		case <-lost:
		case <-time.After(15 * time.Second):
			t.Fatal("OnLost was not called after renew failures")
		}
		require.False(t, lock.IsHeld(), "lock must report not held after renew failures")
		require.ErrorIs(t, lock.Unlock(ctx), ErrLockLost)
	})

	t.Run("session destroyed", func(t *testing.T) {
		l, err := NewConsulLocker(addr, nil)
		require.NoError(t, err)

		lock, err := l.NewLock("fxkit-itest/session-destroy", WithTTL(10*time.Second))
		require.NoError(t, err)
		cl, ok := lock.(*consulLock)
		require.True(t, ok)
		cl.renewInterval = 100 * time.Millisecond
		require.NoError(t, lock.TryLock(ctx))

		sessionID := cl.renew.sessionID
		require.NotEmpty(t, sessionID)

		base, err := normalizeConsulAddr(addr)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut,
			base+"/v1/session/destroy/"+url.PathEscape(sessionID), nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		deadline := time.Now().Add(15 * time.Second)
		for lock.IsHeld() {
			if time.Now().After(deadline) {
				t.Fatal("lock still reports held after session destroy")
			}
			time.Sleep(50 * time.Millisecond)
		}
		require.ErrorIs(t, lock.Unlock(ctx), ErrLockLost)
	})
}
