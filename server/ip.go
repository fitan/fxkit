package server

import (
	"net"
	"net/http"
	"strings"
)

// clientIP 从 RemoteAddr 提取 IP（不读 X-Forwarded-For）。
// 访问日志等场景使用；真实客户端 IP 的信任链应交给网关。
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
