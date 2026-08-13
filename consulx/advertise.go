package consulx

import (
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fitan/fxkit/config"
)

type advertiseResult struct {
	Addr     string
	Explicit bool
}

// resolveAdvertise returns the address written into the Consul catalog.
// Priority: FXKIT_ADVERTISE_ADDRESS → discovery.advertise_address → auto-detect → 127.0.0.1.
func resolveAdvertise(cfg *config.Config) advertiseResult {
	if v := strings.TrimSpace(os.Getenv("FXKIT_ADVERTISE_ADDRESS")); v != "" {
		return advertiseResult{Addr: v, Explicit: true}
	}
	if cfg != nil {
		if v := strings.TrimSpace(cfg.Get().Discovery.AdvertiseAddress); v != "" {
			return advertiseResult{Addr: v, Explicit: true}
		}
	}
	consul := ""
	if cfg != nil {
		consul = strings.TrimSpace(cfg.Get().Discovery.ConsulAddress)
	}
	if ip := detectLocalIP(consul); ip != "" {
		return advertiseResult{Addr: ip}
	}
	return advertiseResult{Addr: "127.0.0.1"}
}

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

// detectLocalIP prefers the outbound NIC toward Consul when it is not a VPN
// (point-to-point) interface; otherwise picks the best non-loopback IPv4.
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
