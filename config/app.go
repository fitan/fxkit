package config

import (
	"fmt"
	"strings"
)

// App 是跨模块的应用标识（yaml: app）。
// consulx 注册名、Huma OpenAPI 标题；otel.service_name 为空时回退到此值。
type App struct {
	Name string `yaml:"name"`
}

// SetDefaults 填入编译期默认值。由 [Load] 在解码 YAML 之前调用。
func (a *App) SetDefaults() {
	a.Name = "fxkit-app"
}

// Validate 拒绝空 app.name。
func (a App) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("app.name is required")
	}
	return nil
}
