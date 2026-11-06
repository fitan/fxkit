package huma

import (
	"strings"

	"github.com/fitan/fxkit/crudx"
)

// ListQueryInput 是标准的 Huma GET 列表查询参数集（ZStack 风格）。
type ListQueryInput struct {
	Q              []string `query:"q,explode" doc:"Filter conditions (AND). Example: state=Running. OR group: or(a=1,b=2)"`
	Limit          int      `query:"limit" default:"100" minimum:"1" maximum:"1000" doc:"Max rows per page"`
	Start          int      `query:"start" default:"0" minimum:"0" doc:"Offset from 0 (ignored when cursor is set)"`
	SortBy         string   `query:"sortBy" doc:"Sort field, supports relation paths e.g. cluster.name"`
	SortDirection  string   `query:"sortDirection" enum:"asc,desc" default:"asc"`
	ReplyWithCount bool     `query:"replyWithCount" default:"false" doc:"Include total count (not with cursor)"`
	Cursor         string   `query:"cursor" doc:"Opaque keyset cursor from previous nextCursor"`
	UseCursor      bool     `query:"useCursor" default:"false" doc:"Return nextCursor on first page"`
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
