package entity

import "time"

// Heartbeat 记录提醒器（reminder）每个 actor 的 tick 次数。
type Heartbeat struct {
	// ActorID 是 actor 实例 ID（主键）。
	ActorID string `gorm:"primaryKey;size:64"`
	// Ticks 是累计 tick 次数。
	Ticks int64 `gorm:"not null"`
	// LastTick 是最后一次 tick 时间。
	LastTick time.Time `gorm:"not null"`
	// UpdatedAt 是最后更新时间。
	UpdatedAt time.Time
}

// TableName 覆盖 GORM 默认表名。
func (Heartbeat) TableName() string { return "catalog_heartbeats" }
