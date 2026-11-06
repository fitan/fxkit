package outbox

import (
	"fmt"
	"strings"
	"time"
)

// Config 事务性 outbox relay（yaml: outbox）。需有 [EventPublisher]（hatchet.outbox_publisher 或 [ProvidePublisher]）才真正投递。
type Config struct {
	// Enabled 启动 relay。默认 false。
	Enabled bool `yaml:"enabled"`
	// PollInterval 轮询间隔。默认 1s；必须 > 0。
	PollInterval time.Duration `yaml:"poll_interval"`
	// BatchSize 每轮领取条数，默认 50。
	BatchSize int `yaml:"batch_size"`
	// MaxRetries 投递失败最大重试次数，默认 10。
	MaxRetries int `yaml:"max_retries"`
	// ClaimTimeout processing 租约时长。默认 30s；必须 > 0。
	ClaimTimeout time.Duration `yaml:"claim_timeout"`
	// PublishConcurrency 同一批内的并发投递数。默认 1（严格顺序）。
	//
	// 未设 [PartitionField] 时：>1 可提升吞吐，但不保证同一批内的投递顺序。
	// 设了 [PartitionField] 时：它变成「并行组数」——同一分组内严格串行，
	// 不同分组并行，因此每个 key 的事件仍然按产生顺序投递。
	// 语义始终是 at-least-once。
	PublishConcurrency int `yaml:"publish_concurrency"`
	// PartitionField 是 payload 中用于分组顺序的 JSON 字段（如 "actorId"）。
	//
	// 为空 = 不分组（默认）。设置后，同一字段值的事件串行投递，
	// 不同值的事件可并行——用于「高吞吐 + 同一 actor 保序」。
	PartitionField string `yaml:"partition_field"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.PollInterval = time.Second
	c.BatchSize = 50
	c.MaxRetries = 10
	c.ClaimTimeout = 30 * time.Second
	c.PublishConcurrency = 1
}

// Validate 拒绝非正间隔/批次。非法值启动失败。
func (c Config) Validate() error {
	if c.PollInterval <= 0 {
		return fmt.Errorf("outbox.poll_interval must be > 0")
	}
	if c.ClaimTimeout <= 0 {
		return fmt.Errorf("outbox.claim_timeout must be > 0")
	}
	if c.BatchSize <= 0 {
		return fmt.Errorf("outbox.batch_size must be > 0")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("outbox.max_retries must be >= 0")
	}
	// partition_field 必须是简单的 JSON 路径（字段名或 a.b）。
	if f := strings.TrimSpace(c.PartitionField); strings.ContainsAny(f, " \t\n*?") {
		return fmt.Errorf("outbox.partition_field %q must be a simple JSON path", c.PartitionField)
	}
	if c.PublishConcurrency < 0 {
		return fmt.Errorf("outbox.publish_concurrency must be >= 0")
	}
	return nil
}
