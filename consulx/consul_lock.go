package consulx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fitan/fxkit/config"
)

type consulLocker struct {
	baseURL string
	client  *http.Client
}

// NewConsulLocker 创建基于 Consul Session 与 KV 的分布式锁管理器。
func NewConsulLocker(consulAddr string, client *http.Client) (Locker, error) {
	addr := strings.TrimSpace(consulAddr)
	if addr == "" {
		return nil, ErrConsulDisabled
	}
	base, err := normalizeConsulAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("consulx: invalid consul address: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &consulLocker{
		baseURL: base,
		client:  client,
	}, nil
}

func (l *consulLocker) NewLock(key string, opts ...LockOption) (Lock, error) {
	cleanKey := strings.TrimSpace(strings.Trim(key, "/"))
	if cleanKey == "" {
		return nil, fmt.Errorf("consulx: lock key cannot be empty")
	}
	opt := DefaultLockOptions()
	for _, o := range opts {
		o(&opt)
	}
	return &consulLock{
		locker: l,
		key:    cleanKey,
		opts:   opt,
	}, nil
}

func (l *consulLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) error {
	lock, err := l.NewLock(key, opts...)
	if err != nil {
		return err
	}
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock(context.Background())
	}()
	return fn(ctx)
}

type consulLock struct {
	locker    *consulLocker
	key       string
	opts      LockOptions
	mu        sync.Mutex
	isHeld    bool
	sessionID string
	stopRenew chan struct{}
	renewDone chan struct{}
	lockLost  bool
}

func (l *consulLock) Key() string {
	return l.key
}

func (l *consulLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.isHeld && !l.lockLost
}

func (l *consulLock) fullKey() string {
	prefix := strings.TrimLeft(l.opts.Prefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix + l.key
}

func (l *consulLock) TryLock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.isHeld {
		return nil
	}

	sessionID, err := l.createSession(ctx)
	if err != nil {
		return err
	}

	acquired, err := l.acquireKV(ctx, sessionID)
	if err != nil {
		_ = l.destroySession(context.Background(), sessionID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if !acquired {
		_ = l.destroySession(context.Background(), sessionID)
		return ErrLockHeld
	}

	l.sessionID = sessionID
	l.isHeld = true
	l.lockLost = false
	l.stopRenew = make(chan struct{})
	l.renewDone = make(chan struct{})

	go l.renewLoop(sessionID, l.stopRenew, l.renewDone)
	return nil
}

func (l *consulLock) Lock(ctx context.Context) error {
	l.mu.Lock()
	if l.isHeld {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	sessionID, err := l.createSession(ctx)
	if err != nil {
		return err
	}

	// 启动心跳协程，保证在阻塞等待期间该 Session 不会因 TTL 超时而提前失效
	stopRenew := make(chan struct{})
	renewDone := make(chan struct{})
	go l.renewLoop(sessionID, stopRenew, renewDone)

	fullKey := l.fullKey()
	ticker := time.NewTicker(l.opts.RetryWait)
	defer ticker.Stop()

	for {
		acquired, err := l.acquireKV(ctx, sessionID)
		if err != nil {
			close(stopRenew)
			<-renewDone
			_ = l.destroySession(context.Background(), sessionID)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if acquired {
			l.mu.Lock()
			l.sessionID = sessionID
			l.isHeld = true
			l.lockLost = false
			l.stopRenew = stopRenew
			l.renewDone = renewDone
			l.mu.Unlock()
			return nil
		}

		// 阻塞等待释放或超时
		select {
		case <-ctx.Done():
			close(stopRenew)
			<-renewDone
			_ = l.destroySession(context.Background(), sessionID)
			return ctx.Err()
		case <-ticker.C:
			// 尝试使用 Consul 的 Blocking Query 探测，若不支持或超时直接继续
			_ = l.waitKVChange(ctx, fullKey)
		}
	}
}

func (l *consulLock) Unlock(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.isHeld {
		return ErrLockNotHeld
	}

	sessionID := l.sessionID
	if l.stopRenew != nil {
		close(l.stopRenew)
		<-l.renewDone
		l.stopRenew = nil
		l.renewDone = nil
	}

	// 释放 KV 上的锁
	_ = l.releaseKV(ctx, sessionID)
	// 销毁 Session
	_ = l.destroySession(ctx, sessionID)

	l.isHeld = false
	l.sessionID = ""
	l.lockLost = false
	return nil
}

type createSessionRequest struct {
	Name      string `json:"Name"`
	TTL       string `json:"TTL"`
	Behavior  string `json:"Behavior"`
	LockDelay string `json:"LockDelay"`
}

func (l *consulLock) createSession(ctx context.Context) (string, error) {
	ttlSec := int(l.opts.TTL.Seconds())
	if ttlSec < 10 {
		ttlSec = 10
	}
	delaySec := int(l.opts.LockDelay.Seconds())

	payload := createSessionRequest{
		Name:      "fxkit-lock:" + l.fullKey(),
		TTL:       fmt.Sprintf("%ds", ttlSec),
		Behavior:  "release",
		LockDelay: fmt.Sprintf("%ds", delaySec),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, l.locker.baseURL+"/v1/session/create", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("consul create session: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("consul create session status=%d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("consul parse session id: %w", err)
	}
	if res.ID == "" {
		return "", fmt.Errorf("consul returned empty session id")
	}
	return res.ID, nil
}

func (l *consulLock) renewLoop(sessionID string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	interval := l.opts.TTL / 3
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := l.renewSession(sessionID); err != nil {
				failures++
				slog.Warn("consul lock session renew failed", "session", sessionID, "failures", failures, "error", err)
				if failures >= 2 {
					l.mu.Lock()
					l.lockLost = true
					l.mu.Unlock()
					return
				}
			} else {
				failures = 0
			}
		}
	}
}

func (l *consulLock) renewSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1/session/renew/%s", l.locker.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("renew session status=%d", resp.StatusCode)
	}
	return nil
}

func (l *consulLock) destroySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	url := fmt.Sprintf("%s/v1/session/destroy/%s", l.locker.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (l *consulLock) acquireKV(ctx context.Context, sessionID string) (bool, error) {
	url := fmt.Sprintf("%s/v1/kv/%s?acquire=%s", l.locker.baseURL, l.fullKey(), sessionID)
	var body io.Reader
	if len(l.opts.Value) > 0 {
		body = bytes.NewReader(l.opts.Value)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return false, err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("consul acquire kv: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("consul acquire kv status=%d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return strings.TrimSpace(string(respBody)) == "true", nil
}

func (l *consulLock) releaseKV(ctx context.Context, sessionID string) error {
	url := fmt.Sprintf("%s/v1/kv/%s?release=%s", l.locker.baseURL, l.fullKey(), sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (l *consulLock) waitKVChange(ctx context.Context, fullKey string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1/kv/%s?wait=1s", l.locker.baseURL, fullKey)
	req, err := http.NewRequestWithContext(waitCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
