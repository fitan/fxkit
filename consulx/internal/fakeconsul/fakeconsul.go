// Package fakeconsul 提供一个基于 http.RoundTripper 的 Consul 假实现，
// 供 consulx 测试注入 http.Client 使用（无需监听端口）。
package fakeconsul

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server 实现 http.RoundTripper，模拟 consulx 使用到的 Consul HTTP API。
type Server struct {
	mu sync.Mutex

	Sessions map[string]bool
	KV       map[string]string // key（已解码）-> sessionID

	Doomed     map[string]bool // 这些 session 的 renew 返回 500
	RenewCalls map[string]int

	// DoomAllRenew 为 true 时所有 renew（含未来创建的 session）都返回 500。
	DoomAllRenew bool

	AcquireCalls int
	ReleaseCalls int
	DestroyCalls int

	CreateStatus   int // 非 0 时 session/create 强制返回该状态码
	AcquireStatus  int // 非 0 时 acquire 强制返回该状态码（4xx 致命）
	AcquireFailure int // >0 时前 N 次 acquire 返回 500
	ReleaseStatus  int // 非 0 时 release 返回该状态码
	DestroyStatus  int // 非 0 时 destroy 返回该状态码

	PassStatus int // 非 0 时 check/pass 返回该状态码
	// NotFoundUntilRegistered 为 true 时，服务未注册前 check/pass 返回 404。
	NotFoundUntilRegistered bool

	RegisterStatus int // 非 0 时 agent/service/register 返回该状态码

	// AcquireDelay 在 acquire 请求处理前等待（不持内部锁），用于让续租协程先失败。
	AcquireDelay time.Duration

	Registrations   [][]byte
	Deregistrations []string
	PassCalls       []string
	Tokens          []string

	Requests     []string
	WatchQueries []url.Values
	EscapedPaths []string

	seq   int
	index uint64
}

// New 创建假 Consul。
func New() *Server {
	return &Server{
		Sessions:   map[string]bool{},
		KV:         map[string]string{},
		Doomed:     map[string]bool{},
		RenewCalls: map[string]int{},
	}
}

// Client 返回注入本 fake 的 http.Client。
func (s *Server) Client() *http.Client {
	return &http.Client{Transport: s}
}

func (s *Server) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	delay := s.AcquireDelay
	s.mu.Unlock()
	if delay > 0 && req.Method == http.MethodPut &&
		strings.HasPrefix(req.URL.Path, "/v1/kv/") && req.URL.Query().Get("acquire") != "" {
		// 释放内部锁后等待，让续租请求仍可并发处理。
		time.Sleep(delay)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.Requests = append(s.Requests, req.Method+" "+req.URL.RequestURI())
	s.Tokens = append(s.Tokens, req.Header.Get("X-Consul-Token"))

	path := req.URL.Path
	switch {
	case req.Method == http.MethodPut && path == "/v1/session/create":
		if s.CreateStatus != 0 {
			return resp(req, s.CreateStatus, "create failed", 0), nil
		}
		var payload struct {
			TTL       string `json:"TTL"`
			Name      string `json:"Name"`
			Behavior  string `json:"Behavior"`
			LockDelay string `json:"LockDelay"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &payload)
		s.seq++
		id := fmt.Sprintf("session-%d", s.seq)
		s.Sessions[id] = true
		// 记录 TTL/LockDelay 便于断言。
		s.KV["__meta__/"+id] = payload.TTL + "|" + payload.LockDelay
		return resp(req, http.StatusOK, fmt.Sprintf(`{"ID":%q}`, id), 0), nil

	case req.Method == http.MethodPut && strings.HasPrefix(path, "/v1/session/renew/"):
		id := strings.TrimPrefix(path, "/v1/session/renew/")
		s.RenewCalls[id]++
		if s.DoomAllRenew || s.Doomed[id] {
			return resp(req, http.StatusInternalServerError, "renew boom", 0), nil
		}
		if !s.Sessions[id] {
			return resp(req, http.StatusNotFound, "session not found", 0), nil
		}
		return resp(req, http.StatusOK, "", 0), nil

	case req.Method == http.MethodPut && strings.HasPrefix(path, "/v1/session/destroy/"):
		id := strings.TrimPrefix(path, "/v1/session/destroy/")
		s.DestroyCalls++
		if s.DestroyStatus != 0 {
			return resp(req, s.DestroyStatus, "destroy failed", 0), nil
		}
		delete(s.Sessions, id)
		for k, v := range s.KV {
			if v == id && !strings.HasPrefix(k, "__meta__/") {
				delete(s.KV, k)
			}
		}
		return resp(req, http.StatusOK, "", 0), nil

	case strings.HasPrefix(path, "/v1/kv/"):
		s.EscapedPaths = append(s.EscapedPaths, req.URL.EscapedPath())
		key := strings.TrimPrefix(path, "/v1/kv/")
		q := req.URL.Query()

		if sid := q.Get("acquire"); sid != "" {
			s.AcquireCalls++
			if s.AcquireStatus != 0 {
				return resp(req, s.AcquireStatus, "acquire failed", 0), nil
			}
			if s.AcquireFailure > 0 {
				s.AcquireFailure--
				return resp(req, http.StatusInternalServerError, "acquire boom", 0), nil
			}
			s.index++
			if cur, ok := s.KV[key]; !ok || cur == sid {
				s.KV[key] = sid
				return resp(req, http.StatusOK, "true", s.index), nil
			}
			return resp(req, http.StatusOK, "false", s.index), nil
		}

		if sid := q.Get("release"); sid != "" {
			s.ReleaseCalls++
			if s.ReleaseStatus != 0 {
				return resp(req, s.ReleaseStatus, "release failed", 0), nil
			}
			s.index++
			if s.KV[key] == sid {
				delete(s.KV, key)
				return resp(req, http.StatusOK, "true", s.index), nil
			}
			return resp(req, http.StatusOK, "false", s.index), nil
		}

		// 阻塞查询
		s.WatchQueries = append(s.WatchQueries, q)
		s.index++
		if _, ok := s.KV[key]; !ok {
			return resp(req, http.StatusNotFound, "", s.index), nil
		}
		return resp(req, http.StatusOK, "[]", s.index), nil

	case req.Method == http.MethodPut && path == "/v1/agent/service/register":
		if s.RegisterStatus != 0 {
			return resp(req, s.RegisterStatus, "register failed", 0), nil
		}
		raw, _ := io.ReadAll(req.Body)
		s.Registrations = append(s.Registrations, raw)
		return resp(req, http.StatusOK, "", 0), nil

	case req.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agent/service/deregister/"):
		s.Deregistrations = append(s.Deregistrations, strings.TrimPrefix(path, "/v1/agent/service/deregister/"))
		return resp(req, http.StatusOK, "", 0), nil

	case req.Method == http.MethodPut && strings.HasPrefix(path, "/v1/agent/check/pass/"):
		id := strings.TrimPrefix(path, "/v1/agent/check/pass/")
		s.PassCalls = append(s.PassCalls, id)
		if s.PassStatus != 0 {
			return resp(req, s.PassStatus, "pass failed", 0), nil
		}
		if s.NotFoundUntilRegistered && len(s.Registrations) == 0 {
			return resp(req, http.StatusNotFound, "check not found", 0), nil
		}
		return resp(req, http.StatusOK, "", 0), nil

	case req.Method == http.MethodGet && path == "/v1/agent/services":
		return resp(req, http.StatusOK, "{}", 0), nil

	case req.Method == http.MethodGet && path == "/v1/agent/checks":
		return resp(req, http.StatusOK, "{}", 0), nil
	}
	return resp(req, http.StatusNotFound, "unhandled: "+path, 0), nil
}

func resp(req *http.Request, code int, body string, index uint64) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	if index > 0 {
		h.Set("X-Consul-Index", fmt.Sprintf("%d", index))
	}
	return &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// --- 线程安全的状态访问辅助 ---

// SessionFor 返回当前持有该 key 的 session（key 为已解码的完整 KV key）。
func (s *Server) SessionFor(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.KV[key]
}

// Doom 标记某 session 的续租永远失败。
func (s *Server) Doom(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Doomed[sessionID] = true
}

// RenewCount 返回该 session 的续租尝试次数。
func (s *Server) RenewCount(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RenewCalls[sessionID]
}

// Counts 返回 acquire / release / destroy 的调用次数。
func (s *Server) Counts() (acquire, release, destroy int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AcquireCalls, s.ReleaseCalls, s.DestroyCalls
}

// SetFailAllRenew 控制所有 renew 是否失败。
func (s *Server) SetFailAllRenew(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DoomAllRenew = v
}

// PassCallCount 返回 TTL pass 调用次数。
func (s *Server) PassCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.PassCalls)
}

// Watch 返回阻塞查询次数与最近一次查询参数。
func (s *Server) Watch() (int, url.Values) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.WatchQueries) == 0 {
		return 0, nil
	}
	cp := url.Values{}
	for k, v := range s.WatchQueries[len(s.WatchQueries)-1] {
		cp[k] = append([]string(nil), v...)
	}
	return len(s.WatchQueries), cp
}

// SessionTTL 返回某 session 创建时请求的 TTL/LockDelay 字符串。
func (s *Server) SessionTTL(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.KV["__meta__/"+sessionID]
}

// RegisterCount 返回服务注册次数。
func (s *Server) RegisterCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Registrations)
}

// Registration 返回第 i 次注册的原始 JSON。
func (s *Server) Registration(i int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.Registrations) {
		return nil
	}
	return s.Registrations[i]
}

// LastSessionID 返回最近创建的 session id。
func (s *Server) LastSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seq == 0 {
		return ""
	}
	return fmt.Sprintf("session-%d", s.seq)
}

// SessionCount 返回当前存在的 session 数量。
func (s *Server) SessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Sessions)
}

// AllTokens 返回所有请求携带的 token 列表（副本）。
func (s *Server) AllTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Tokens...)
}

// PassCallsSnapshot 返回 TTL pass 调用过的 checkID 列表。
func (s *Server) PassCallsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.PassCalls...)
}

// SetAcquireDelay 设置 acquire 请求的处理延迟。
func (s *Server) SetAcquireDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AcquireDelay = d
}

// SetRegisterStatus 设置注册接口强制状态码（0 表示正常）。
func (s *Server) SetRegisterStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RegisterStatus = code
}

// SetPassStatus 设置 TTL pass 强制状态码（0 表示正常）。
func (s *Server) SetPassStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PassStatus = code
}

// LastEscapedPath 返回最近一次 KV 请求的转义路径。
func (s *Server) LastEscapedPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.EscapedPaths) == 0 {
		return ""
	}
	return s.EscapedPaths[len(s.EscapedPaths)-1]
}
