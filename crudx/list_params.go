package crudx

import "strings"

// ListParams 承载 ZStack 风格的列表查询参数（Q 值已完成 URL 解码）。
type ListParams struct {
	Limit          int
	Start          int
	SortBy         string
	SortDirection  string // asc | desc
	ReplyWithCount bool
	Q              []string
	Cursor         string // 不透明的 keyset 游标；设置后 Start 被忽略
	UseCursor      bool   // 即使首页（尚无 Cursor）也返回 nextCursor

	// sortByExplicit / sortDirExplicit 记录调用方在 Normalize 补齐默认值之前
	// 是否显式传入了排序字段/方向。游标翻页时以游标内的排序为准，只有显式
	// 冲突才报错，避免把空 direction 归一成的 asc 或 DefaultSort 误判为冲突。
	sortByExplicit  bool
	sortDirExplicit bool
	normalized      bool
}

// Limits 配置列表查询的保护限制。
type Limits struct {
	MaxConditions int
	MaxInValues   int
	MaxJoins      int
	MaxLikeConds  int
	LimitDefault  int
	LimitMax      int
	StartMax      int // offset 上限，默认 LimitMax*100；防止巨大的 OFFSET 拖垮数据库
}

func (l Limits) normalized() Limits {
	out := l
	if out.MaxConditions <= 0 {
		out.MaxConditions = 20
	}
	if out.MaxInValues <= 0 {
		out.MaxInValues = 50
	}
	if out.MaxJoins <= 0 {
		out.MaxJoins = 3
	}
	if out.MaxLikeConds <= 0 {
		out.MaxLikeConds = 3
	}
	if out.LimitDefault <= 0 {
		out.LimitDefault = 100
	}
	if out.LimitMax <= 0 {
		out.LimitMax = 1000
	}
	if out.StartMax <= 0 {
		out.StartMax = out.LimitMax * 100
	}
	return out
}

// Normalize 将 spec 中的默认值与上限应用到 params（会修改 p）。
func (spec *ListSpec) Normalize(p *ListParams) {
	if p == nil {
		return
	}
	lim := spec.Limits.normalized()
	if p.Limit <= 0 {
		p.Limit = lim.LimitDefault
	}
	if p.Limit > lim.LimitMax {
		p.Limit = lim.LimitMax
	}
	if p.Start < 0 {
		p.Start = 0
	}
	if p.Start > lim.StartMax {
		p.Start = lim.StartMax
	}
	// 只在第一次 Normalize 时记录显式性（重复调用不能把补齐的默认值当成显式输入）。
	if !p.normalized {
		p.sortByExplicit = strings.TrimSpace(p.SortBy) != ""
		p.sortDirExplicit = strings.TrimSpace(p.SortDirection) != ""
		p.normalized = true
	}
	p.SortBy = strings.TrimSpace(p.SortBy)
	p.SortDirection = normalizeSortDir(p.SortDirection)
	if p.SortBy == "" && spec.DefaultSort != "" {
		if by, dir := ParseSort(spec.DefaultSort); by != "" {
			p.SortBy, p.SortDirection = by, dir
		}
	}
	p.Cursor = strings.TrimSpace(p.Cursor)
	if p.Cursor != "" {
		p.UseCursor = true
		p.Start = 0
	}
}
