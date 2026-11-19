package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fitan/fxkit/gormx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KeyRecord 是 idempotency_keys 表。
type KeyRecord struct {
	// Key 是幂等键（含作用域前缀），主键，最长 128 字符。
	Key string `gorm:"primaryKey;size:128"`
	// RequestHash 是请求指纹（method+URI+body 的 SHA-256 hex），最长 64 字符。
	RequestHash string `gorm:"size:64;not null"`
	// StatusCode 是缓存响应的 HTTP 状态码；未完成时为 0。
	StatusCode int `gorm:"not null;default:0"`
	// ContentType 是缓存响应的 Content-Type，最长 255 字符。
	ContentType string `gorm:"size:255"`
	// ResponseBody 是缓存的响应体；未完成时为空。
	ResponseBody []byte
	// Completed 表示请求处理是否已结束；false 表示进行中。
	Completed bool `gorm:"not null;default:false"`
	// CreatedAt 是记录创建时间（UTC）。
	CreatedAt time.Time
	// ExpiresAt 是记录过期时间，带索引；PurgeExpired 按它分批删除。
	ExpiresAt time.Time `gorm:"index"`
}

// TableName 覆盖 GORM 默认表名。
func (KeyRecord) TableName() string { return "idempotency_keys" }

// DBStore 用数据库保存幂等键。
type DBStore struct {
	// client 是幂等键表所在的 GORM 客户端。
	client *gormx.Client
}

// NewDBStore 返回由 client 支撑的 DBStore。
func NewDBStore(client *gormx.Client) *DBStore { return &DBStore{client: client} }

// Migrate 创建/补齐 idempotency_keys 表；无可用数据库时返回错误。
func (s *DBStore) Migrate(ctx context.Context) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("idempotency: db store requires a configured database")
	}
	return s.client.Conn(ctx).AutoMigrate(&KeyRecord{})
}

// Begin 尝试以 key 插入新记录；created=true 表示本次请求获得执行权。
// key 已存在时返回已有记录；已过期则删除后重试一次。ttl<=0 时取 24h。
func (s *DBStore) Begin(ctx context.Context, key, requestHash string, ttl time.Duration) (Record, bool, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return Record{}, false, fmt.Errorf("idempotency: db store requires a configured database")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	for attempt := 0; attempt < 2; attempt++ {
		now := time.Now().UTC()
		row := KeyRecord{
			Key:         key,
			RequestHash: requestHash,
			CreatedAt:   now,
			ExpiresAt:   now.Add(ttl),
		}
		res := s.client.Conn(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if res.Error != nil {
			return Record{}, false, res.Error
		}
		if res.RowsAffected > 0 {
			return toRecord(row), true, nil
		}

		var cur KeyRecord
		err := s.client.Conn(ctx).Where("key = ?", key).First(&cur).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return Record{}, false, err
		}
		if cur.ExpiresAt.Before(now) {
			if err := s.client.Conn(ctx).Where("key = ?", key).Delete(&KeyRecord{}).Error; err != nil {
				return Record{}, false, err
			}
			continue
		}
		return toRecord(cur), false, nil
	}
	return Record{}, false, fmt.Errorf("idempotency: begin %q: concurrent state change", key)
}

// Complete 把记录标记为完成并写入缓存响应；记录不存在或已完成时返回错误。
func (s *DBStore) Complete(ctx context.Context, key string, status int, contentType string, body []byte) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("idempotency: db store requires a configured database")
	}
	res := s.client.Conn(ctx).Model(&KeyRecord{}).
		Where("key = ? AND completed = ?", key, false).
		Updates(map[string]any{
			"status_code":   status,
			"content_type":  contentType,
			"response_body": body,
			"completed":     true,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("idempotency: complete %q lost claim", key)
	}
	return nil
}

// Abort 删除未完成的记录，让同一 key 的后续请求可以重新执行。
func (s *DBStore) Abort(ctx context.Context, key string) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("idempotency: db store requires a configured database")
	}
	return s.client.Conn(ctx).Where("key = ? AND completed = ?", key, false).Delete(&KeyRecord{}).Error
}

// purgeBatchSize 是 PurgeExpired 每批删除的行数。
const purgeBatchSize = 1000

// PurgeExpired 分批删除 expires_at 早于 before 的行。
// 只删已过期 key；未过期的 in-progress/completed 行不受影响。
func (s *DBStore) PurgeExpired(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return 0, fmt.Errorf("idempotency: db store requires a configured database")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("idempotency: purge cutoff is required")
	}
	var total int64
	for {
		var keys []string
		if err := s.client.Conn(ctx).Model(&KeyRecord{}).
			Where("expires_at < ?", before).
			Order("key").Limit(purgeBatchSize).
			Pluck("key", &keys).Error; err != nil {
			return total, err
		}
		if len(keys) == 0 {
			return total, nil
		}
		res := s.client.Conn(ctx).Where("key IN ?", keys).Delete(&KeyRecord{})
		total += res.RowsAffected
		if res.Error != nil {
			return total, res.Error
		}
		if len(keys) < purgeBatchSize {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// toRecord 把数据库行转换为 Record。
func toRecord(row KeyRecord) Record {
	return Record{
		RequestHash:  row.RequestHash,
		StatusCode:   row.StatusCode,
		ContentType:  row.ContentType,
		ResponseBody: row.ResponseBody,
		Completed:    row.Completed,
	}
}
