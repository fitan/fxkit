package entity

import "time"

// Counter 是 actor 演示用的计数器表：一个 actorID 一行。
type Counter struct {
	// ActorID 是 actor 实例 ID（主键）。
	ActorID string `gorm:"primaryKey;size:64"`
	// Value 是当前计数值。
	Value int64 `gorm:"not null"`
	// UpdatedAt 是最后更新时间。
	UpdatedAt time.Time `json:"-"`
}

// TableName 覆盖 GORM 默认表名。
func (Counter) TableName() string { return "catalog_counters" }
