package idempotency

import (
	"context"
	"time"
)

// Record 是一次幂等请求的持久化状态。
type Record struct {
	// RequestHash 是首次请求的指纹；与本次请求不一致时视为 key 被复用。
	RequestHash string
	// StatusCode 是缓存响应的 HTTP 状态码。
	StatusCode int
	// ContentType 是缓存响应的 Content-Type；为空时不回写。
	ContentType string
	// ResponseBody 是缓存的响应体。
	ResponseBody []byte
	// Completed 表示请求处理是否已结束；false 表示仍在进行中。
	Completed bool
}

// Store 是幂等键存储。实现必须并发安全。
type Store interface {
	// Begin 尝试创建 key；created=true 表示本次请求拥有执行权。
	// created=false 时返回已有记录（可能未完成）。
	Begin(ctx context.Context, key, requestHash string, ttl time.Duration) (rec Record, created bool, err error)
	// Complete 写入响应并标记完成，后续同 key 请求直接回放该响应。
	Complete(ctx context.Context, key string, status int, contentType string, body []byte) error
	// Abort 删除未完成的记录，让同 key 请求可以重试；已完成记录不受影响。
	Abort(ctx context.Context, key string) error
}
