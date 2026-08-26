package reqx

// discoveryConfig 是 yaml discovery 段中 reqx 用到的子集。
type discoveryConfig struct {
	ConsulAddress     string `yaml:"consul_address"`
	ConsulPassingOnly bool   `yaml:"consul_passing_only"`
}

func (d *discoveryConfig) SetDefaults() {
	d.ConsulPassingOnly = true
}
