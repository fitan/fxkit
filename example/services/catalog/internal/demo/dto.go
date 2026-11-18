package demo

import "time"

// DemoConfig 是 demo 段配置。可被 Consul KV 热更新（config.ProvideWatch）。
type DemoConfig struct {
	// Welcome 是热更新演示用的欢迎语。
	Welcome string `yaml:"welcome"`
}

// SetDefaults 给 demo 段一个可用的默认欢迎语。
func (c *DemoConfig) SetDefaults() { c.Welcome = "hello fxkit" }

// DemoInput 是 Hatchet 任务 / 工作流的输入。
type DemoInput struct {
	// Name 是任务/工作流输入名。
	Name string `json:"name"`
	// DelayMs 是可选延迟（定时触发用）。
	DelayMs int `json:"delayMs,omitempty"`
}

// DemoOutput 是 Hatchet 任务 / 工作流的输出。
type DemoOutput struct {
	// Name 是处理的对象名。
	Name string `json:"name"`
	// Message 是处理结果描述。
	Message string `json:"message"`
	// Worker 是执行 worker 标识。
	Worker string `json:"worker"`
	// At 是执行时间。
	At time.Time `json:"at"`
}

// CacheDemo 是 cachex.GetOrSet 的返回：LoadedAt 相同说明命中了缓存，
// Loads 是进程内 loader 的实际执行次数（并发请求应被 singleflight 合并）。
type CacheDemo struct {
	// Key 是缓存 key。
	Key string `json:"key"`
	// Value 是缓存值。
	Value string `json:"value"`
	// LoadedAt 是 loader 执行时间（相同 = 命中）。
	LoadedAt time.Time `json:"loadedAt"`
	// Loads 是进程内 loader 执行次数。
	Loads int64 `json:"loads"`
}

// ConfigDemo 展示动态配置的当前快照。
type ConfigDemo struct {
	// Welcome 是当前配置值。
	Welcome string `json:"welcome"`
	// Source 是来源（dynamic/static）。
	Source string `json:"source"`
}

// LockDemo 展示分布式锁的 fencing token。
type LockDemo struct {
	// Key 是实际锁 key。
	Key string `json:"key"`
	// FencingToken 是本次持有的单调 token。
	FencingToken uint64 `json:"fencingToken"`
	// HeldMs 是人为持锁时长。
	HeldMs int64 `json:"heldMs"`
}

// OutboxFailed 是 outbox DLQ 的一行。
type OutboxFailed struct {
	// ID 是 outbox 行 ID。
	ID uint64 `json:"id"`
	// Topic 是事件 topic。
	Topic string `json:"topic"`
	// Attempts 是已重试次数。
	Attempts int `json:"attempts"`
	// LastError 是最后一次错误。
	LastError string `json:"lastError,omitempty"`
	// CreatedAt 是入队时间。
	CreatedAt time.Time `json:"createdAt"`
}

// RequeueOutboxReq 是重放 failed 事件的选择条件。
type RequeueOutboxReq struct {
	// Topics 非空时只重放这些 topic。
	Topics []string `json:"topics,omitempty"`
	// Limit 是最多重放条数。
	Limit int `json:"limit,omitempty"`
}

// RequeueOutboxResult 返回本次实际重置为 pending 的行数。
type RequeueOutboxResult struct {
	// Requeued 是实际重置为 pending 的行数。
	Requeued int64 `json:"requeued"`
}

// RateLimitReq 创建/更新 Hatchet 原生限流规则。
type RateLimitReq struct {
	// Key 是限流规则 key。
	Key string `json:"key"`
	// Limit 是每分钟允许次数。
	Limit int `json:"limit"`
}

// TxDemo 展示 gormx.AfterCommit：Hooks 只在事务提交后递增。
type TxDemo struct {
	// Committed 表示事务是否提交。
	Committed bool `json:"committed"`
	// Hooks 是 AfterCommit 实际执行次数。
	Hooks int64 `json:"hooks"`
}
