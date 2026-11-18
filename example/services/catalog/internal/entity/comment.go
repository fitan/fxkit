package entity

import (
	"time"

	"gorm.io/gorm"
)

// Comment 是第二个资源，用于演示显式 Huma Register 以及 crudx 的
// q= / cursor / JOIN / isnull。
type Comment struct {
	// ID 是自增主键。
	ID int64 `gorm:"primaryKey" json:"-"`
	// ArticleID 是所属文章 ID。
	ArticleID int64 `gorm:"index;not null" json:"-"`
	// Body 是评论正文。
	Body string `gorm:"type:text" json:"-"`
	// Note 是可选备注。
	Note *string `gorm:"size:255" json:"-"`
	// Author 是作者。
	Author string `gorm:"size:128;index" json:"-"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"-"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"-"`
	// DeletedAt 是软删除时间。
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 覆盖 GORM 默认表名。
func (Comment) TableName() string { return "comments" }
