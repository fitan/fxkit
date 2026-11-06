// Package consuladdr 统一 Consul HTTP 地址的规范化。
//
// 之前 config（拉 KV）与 consulx（注册/发现）各自维护一份逐行相同的实现，
// 只修一处就会让两处行为不一致（例如未来支持 unix socket 或 IPv6 zone）。
package consuladdr

import (
	"fmt"
	"net/url"
	"strings"
)

// Normalize 补齐 scheme、去掉尾部斜杠，并校验 host 存在。
func Normalize(addr string) (string, error) {
	a := strings.TrimSpace(strings.TrimRight(addr, "/"))
	if a == "" {
		return "", fmt.Errorf("empty consul address")
	}
	if !strings.HasPrefix(a, "http://") && !strings.HasPrefix(a, "https://") {
		a = "http://" + a
	}
	u, err := url.Parse(a)
	if err != nil {
		return "", fmt.Errorf("invalid consul address %q: %w", addr, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid consul address %q: missing host", addr)
	}
	return strings.TrimRight(u.String(), "/"), nil
}
