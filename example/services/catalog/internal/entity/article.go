package entity

import "time"

// Article 是 GORM 模型。JSON tag 都是 "-"，因为 API 使用读投影/DTO。
//
// 过滤/排序白名单见 repository.NewArticleRepo 里的 crudx.ListSpec。
type Article struct {
	// ID 是自增主键。
	ID int64 `gorm:"primaryKey" json:"-"`
	// Title 是标题（唯一索引）。
	Title string `gorm:"size:255;not null;index" json:"-"`
	// Body 是正文。
	Body string `gorm:"type:text" json:"-"`
	// Status 是状态（draft/indexed）。
	Status string `gorm:"size:32;not null;index" json:"-"`
	// Author 是作者。
	Author string `gorm:"size:128;index" json:"-"`
	// Version 是乐观锁版本。
	Version int64 `gorm:"not null;default:1" json:"-"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"-"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"-"`
}

// TableName 覆盖 GORM 默认表名。
func (Article) TableName() string { return "articles" }

// GetVersion/SetVersion 让 gormx.UpdateWithVersion 可以做乐观锁。
func (a *Article) GetVersion() int64 { return a.Version }

// SetVersion 写回版本号，供乐观锁更新使用。
func (a *Article) SetVersion(v int64) { a.Version = v }
