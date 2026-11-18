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
	Key          string `gorm:"primaryKey;size:128"`
	RequestHash  string `gorm:"size:64;not null"`
	StatusCode   int    `gorm:"not null;default:0"`
	ContentType  string `gorm:"size:255"`
	ResponseBody []byte
	Completed    bool `gorm:"not null;default:false"`
	CreatedAt    time.Time
	ExpiresAt    time.Time `gorm:"index"`
}

func (KeyRecord) TableName() string { return "idempotency_keys" }

// DBStore 用数据库保存幂等键。
type DBStore struct{ client *gormx.Client }

func NewDBStore(client *gormx.Client) *DBStore { return &DBStore{client: client} }

func (s *DBStore) Migrate(ctx context.Context) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("idempotency: db store requires a configured database")
	}
	return s.client.Conn(ctx).AutoMigrate(&KeyRecord{})
}

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

func (s *DBStore) Abort(ctx context.Context, key string) error {
	if s == nil || s.client == nil || !s.client.Enabled() {
		return fmt.Errorf("idempotency: db store requires a configured database")
	}
	return s.client.Conn(ctx).Where("key = ? AND completed = ?", key, false).Delete(&KeyRecord{}).Error
}

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

func toRecord(row KeyRecord) Record {
	return Record{
		RequestHash:  row.RequestHash,
		StatusCode:   row.StatusCode,
		ContentType:  row.ContentType,
		ResponseBody: row.ResponseBody,
		Completed:    row.Completed,
	}
}
