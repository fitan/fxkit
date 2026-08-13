package reqx

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// endpointPool holds a thread-safe list of host:port targets and picks them round-robin.
type endpointPool struct {
	mu        sync.RWMutex
	endpoints []string
	rr        atomic.Uint64
}

func newEndpointPool(seeds []string) *endpointPool {
	p := &endpointPool{}
	p.replace(normalizeEndpoints(seeds))
	return p
}

func (p *endpointPool) replace(eps []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endpoints = append([]string(nil), eps...)
}

func (p *endpointPool) snapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.endpoints...)
}

func (p *endpointPool) len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.endpoints)
}

func (p *endpointPool) next() (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := len(p.endpoints)
	if n == 0 {
		return "", fmt.Errorf("reqx: no healthy endpoints")
	}
	i := p.rr.Add(1) - 1
	return p.endpoints[i%uint64(n)], nil
}

func normalizeEndpoints(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		ep, ok := parseEndpoint(s)
		if !ok {
			continue
		}
		if _, dup := seen[ep]; dup {
			continue
		}
		seen[ep] = struct{}{}
		out = append(out, ep)
	}
	return out
}

// parseEndpoint accepts "host", "host:port", "http://host:port/path", "[ipv6]:port".
// Returns host:port (or bare host) suitable for url.URL.Host.
func parseEndpoint(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", false
		}
		return u.Host, true
	}
	// Bare host or host:port / [ipv6]:port
	if host, port, err := net.SplitHostPort(s); err == nil {
		if strings.TrimSpace(host) == "" {
			return "", false
		}
		return net.JoinHostPort(host, port), true
	}
	// No port — still valid (scheme default port later, or domain-only).
	if strings.Contains(s, "/") {
		return "", false
	}
	return s, true
}

func joinHostPort(addr string, port int) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || port <= 0 {
		return ""
	}
	// Address may already include port (unusual but tolerate).
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, fmt.Sprintf("%d", port))
}
