package entity

import "time"

// ArticleFanout 是同一个 Hatchet 事件的第二个消费者（按 task 名称 fan-out）的落库结果。
//
// 该结构体目前直接作为 /article-fanout 的响应体，因此保留默认 JSON 字段名。
type ArticleFanout struct {
	// ID 是自增主键。
	ID int64 `gorm:"primaryKey"`
	// ArticleID 是原文章 ID。
	ArticleID int64 `gorm:"index;not null"`
	// Title 是事件里的标题快照。
	Title string `gorm:"size:255"`
	// CreatedAt 是写入时间。
	CreatedAt time.Time
}

// TableName 覆盖 GORM 默认表名。
func (ArticleFanout) TableName() string { return "catalog_article_fanout" }
