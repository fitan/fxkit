package article

import (
	"time"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// ArticleListRow 是文章列表的 API 读模型。
// 它属于 feature 层：repo 返回 entity，由 service 负责投影。
type ArticleListRow struct {
	// ID 是文章 ID。
	ID int64 `json:"id"`
	// Title 是标题。
	Title string `json:"title"`
	// Status 是状态（draft/indexed）。
	Status string `json:"status"`
	// Author 是作者。
	Author string `json:"author,omitempty"`
}

// toArticleListRow 把实体转为列表读模型（不暴露 Body 等字段）。
func toArticleListRow(m entity.Article) ArticleListRow {
	return ArticleListRow{ID: m.ID, Title: m.Title, Status: m.Status, Author: m.Author}
}

// ArticleDetail 是文章接口的响应模型。
// Version 用于乐观锁：更新时必须原样带回。
type ArticleDetail struct {
	// ID 是文章 ID。
	ID int64 `json:"id"`
	// Title 是标题。
	Title string `json:"title"`
	// Body 是正文。
	Body string `json:"body,omitempty"`
	// Status 是状态。
	Status string `json:"status"`
	// Author 是作者。
	Author string `json:"author,omitempty"`
	// Version 是乐观锁版本。
	Version int64 `json:"version"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"createdAt"`
	// UpdatedAt 是更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
}

// CreateArticleReq 是创建文章的请求体。
type CreateArticleReq struct {
	// Title 是标题，必填且唯一。
	Title string `json:"title"`
	// Body 是正文。
	Body string `json:"body,omitempty"`
}

// UpdateArticleReq 是更新文章的请求体；Version 必填。
type UpdateArticleReq struct {
	// ID 是文章 ID（由路径注入）。
	ID string `json:"id,omitempty"`
	// Version 是乐观锁版本，必填。
	Version int64 `json:"version"`
	// Title 是标题。
	Title string `json:"title"`
	// Body 是正文。
	Body string `json:"body,omitempty"`
}

// toArticleDetail 把持久化模型转换为接口模型，避免 handler 直接暴露 entity。
func toArticleDetail(m entity.Article) ArticleDetail {
	return ArticleDetail{
		ID:        m.ID,
		Title:     m.Title,
		Body:      m.Body,
		Status:    m.Status,
		Author:    m.Author,
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
