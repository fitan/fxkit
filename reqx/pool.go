package reqx

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// endpointPool 持有线程安全的 host:port 目标列表，并以 round-robin 方式选取。
type endpointPool struct {
	// mu 保护 endpoints。
	mu sync.RWMutex
	// endpoints 是当前端点列表，元素为 host:port 或裸 host。
	endpoints []string
	// rr 是 round-robin 游标。
	rr atomic.Uint64
}

// newEndpointPool 用归一化后的种子地址创建端点池。
func newEndpointPool(seeds []string) *endpointPool {
	p := &endpointPool{}
	p.replace(normalizeEndpoints(seeds))
	return p
}

// replace 用 eps 的副本整体替换端点列表（并发安全）。
func (p *endpointPool) replace(eps []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endpoints = append([]string(nil), eps...)
}

// endpointsEqual 报告两个端点列表排序后是否完全一致。
func endpointsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// snapshot 返回当前端点列表的副本。
func (p *endpointPool) snapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.endpoints...)
}

// len 返回当前端点数量。
func (p *endpointPool) len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.endpoints)
}

// next 按 round-robin 返回下一个端点；池为空时返回错误。
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

// normalizeEndpoints 解析并去重端点，丢弃无法解析的项，保留首次出现顺序。
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

// parseEndpoint 接受 "host"、"host:port"、"http://host:port/path"、"[ipv6]:port"。
// 返回适合作为 url.URL.Host 的 host:port（或裸 host）。
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
	// 裸 host 或 host:port / [ipv6]:port
	if host, port, err := net.SplitHostPort(s); err == nil {
		if strings.TrimSpace(host) == "" {
			return "", false
		}
		return net.JoinHostPort(host, port), true
	}
	// 无端口 —— 仍然有效（后续使用 scheme 默认端口，或仅为域名）。
	if strings.Contains(s, "/") {
		return "", false
	}
	return s, true
}

// joinHostPort 把 addr 与 port 拼成 host:port；addr 为空或 port<=0 时返回空串，
// addr 已含端口时原样返回。
func joinHostPort(addr string, port int) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || port <= 0 {
		return ""
	}
	// 地址可能已包含端口（不常见，但需要容忍这种情况）。
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, fmt.Sprintf("%d", port))
}
