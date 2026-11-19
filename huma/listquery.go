package huma

import (
	"strings"

	"github.com/fitan/fxkit/crudx"
)

// ListQueryInput 是标准的 Huma GET 列表查询参数集（ZStack 风格）。
type ListQueryInput struct {
	// Q 是过滤条件（AND 组合），ZStack q= 语法；空串条目会被丢弃。
	Q []string `query:"q,explode" doc:"Filter conditions (AND). Example: state=Running. OR group: or(a=1,b=2)"`
	// Limit 是每页最大行数，默认 100，范围 1..1000。
	Limit int `query:"limit" default:"100" minimum:"1" maximum:"1000" doc:"Max rows per page"`
	// Start 是从 0 开始的偏移量，默认 0；设置 Cursor 时被忽略。
	Start int `query:"start" default:"0" minimum:"0" doc:"Offset from 0 (ignored when cursor is set)"`
	// SortBy 是排序字段，支持关联路径（如 cluster.name）；为空时用列表 spec 的默认排序。
	SortBy string `query:"sortBy" doc:"Sort field, supports relation paths e.g. cluster.name"`
	// SortDirection 是排序方向，asc 或 desc，默认 asc。
	SortDirection string `query:"sortDirection" enum:"asc,desc" default:"asc"`
	// ReplyWithCount 为 true 时返回总数；与游标分页互斥。
	ReplyWithCount bool `query:"replyWithCount" default:"false" doc:"Include total count (not with cursor)"`
	// Cursor 是上一页 nextCursor 返回的不透明 keyset 游标。
	Cursor string `query:"cursor" doc:"Opaque keyset cursor from previous nextCursor"`
	// UseCursor 为 true 时首页（无 Cursor）也返回 nextCursor，默认 false。
	UseCursor bool `query:"useCursor" default:"false" doc:"Return nextCursor on first page"`
}

// ListParamsFromInput 将 Huma 绑定的查询参数映射为 [crudx.ListParams]。
//
// 注意：Huma 的 query 绑定底层是 net/url，值已经解码过，这里**不能**再解码。
// 早期实现调了 crudx.ParseHTTPQValues，导致客户端搜索字面量 "%20" 被
// 二次解码成空格，过滤条件被静默改写。
func ListParamsFromInput(in *ListQueryInput) (crudx.ListParams, error) {
	return crudx.ListParams{
		Limit:          in.Limit,
		Start:          in.Start,
		SortBy:         in.SortBy,
		SortDirection:  in.SortDirection,
		ReplyWithCount: in.ReplyWithCount,
		Q:              normalizeQValues(in.Q),
		Cursor:         in.Cursor,
		UseCursor:      in.UseCursor,
	}, nil
}

// normalizeQValues 去掉空条件并修剪首尾空白，但不做 URL 解码。
func normalizeQValues(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}
