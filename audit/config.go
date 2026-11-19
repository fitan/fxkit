// Package audit 提供 opt-in 的 HTTP 审计日志。
//
// 默认关闭；只记录请求元数据（actor/org/method/path/status/requestId/traceId/ip），
// body/headers 需要显式开启并脱敏。业务级审计（如“修改了价格”）仍应由业务显式写入。
package audit

import "time"

// Config 是 audit 段配置。
type Config struct {
	// Enabled 审计总开关，默认 false；关闭时不挂中间件、不产生事件。
	Enabled bool `yaml:"enabled"`
	// Sink 审计事件落点，可选 slog / db / outbox，默认 slog。
	Sink string `yaml:"sink"` // slog | db | outbox
	// Topic 是 outbox sink 写入的 topic，默认 audit-event；slog/db sink 忽略。
	Topic string `yaml:"topic"`
	// IncludeBody 为 true 时记录请求体到 metadata.body，最多保留 8 KiB。
	IncludeBody bool `yaml:"include_body"`
	// IncludeHeaders 为 true 时记录请求头到 metadata.headers（按 RedactHeaders 脱敏）。
	IncludeHeaders bool `yaml:"include_headers"`
	// RedactHeaders 是需脱敏的请求头名（大小写不敏感），默认 Authorization / Cookie / Set-Cookie。
	RedactHeaders []string `yaml:"redact_headers"`
	// Retention 审计记录保留时长，默认 720h；Validate 要求 >= 0，框架本身不做清理。
	Retention time.Duration `yaml:"retention"`
}

// SetDefaults 设置默认值：Sink=slog、Topic=audit-event、RedactHeaders 为 Authorization/Cookie/Set-Cookie、Retention=720h。
func (c *Config) SetDefaults() {
	c.Sink = "slog"
	c.Topic = "audit-event"
	c.RedactHeaders = []string{"Authorization", "Cookie", "Set-Cookie"}
	c.Retention = 720 * time.Hour
}

// Validate 校验 Sink 取值（空/slog/db/outbox）与 Retention 非负，不合法时返回 *configError。
func (c Config) Validate() error {
	switch c.Sink {
	case "", "slog", "db", "outbox":
	default:
		return &configError{msg: "audit.sink must be slog, db or outbox"}
	}
	if c.Retention < 0 {
		return &configError{msg: "audit.retention must be >= 0"}
	}
	return nil
}

// configError 是 audit 配置校验错误。
type configError struct {
	// msg 是返回给调用方的错误消息。
	msg string
}

// Error 返回配置错误消息。
func (e *configError) Error() string { return e.msg }
