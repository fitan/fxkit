package crudx

import "strings"

// ListParams 承载 ZStack 风格的列表查询参数（Q 值已完成 URL 解码）。
type ListParams struct {
	// Limit 页大小；<=0 时由 [ListSpec.Normalize] 填 [Limits.LimitDefault]，超过 [Limits.LimitMax] 会被截断。
	Limit int
	// Start offset 偏移；负数归一为 0，超过 [Limits.StartMax] 会被截断；设置 Cursor 后忽略。
	Start int
	// SortBy 排序字段名，须在 ListSpec 的白名单内；为空时使用 [ListSpec.DefaultSort]。
	SortBy        string
	SortDirection string // asc | desc
	// ReplyWithCount 为 true 时额外执行 Count 并返回 Total；与游标分页互斥，同时使用会返回 400。
	ReplyWithCount bool
	// Q ZStack 风格的 q= 条件列表（已完成 URL 解码）；每项形如 field=value，or(...) 表示 OR 组。
	Q         []string
	Cursor    string // 不透明的 keyset 游标；设置后 Start 被忽略
	UseCursor bool   // 即使首页（尚无 Cursor）也返回 nextCursor

	// sortByExplicit / sortDirExplicit 记录调用方在 Normalize 补齐默认值之前
	// 是否显式传入了排序字段/方向。游标翻页时以游标内的排序为准，只有显式
	// 冲突才报错，避免把空 direction 归一成的 asc 或 DefaultSort 误判为冲突。
	sortByExplicit bool
	// sortDirExplicit 记录调用方是否显式传入了排序方向（而非被归一出的默认 asc）。
	sortDirExplicit bool
	// normalized 记录 [ListSpec.Normalize] 是否已执行过；显式性只在首次调用时采集。
	normalized bool
}

// Limits 配置列表查询的保护限制。
type Limits struct {
	// MaxConditions q= 条件总数上限（含 or(...) 内各项），超出返回 400；<=0 时为 20。
	MaxConditions int
	// MaxInValues 单个 in / notin（以及多值 =）条件的元素个数上限；<=0 时为 50。
	MaxInValues int
	// MaxJoins 单次查询允许的关联 JOIN 数上限；<=0 时为 3。
	MaxJoins int
	// MaxLikeConds 模糊查询（~ / !~）的条件数量上限；<=0 时为 3。
	MaxLikeConds int
	// LimitDefault 未指定 limit 时的默认页大小；<=0 时为 100。
	LimitDefault int
	// LimitMax 页大小上限，超出会被截断；<=0 时为 1000。
	LimitMax int
	StartMax int // offset 上限，默认 LimitMax*100；防止巨大的 OFFSET 拖垮数据库
}

// normalized 返回把 <=0 字段填为内置默认值的副本，不改动 l。
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
