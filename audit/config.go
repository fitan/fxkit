// Package audit 提供 opt-in 的 HTTP 审计日志。
//
// 默认关闭；只记录请求元数据（actor/org/method/path/status/requestId/traceId/ip），
// body/headers 需要显式开启并脱敏。业务级审计（如“修改了价格”）仍应由业务显式写入。
package audit

import "time"

// Config 是 audit 段配置。
type Config struct {
	Enabled        bool          `yaml:"enabled"`
	Sink           string        `yaml:"sink"` // slog | db | outbox
	Topic          string        `yaml:"topic"`
	IncludeBody    bool          `yaml:"include_body"`
	IncludeHeaders bool          `yaml:"include_headers"`
	RedactHeaders  []string      `yaml:"redact_headers"`
	Retention      time.Duration `yaml:"retention"`
}

func (c *Config) SetDefaults() {
	c.Sink = "slog"
	c.Topic = "audit-event"
	c.RedactHeaders = []string{"Authorization", "Cookie", "Set-Cookie"}
	c.Retention = 720 * time.Hour
}

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

type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }
