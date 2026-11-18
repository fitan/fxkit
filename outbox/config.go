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
	// PublishConcurrency 同一批内的并发投递数。默认 1：按 id 串行投递。
	//
	// 未设 [PartitionField] 时：失败不挡住同批后面的事件；>1 也不保证批内顺序。
	// 设了 [PartitionField] 时：它是「并行组数」上限。同一分组内按 id 串行，
	// 当前事件没成功就停住该组的本批剩余；不同分组并行。
	// 不保证跨批重试的同 key 顺序：组头进入退避后，组尾的租约到期可能先被发出。
	// 多副本也不保证同 key 全序。语义始终是 at-least-once。
	PublishConcurrency int `yaml:"publish_concurrency"`
	// PublishTimeout 单条事件投递超时，默认 30s。
	// <=0 表示不设置超时（不推荐：卡住的 publisher 会阻塞整个批次与 Stop）。
	PublishTimeout time.Duration `yaml:"publish_timeout"`
	// RetryBase 首次失败后的退避基数，默认 2s；<=0 表示不退避（立即重试）。
	RetryBase time.Duration `yaml:"retry_base"`
	// RetryMax 退避上限，默认 5m。
	RetryMax time.Duration `yaml:"retry_max"`
	// RetryJitterPercent 退避抖动百分比（0-50），默认 20。
	RetryJitterPercent int `yaml:"retry_jitter_percent"`
	// InboxClaimTimeout Inbox 认领行的租约时长，默认 5m。
	// 处理中的消息崩溃后，超过该时间可被重新认领。
	InboxClaimTimeout time.Duration `yaml:"inbox_claim_timeout"`
	// PartitionField 是 payload 中用于分组顺序的 JSON 字段（如 "actorId"）。
	//
	// 为空 = 不分组（默认）。设置后，单个 relay 在同一批内按该字段分组：
	// 同值串行且失败即停，不同值可并行。多副本之间不保证同 key 的全序。
	PartitionField string `yaml:"partition_field"`
	// PurgeInterval 自动清理间隔；0 = 关闭（默认）。开启后需要数据库。
	PurgeInterval time.Duration `yaml:"purge_interval"`
	// PurgeBefore 清理 published/failed 行的时间阈值，默认 168h。
	PurgeBefore time.Duration `yaml:"purge_before"`
	// InboxPurgeBefore 清理 done inbox 行的时间阈值，默认 168h；0 = 不清理 inbox。
	InboxPurgeBefore time.Duration `yaml:"inbox_purge_before"`
}

// SetDefaults 填入编译期默认值。
func (c *Config) SetDefaults() {
	c.PollInterval = time.Second
	c.BatchSize = 50
	c.MaxRetries = 10
	c.ClaimTimeout = 30 * time.Second
	c.PublishConcurrency = 1
	c.PublishTimeout = 30 * time.Second
	c.RetryBase = 2 * time.Second
	c.RetryMax = 5 * time.Minute
	c.RetryJitterPercent = 20
	c.InboxClaimTimeout = 5 * time.Minute
	c.PurgeBefore = 168 * time.Hour
	c.InboxPurgeBefore = 168 * time.Hour
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
	if c.PublishTimeout < 0 {
		return fmt.Errorf("outbox.publish_timeout must be >= 0")
	}
	if c.RetryBase < 0 {
		return fmt.Errorf("outbox.retry_base must be >= 0")
	}
	if c.RetryMax < 0 {
		return fmt.Errorf("outbox.retry_max must be >= 0")
	}
	if c.RetryMax > 0 && c.RetryBase > c.RetryMax {
		return fmt.Errorf("outbox.retry_base must be <= outbox.retry_max")
	}
	if c.RetryJitterPercent < 0 || c.RetryJitterPercent > 50 {
		return fmt.Errorf("outbox.retry_jitter_percent must be between 0 and 50")
	}
	if c.InboxClaimTimeout < 0 {
		return fmt.Errorf("outbox.inbox_claim_timeout must be >= 0")
	}
	if c.PurgeInterval < 0 {
		return fmt.Errorf("outbox.purge_interval must be >= 0")
	}
	if c.PurgeBefore < 0 {
		return fmt.Errorf("outbox.purge_before must be >= 0")
	}
	if c.InboxPurgeBefore < 0 {
		return fmt.Errorf("outbox.inbox_purge_before must be >= 0")
	}
	if c.PurgeInterval > 0 && c.PurgeBefore <= 0 {
		return fmt.Errorf("outbox.purge_before must be > 0 when purge_interval is set")
	}
	return nil
}
