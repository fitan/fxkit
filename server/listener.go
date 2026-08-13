package server

import "net"

// newListener 单独抽出，便于测试或特殊启动流程替换（例如 TLS listener）。
func newListener(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
