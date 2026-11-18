package comment

import (
	"time"

	"github.com/fitan/fxkit-example/services/catalog/internal/entity"
)

// CommentListRow 是评论列表/创建接口的 API 读模型。
// 它属于 feature 层：repo 返回 entity，由 service 投影。
type CommentListRow struct {
	// ID 是评论 ID。
	ID int64 `json:"id"`
	// ArticleID 是所属文章 ID。
	ArticleID int64 `json:"articleId"`
	// Body 是评论正文。
	Body string `json:"body"`
	// Note 是可选的备注。
	Note *string `json:"note,omitempty"`
	// Author 是作者。
	Author string `json:"author,omitempty"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"createdAt"`
}

// toCommentListRow 把实体转为评论读模型。
func toCommentListRow(m entity.Comment) CommentListRow {
	return CommentListRow{
		ID:        m.ID,
		ArticleID: m.ArticleID,
		Body:      m.Body,
		Note:      m.Note,
		Author:    m.Author,
		CreatedAt: m.CreatedAt,
	}
}

// CreateCommentReq 是创建评论的请求体。
type CreateCommentReq struct {
	// ArticleID 是所属文章 ID，必填。
	ArticleID int64 `json:"articleId"`
	// Body 是评论正文，必填。
	Body string `json:"body"`
	// Note 是可选备注。
	Note *string `json:"note,omitempty"`
}
