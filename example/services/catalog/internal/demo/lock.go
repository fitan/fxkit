package demo

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/fitan/fxkit/consulx"
	"github.com/fitan/fxkit/fxerrors"
)

// Consul KV key 只接受 [a-zA-Z0-9,_./\-?&=]，其余字符替换为 "-"。
var lockKeyUnsafe = regexp.MustCompile(`[^a-zA-Z0-9,_./\-?&=]`)

// LockDemo 演示 consulx.Locker：阻塞获取 → 临界区 → 释放，并返回 fencing token。
// 未配置 Consul 时返回 503（BuildLocker 会返回 disabledLocker）。
func (s *Service) LockDemo(ctx context.Context, name string, hold time.Duration) (LockDemo, error) {
	if s.locker == nil {
		return LockDemo{}, fxerrors.Unavailable("locker disabled")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return LockDemo{}, fxerrors.Validation(map[string]string{"name": "required"})
	}
	if hold <= 0 || hold > 5*time.Second {
		hold = 200 * time.Millisecond
	}
	key := "demo/" + lockKeyUnsafe.ReplaceAllString(name, "-")

	lk, err := s.locker.NewLock(key, consulx.WithTTL(15*time.Second))
	if err != nil {
		if errors.Is(err, consulx.ErrConsulDisabled) {
			return LockDemo{}, fxerrors.Unavailable("consul disabled")
		}
		return LockDemo{}, fxerrors.Wrap(err)
	}
	if err := lk.Lock(ctx); err != nil {
		if errors.Is(err, consulx.ErrConsulDisabled) {
			return LockDemo{}, fxerrors.Unavailable("consul disabled")
		}
		return LockDemo{}, fxerrors.Unavailable("lock: %v", err)
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = lk.Unlock(uctx)
	}()

	token := lk.FencingToken()
	select {
	case <-ctx.Done():
		return LockDemo{}, ctx.Err()
	case <-time.After(hold):
	}
	return LockDemo{Key: key, FencingToken: token, HeldMs: hold.Milliseconds()}, nil
}
