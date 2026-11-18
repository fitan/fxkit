package idempotency

import (
	"context"
	"time"
)

// Record 是一次幂等请求的持久化状态。
type Record struct {
	RequestHash  string
	StatusCode   int
	ContentType  string
	ResponseBody []byte
	Completed    bool
}

// Store 是幂等键存储。实现必须并发安全。
type Store interface {
	// Begin 尝试创建 key；created=true 表示本次请求拥有执行权。
	// created=false 时返回已有记录（可能未完成）。
	Begin(ctx context.Context, key, requestHash string, ttl time.Duration) (rec Record, created bool, err error)
	Complete(ctx context.Context, key string, status int, contentType string, body []byte) error
	Abort(ctx context.Context, key string) error
}
