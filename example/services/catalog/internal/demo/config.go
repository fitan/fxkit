package demo

import "strings"

// DynamicConfig 返回动态配置的当前快照。
// config.Dynamic 只保证指针原子替换，不负责重启框架对象（listener/db/auth/otel）。
func (s *Service) DynamicConfig() ConfigDemo {
	source := "static"
	welcome := ""
	if s.dynamic != nil {
		if c := s.dynamic.Get(); c != nil {
			welcome = strings.TrimSpace(c.Welcome)
			source = "dynamic"
		}
	}
	if welcome == "" {
		welcome = strings.TrimSpace(s.cfg.Welcome)
	}
	return ConfigDemo{Welcome: welcome, Source: source}
}
