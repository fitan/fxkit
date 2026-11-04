package server

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	rateLimitSweepThreshold = 10000
	rateLimitIdleTTL        = 10 * time.Minute
)

// rateLimitMiddleware 按客户端 IP 做令牌桶限流。
//
// 默认关闭；启用后超限返回 429 + Retry-After。仅使用 RemoteAddr（不信任
// X-Forwarded-For，避免客户端伪造 IP 绕过）；部署在网关后时应由网关先限流。
func rateLimitMiddleware(cfg RateLimitConfig) Middleware {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	rps := cfg.RPS
	if rps <= 0 {
		rps = 20
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = 40
	}
	limiter := newIPLimiter(rps, burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(clientIP(r)) {
				w.Header().Set("Retry-After", "1")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":"too_many_requests","message":"rate limit exceeded"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type rateBucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

type ipLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	rps     float64
	burst   float64
}

func newIPLimiter(rps float64, burst int) *ipLimiter {
	return &ipLimiter{
		buckets: make(map[string]*rateBucket),
		rps:     rps,
		burst:   float64(burst),
	}
}

func (l *ipLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > rateLimitSweepThreshold {
		l.sweepLocked(now)
	}

	b := l.buckets[key]
	if b == nil {
		b = &rateBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rps)
		b.last = now
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *ipLimiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.lastSeen) > rateLimitIdleTTL {
			delete(l.buckets, k)
		}
	}
}

// clientIP 从 RemoteAddr 提取 IP（不读 X-Forwarded-For）。
func clientIP(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	if addr == "" {
		return "unknown"
	}
	return addr
}
