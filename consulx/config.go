package consulx

import (
	"fmt"
	"strings"
)

// Config 服务注册与发现（yaml: discovery）。consulx 自注册与 reqx 出站发现共用此段。
type Config struct {
	// ConsulAddress Consul HTTP 地址。空 = 跳过注册与发现。与 --consul（拉配置）相互独立。
	ConsulAddress string `yaml:"consul_address"`
	// ConsulPassingOnly reqx watch 是否只取 healthy 实例。默认 true。
	ConsulPassingOnly bool `yaml:"consul_passing_only"`
	// Register 为 true 时以 app.name 自注册（TTL 心跳），停机注销。默认 false。
	Register bool `yaml:"register"`
	// AdvertiseAddress 写入 Consul catalog 的地址。空则自动探测本机非 VPN IPv4。
	AdvertiseAddress string `yaml:"advertise_address"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.ConsulPassingOnly = true
}

// Validate：register=true 时必须有 consul_address。
func (c Config) Validate() error {
	if c.Register && strings.TrimSpace(c.ConsulAddress) == "" {
		return fmt.Errorf("discovery.register=true requires discovery.consul_address")
	}
	return nil
}
