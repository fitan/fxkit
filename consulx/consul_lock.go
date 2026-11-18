package consulx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/internal/consuladdr"
)

const (
	// renewFailureThreshold 连续续租失败达到该值即判定会话失效（锁可能已易主）。
	renewFailureThreshold = 2
	// minWatchWait Blocking Query 的 wait 下限，避免退化为忙轮询。
	minWatchWait = 100 * time.Millisecond
	// maxWatchWait Blocking Query 的 wait 上限。
	maxWatchWait = 10 * time.Minute
	// tryLockAcquireAttempts TryLock 在遇到 5xx/传输错误时的最大尝试次数。
	tryLockAcquireAttempts = 3
	// consulRetryBackoff 是 blocking query 出错后的短退避。
	//
	// 不能再用 opts.RetryWait：waitKVChange 本身已经阻塞了 wait 这么久，
	// 叠加等于等两次（RetryWait=30s 时每次重试白等 60s）。
	consulRetryBackoff = 200 * time.Millisecond
)

type consulLocker struct {
	baseURL string
	client  *http.Client
}

// NewConsulLocker 创建基于 Consul Session 与 KV 的分布式锁管理器。
// client 可注入自定义 Transport（http.Client 为空时使用默认 10s 超时的 client）。
func NewConsulLocker(consulAddr string, client *http.Client) (Locker, error) {
	addr := strings.TrimSpace(consulAddr)
	if addr == "" {
		return nil, ErrConsulDisabled
	}
	base, err := consuladdr.Normalize(addr)
	if err != nil {
		return nil, fmt.Errorf("consulx: invalid consul address: %w", err)
	}
	if client == nil {
		// 长轮询的 wait 可能到 maxWatchWait，客户端超时必须大于它，
		// 否则每次 blocking query 都会被本地超时打断并退化为错误重试。
		client = &http.Client{Timeout: maxWatchWait + 10*time.Second}
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
	if err := validateLockKey(cleanKey); err != nil {
		return nil, err
	}
	opt := DefaultLockOptions()
	for _, o := range opts {
		o(&opt)
	}
	opt = normalizeLockOptions(opt, true)
	// 校验 prefix+key 拼出的最终 KV 路径（Consul 对字符集有严格要求）。
	if err := validateLockKey(opt.keyPath(cleanKey)); err != nil {
		return nil, err
	}
	return &consulLock{
		locker: l,
		key:    cleanKey,
		opts:   opt,
	}, nil
}

func (l *consulLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error, opts ...LockOption) (err error) {
	lock, err := l.NewLock(key, opts...)
	if err != nil {
		return err
	}
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		uctx, cancel := cleanupContext()
		defer cancel()
		err = joinUnlockErr(err, key, lock.Unlock(uctx))
	}()
	return fn(ctx)
}

// renewState 是单次加锁尝试（session）的续租状态。
// 续租协程只写本结构，不获取 consulLock.mu，因此与 Unlock 不存在锁序依赖。
type renewState struct {
	sessionID string
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	alive     atomic.Bool  // 续租协程是否仍在运行
	lost      atomic.Bool  // 续租判定会话失效
	lastOK    atomic.Int64 // 最近一次续租成功的 UnixNano

	mu     sync.Mutex
	lostEr error
}

func newRenewState(sessionID string) *renewState {
	s := &renewState{
		sessionID: sessionID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.lastOK.Store(time.Now().UnixNano())
	return s
}

func (s *renewState) stopRenew() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *renewState) wait() {
	<-s.done
}

func (s *renewState) markLost(err error) {
	s.mu.Lock()
	if s.lostEr == nil {
		s.lostEr = err
	}
	s.mu.Unlock()
	s.lost.Store(true)
}

func (s *renewState) lostErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lostEr
}

func (s *renewState) usable() bool {
	return !s.lost.Load() && s.alive.Load()
}

type consulLock struct {
	locker *consulLocker
	key    string
	opts   LockOptions

	// renewInterval 为 0 时按 TTL/3 推导（下限 2s）。仅测试需要覆盖。
	renewInterval time.Duration

	mu      sync.Mutex
	held    bool
	pending bool // 正在获取中（Lock 阻塞等待期间），用于实现不可重入
	renew   *renewState
	fencing atomic.Uint64
}

func (l *consulLock) FencingToken() uint64 {
	return l.fencing.Load()
}

func (l *consulLock) Key() string {
	return l.key
}

func (l *consulLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held && l.renew != nil && l.renew.usable()
}

func (l *consulLock) fullKey() string {
	return l.opts.keyPath(l.key)
}

// beginAcquire 在开始一次获取前检查实例状态：
//   - 仍在有效持有中 → ErrLockHeld（不可重入）
//   - 持有已丢失 → 清理旧会话后允许重新获取
//   - 另一个 goroutine 正在获取 → ErrLockHeld
func (l *consulLock) beginAcquire() error {
	l.mu.Lock()
	if l.held {
		st := l.renew
		if st != nil && st.lost.Load() {
			l.held = false
			l.renew = nil
			l.mu.Unlock()
			l.discardSession(st)
			return l.beginAcquire()
		}
		l.mu.Unlock()
		return ErrLockHeld
	}
	if l.pending {
		l.mu.Unlock()
		return ErrLockHeld
	}
	l.pending = true
	l.mu.Unlock()
	return nil
}

func (l *consulLock) endAcquire() {
	l.mu.Lock()
	l.pending = false
	l.mu.Unlock()
}

// commit 在确认续租协程存活后把会话登记为当前持有状态。
func (l *consulLock) commit(st *renewState) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held || l.renew != nil {
		return ErrLockHeld
	}
	l.held = true
	l.renew = st
	return nil
}

// discardSession 停掉续租协程并销毁会话（不等待调用方 ctx）。
func (l *consulLock) discardSession(st *renewState) {
	if st == nil {
		return
	}
	st.stopRenew()
	st.wait()
	ctx, cancel := cleanupContext()
	defer cancel()
	if err := l.destroySession(ctx, st.sessionID); err != nil {
		slog.Debug("consulx: destroy abandoned session failed",
			"session", st.sessionID, "error", err)
	}
}

func (l *consulLock) TryLock(ctx context.Context) (err error) {
	start := time.Now()
	defer func() { recordLockAcquire(ctx, time.Since(start), err) }()

	if err := l.beginAcquire(); err != nil {
		return err
	}
	defer l.endAcquire()

	sessionID, st, err := l.startSession(ctx)
	if err != nil {
		return err
	}

	acquired, idx, err := l.acquireWithRetry(ctx, sessionID, tryLockAcquireAttempts)
	if err != nil {
		l.discardSession(st)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	if !acquired {
		l.discardSession(st)
		return ErrLockHeld
	}
	// 关键：续租协程已经死亡时绝不能报告“持锁成功”。
	if !st.usable() {
		l.discardSession(st)
		return l.sessionLostError(st)
	}
	if err := l.commit(st); err != nil {
		l.discardSession(st)
		return err
	}
	if !st.usable() {
		// 提交与续租失败之间的竞态：撤销持有状态并如实报错。
		_ = l.Unlock(ctx)
		return l.sessionLostError(st)
	}
	l.fencing.Store(idx)
	return nil
}

func (l *consulLock) Lock(ctx context.Context) (err error) {
	start := time.Now()
	defer func() { recordLockAcquire(ctx, time.Since(start), err) }()

	if err := l.beginAcquire(); err != nil {
		return err
	}
	defer l.endAcquire()

	var index uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		sessionID, st, err := l.startSession(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !isRetryable(err) {
				return err
			}
			if !sleepCtx(ctx, l.opts.RetryWait) {
				return ctx.Err()
			}
			continue
		}

		// 内层循环复用当前 session（续租协程持续保活），直到拿到锁或会话失效。
		for {
			if ctxErr := ctx.Err(); ctxErr != nil {
				l.discardSession(st)
				return ctxErr
			}

			// 续租协程已退出时，session 已失效。继续用它 acquire 只会拿到
			// 500 "Missing session"（被视为可重试）而无限循环。
			if !st.usable() {
				l.discardSession(st)
				break
			}

			// maxAttempts=1：把 "Missing session" 这类可重试错误交回外层，
			// 重建 session 后再试，而不是在死 session 上空转。
			acquired, idx, err := l.acquireWithRetry(ctx, sessionID, 1)
			if idx > 0 {
				index = idx
			}
			if err != nil {
				l.discardSession(st)
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if !isRetryable(err) {
					return err
				}
				if !sleepCtx(ctx, l.opts.RetryWait) {
					return ctx.Err()
				}
				break
			}
			if acquired {
				if !st.usable() {
					// 会话/续租协程已失效：重建 session 后重试，绝不能假装持锁。
					l.discardSession(st)
					break
				}
				if err := l.commit(st); err != nil {
					l.discardSession(st)
					return err
				}
				if !st.usable() {
					_ = l.Unlock(ctx)
					break
				}
				l.fencing.Store(index)
				return nil
			}

			// 使用真正的 Blocking Query：带上 X-Consul-Index 与 wait。
			nextIndex, werr := l.waitKVChange(ctx, l.fullKey(), index, l.opts.RetryWait)
			if nextIndex > 0 {
				index = nextIndex
			}
			if !st.usable() {
				l.discardSession(st)
				break
			}
			if werr != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					l.discardSession(st)
					return ctxErr
				}
				if !isRetryable(werr) {
					l.discardSession(st)
					return werr
				}
				if !sleepCtx(ctx, consulRetryBackoff) {
					l.discardSession(st)
					return ctx.Err()
				}
			}
		}
	}
}

func (l *consulLock) Unlock(ctx context.Context) error {
	l.mu.Lock()
	if !l.held {
		l.mu.Unlock()
		return ErrLockNotHeld
	}
	st := l.renew
	lost := st != nil && st.lost.Load()
	l.held = false
	l.renew = nil
	l.fencing.Store(0)
	l.mu.Unlock()

	// 必须先释放 l.mu 再等待续租协程退出：续租协程可能与 Unlock 并发，
	// 持锁等待 renewDone 会造成死锁。
	if st == nil {
		return nil
	}
	st.stopRenew()
	st.wait()

	var errs []error
	// 释放/销毁必须用独立 context：调用方常常是 `defer lock.Unlock(reqCtx)`，
	// 而此时 reqCtx 已取消，用它会静默释放失败，锁要白占一个 TTL。
	cleanupCtx, cancel := cleanupContext()
	defer cancel()
	if !lost {
		errs = append(errs, l.releaseKV(cleanupCtx, st.sessionID))
	} else {
		errs = append(errs, ErrLockLost, st.lostErr())
	}
	errs = append(errs, l.destroySession(cleanupCtx, st.sessionID))
	return errors.Join(errs...)
}

func (l *consulLock) sessionLostError(st *renewState) error {
	if err := st.lostErr(); err != nil {
		return errors.Join(ErrLockLost, err)
	}
	return ErrLockLost
}

// startSession 创建 Consul Session 并启动续租协程。
// alive 在启动前同步置位，避免调用方在 goroutine 尚未调度时误判为死亡。
func (l *consulLock) startSession(ctx context.Context) (string, *renewState, error) {
	sessionID, err := l.createSession(ctx)
	if err != nil {
		return "", nil, err
	}
	st := newRenewState(sessionID)
	st.alive.Store(true)
	go l.renewLoop(st)
	return sessionID, st, nil
}

type createSessionRequest struct {
	Name      string `json:"Name"`
	TTL       string `json:"TTL"`
	Behavior  string `json:"Behavior"`
	LockDelay string `json:"LockDelay"`
}

func (l *consulLock) createSession(ctx context.Context) (string, error) {
	ttlSec := ttlSecondsCeil(l.opts.TTL)
	delaySec := lockDelaySeconds(l.opts.LockDelay)

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

	body, err := readBodyAndCheck("consul create session", resp)
	if err != nil {
		return "", err
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

func (l *consulLock) renewLoop(st *renewState) {
	defer func() {
		st.alive.Store(false)
		close(st.done)
	}()

	interval := l.renewInterval
	if interval <= 0 {
		interval = l.opts.TTL / 3
		if interval < 2*time.Second {
			interval = 2 * time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 续租请求的超时不能固定 5s：TTL 很小时（如 10s）单次超时就能超过 TTL，
	// 导致锁已在 Consul 侧过期、本地仍报告持有。
	renewTimeout := l.opts.TTL / 6
	if renewTimeout > 5*time.Second {
		renewTimeout = 5 * time.Second
	}
	if renewTimeout < 500*time.Millisecond {
		renewTimeout = 500 * time.Millisecond
	}

	failures := 0
	for {
		select {
		case <-st.stop:
			return
		case <-ticker.C:
			err := l.renewSession(st.sessionID, renewTimeout)
			if err == nil {
				failures = 0
				st.lastOK.Store(time.Now().UnixNano())
				continue
			}
			failures++
			recordRenewFailure(context.Background())
			slog.Warn("consul lock session renew failed",
				"key", l.key, "session", st.sessionID, "failures", failures, "error", err)
			// 404 = 会话已不存在；或距上次成功续租已超过 TTL
			// （Consul 侧大概率已释放锁）。两者都按丢失处理。
			lastOK := time.Unix(0, st.lastOK.Load())
			// 三种判定：404（会话已不存在）、连续失败达到阈值（快速检测），
			// 或距上次成功已超过 TTL（Consul 侧一定已释放，不能继续声称持有）。
			if isStatusError(err, http.StatusNotFound) ||
				failures >= renewFailureThreshold ||
				time.Since(lastOK) >= l.opts.TTL {
				st.markLost(fmt.Errorf("consulx: session renew failed %d time(s): %w", failures, err))
				recordLockLost(context.Background())
				slog.Error("consul lock lost; session renew failed",
					"key", l.key, "session", st.sessionID, "failures", failures, "error", err)
				if l.opts.OnLost != nil {
					go safeOnLost(l.opts.OnLost, l.key, st.lostErr())
				}
				return
			}
		}
	}
}

// safeOnLost 执行用户回调，吞掉 panic 以免终止进程。
func safeOnLost(fn func(key string, err error), key string, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("consulx: OnLost callback panicked", "key", key, "panic", r)
		}
	}()
	fn(key, err)
}

func (l *consulLock) renewSession(sessionID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	u := fmt.Sprintf("%s/v1/session/renew/%s", l.locker.baseURL, url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = readBodyAndCheck("consul renew session", resp)
	return err
}

func (l *consulLock) destroySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	u := fmt.Sprintf("%s/v1/session/destroy/%s", l.locker.baseURL, url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul destroy session: %w", err)
	}
	defer resp.Body.Close()

	_, err = readBodyAndCheck("consul destroy session", resp)
	return err
}

// escapeKeyPath 对 key 的每一段做 PathEscape，保留 "/" 作为分隔符。
func escapeKeyPath(fullKey string) string {
	segments := strings.Split(fullKey, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/")
}

func (l *consulLock) kvURL(fullKey string) string {
	return l.locker.baseURL + "/v1/kv/" + escapeKeyPath(fullKey)
}

func (l *consulLock) acquireKV(ctx context.Context, sessionID string) (bool, uint64, error) {
	var body io.Reader
	if len(l.opts.Value) > 0 {
		body = bytes.NewReader(l.opts.Value)
	}
	q := "?acquire=" + url.QueryEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, l.kvURL(l.fullKey())+q, body)
	if err != nil {
		return false, 0, err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return false, 0, fmt.Errorf("consul acquire kv: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := readBodyAndCheck("consul acquire kv", resp)
	if err != nil {
		return false, parseConsulIndex(resp.Header), err
	}
	ok := strings.TrimSpace(string(bodyBytes)) == "true"
	idx := parseConsulIndex(resp.Header)
	// 真实 Consul 的 KV PUT 响应不带 X-Consul-Index，只有 GET/blocking query 才带。
	// 不补读的话 fencing token 恒为 0，与「0 = 未持有/已释放」语义冲突。
	if ok && idx == 0 {
		if got, gerr := l.readKVIndex(ctx); gerr == nil && got > 0 {
			idx = got
		}
	}
	return ok, idx, nil
}

// readKVIndex 读取 key 当前的 X-Consul-Index（即写入产生的 ModifyIndex）。
// 仅用于 acquire 响应缺少 index 时补齐 fencing token。
func (l *consulLock) readKVIndex(ctx context.Context) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.kvURL(l.fullKey()), nil)
	if err != nil {
		return 0, err
	}
	config.ApplyConsulToken(req)
	resp, err := l.locker.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("consul kv index: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("consul kv index: status=%d", resp.StatusCode)
	}
	return parseConsulIndex(resp.Header), nil
}

// acquireWithRetry 对 5xx/传输错误按指数退避重试；4xx 立即失败。
// maxAttempts<=0 表示一直重试直到 ctx 结束。
func (l *consulLock) acquireWithRetry(ctx context.Context, sessionID string, maxAttempts int) (bool, uint64, error) {
	backoff := l.opts.RetryWait
	if backoff <= 0 {
		backoff = 250 * time.Millisecond
	}
	for attempt := 1; ; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, 0, ctxErr
		}
		acquired, index, err := l.acquireKV(ctx, sessionID)
		if err == nil {
			return acquired, index, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, index, ctxErr
		}
		if !isRetryable(err) {
			return false, index, err
		}
		if maxAttempts > 0 && attempt >= maxAttempts {
			return false, index, err
		}
		if !sleepCtx(ctx, backoff) {
			return false, index, ctx.Err()
		}
		backoff = backoffNext(backoff)
	}
}

func (l *consulLock) releaseKV(ctx context.Context, sessionID string) error {
	q := "?release=" + url.QueryEscape(sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, l.kvURL(l.fullKey())+q, nil)
	if err != nil {
		return err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return fmt.Errorf("consul release kv: %w", err)
	}
	defer resp.Body.Close()

	_, err = readBodyAndCheck("consul release kv", resp)
	return err
}

// waitKVChange 发起真正的 Consul Blocking Query：
// 携带上次的 X-Consul-Index 与 wait，直到索引变化或等待窗口结束。
func (l *consulLock) waitKVChange(ctx context.Context, fullKey string, index uint64, wait time.Duration) (uint64, error) {
	if wait < minWatchWait {
		wait = minWatchWait
	}
	if wait > maxWatchWait {
		wait = maxWatchWait
	}
	// 服务端 wait 必须短于客户端超时：否则长轮询会在本地被超时打断，
	// 每次都要等满 10s 再报错重试，退化成轮询。
	if t := l.locker.client.Timeout; t > 0 {
		if limit := t - 5*time.Second; limit >= minWatchWait && wait > limit {
			wait = limit
		}
	}
	q := url.Values{}
	q.Set("wait", wait.String())
	if index > 0 {
		q.Set("index", strconv.FormatUint(index, 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.kvURL(fullKey)+"?"+q.Encode(), nil)
	if err != nil {
		return index, err
	}
	config.ApplyConsulToken(req)

	resp, err := l.locker.client.Do(req)
	if err != nil {
		return index, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	next := parseConsulIndex(resp.Header)
	if next == 0 {
		next = index
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// key 不存在（已被释放）：不是错误，继续尝试 acquire。
		return next, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return next, nil
	default:
		return next, &statusError{Op: "consul kv watch", Code: resp.StatusCode}
	}
}

func parseConsulIndex(h http.Header) uint64 {
	if h == nil {
		return 0
	}
	raw := strings.TrimSpace(h.Get("X-Consul-Index"))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
