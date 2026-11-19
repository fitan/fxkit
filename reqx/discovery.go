package reqx

// discoveryConfig 是 yaml discovery 段中 reqx 用到的子集。
type discoveryConfig struct {
	// ConsulAddress 是 Consul HTTP 地址，可省略 scheme（缺省补 http://）。
	ConsulAddress string `yaml:"consul_address"`
	// ConsulPassingOnly 只使用健康检查 passing 的实例；默认 true。
	ConsulPassingOnly bool `yaml:"consul_passing_only"`
}

// SetDefaults 把 ConsulPassingOnly 默认设为 true。
func (d *discoveryConfig) SetDefaults() {
	d.ConsulPassingOnly = true
}
