package consulx

import (
	"net"
	"net/url"
	"strings"
	"time"
)

// advertiseResult 是 resolveAdvertise 的结果。
type advertiseResult struct {
	// Addr 写入 Consul catalog 的服务地址。
	Addr string
	// Explicit 地址是否来自 discovery.advertise_address；false 表示自动探测或回环兜底。
	Explicit bool
}

// resolveAdvertise 返回写入 Consul catalog 的地址。
// 优先级：discovery.advertise_address → 自动探测 → 127.0.0.1。
func resolveAdvertise(disc *Config) advertiseResult {
	if disc != nil {
		if v := strings.TrimSpace(disc.AdvertiseAddress); v != "" {
			return advertiseResult{Addr: v, Explicit: true}
		}
	}
	consul := ""
	if disc != nil {
		consul = strings.TrimSpace(disc.ConsulAddress)
	}
	if ip := detectLocalIP(consul); ip != "" {
		return advertiseResult{Addr: ip}
	}
	return advertiseResult{Addr: "127.0.0.1"}
}

// isLoopbackAddr 判断 addr 的主机部分是否为回环地址。
// 支持 host:port 形式，并额外把 localhost 视为回环。
func isLoopbackAddr(addr string) bool {
	host := strings.TrimSpace(addr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// detectLocalIP 优先选择通往 Consul 的出站网卡，前提是它不是 VPN
// （点对点）接口；否则挑选最合适的非回环 IPv4 地址。
func detectLocalIP(consulAddress string) string {
	if host := consulHostname(consulAddress); host != "" {
		if ip := outboundIP(net.JoinHostPort(host, "8500")); ip != "" && !isPointToPointIP(ip) {
			return ip
		}
	}
	for _, dest := range []string{"8.8.8.8:53", "1.1.1.1:53"} {
		if ip := outboundIP(dest); ip != "" && !isPointToPointIP(ip) {
			return ip
		}
	}
	return firstNonLoopbackIPv4()
}

// consulHostname 从 Consul 地址中解析主机名；空地址、回环地址或解析失败时返回 ""。
func consulHostname(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" || host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return ""
	}
	return host
}

// outboundIP 向 dest 发起 UDP 拨号（2s 超时），返回所用本机 IPv4。
// 拨号失败、非 UDP 地址或结果为回环/未指定地址时返回 ""。
func outboundIP(dest string) string {
	conn, err := net.DialTimeout("udp", dest, 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr == nil || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

// isPointToPointIP 判断 ipStr 是否属于某个点对点（如 VPN）网卡；
// IP 解析失败或枚举网卡失败时返回 false。
func isPointToPointIP(ipStr string) bool {
	want := net.ParseIP(ipStr)
	if want == nil {
		return false
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagPointToPoint == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipFromAddr(a).Equal(want) {
				return true
			}
		}
	}
	return false
}

// firstNonLoopbackIPv4 返回按网卡名打分（en/eth/wlan > bridge > 其他）选出的
// 非回环、非点对点 IPv4；没有可用地址时返回 ""。
func firstNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	type cand struct {
		ip    string
		score int
	}
	var best cand
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		score := 10
		name := strings.ToLower(iface.Name)
		switch {
		case strings.HasPrefix(name, "en") || strings.HasPrefix(name, "eth") || strings.HasPrefix(name, "wlan"):
			score = 100
		case strings.HasPrefix(name, "bridge"):
			score = 20
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip4 := ipFromAddr(a).To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
				continue
			}
			if score > best.score {
				best = cand{ip: ip4.String(), score: score}
			}
		}
	}
	return best.ip
}

// ipFromAddr 从 *net.IPNet 或 *net.IPAddr 中取出 IP，其他类型返回 nil。
func ipFromAddr(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}
