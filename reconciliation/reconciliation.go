// Package reconciliation 提供对账比对原语：内部记录 vs 外部记录。
//
// 它只负责找出 missing/mismatch；修复动作由业务实现，且必须幂等。
package reconciliation

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Kind 是差异类型。
type Kind string

const (
	// MissingInternal 表示记录只存在于外部数据源，内部缺失。
	MissingInternal Kind = "missing_internal"
	// MissingExternal 表示记录只存在于内部数据源，外部缺失。
	MissingExternal Kind = "missing_external"
	// Mismatch 表示同一 key 的内外部记录不一致。
	Mismatch Kind = "mismatch"
)

// Diff 是一条差异。
type Diff[T any] struct {
	// Key 是记录的唯一键，由 [Comparator.Key] 生成。
	Key string
	// Kind 是差异类型，见 [MissingInternal]、[MissingExternal]、[Mismatch]。
	Kind Kind
	// Internal 是内部记录；[MissingInternal] 时为 nil。
	Internal *T
	// External 是外部记录；[MissingExternal] 时为 nil。
	External *T
}

// Comparator 定义内部/外部数据源与比较规则。
type Comparator[T any] struct {
	// Internal 加载内部记录；返回错误会让 [Run] 失败。
	Internal func(context.Context) ([]T, error)
	// External 加载外部记录；返回错误会让 [Run] 失败。
	External func(context.Context) ([]T, error)
	// Key 提取记录的唯一键；空键或重复键会让 [Run] 返回错误。
	Key func(T) string
	// Equal 判断同一 key 的内外部记录是否一致。
	Equal func(a, b T) bool
}

// Run 执行一次对账，返回按 key 排序的差异。
func Run[T any](ctx context.Context, c Comparator[T]) (out []Diff[T], err error) {
	start := time.Now()
	defer func() { recordRun(ctx, out, err, time.Since(start)) }()

	if c.Internal == nil || c.External == nil || c.Key == nil || c.Equal == nil {
		return nil, fmt.Errorf("reconciliation: Internal/External/Key/Equal are required")
	}
	internalRows, err := c.Internal(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: internal: %w", err)
	}
	externalRows, err := c.External(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: external: %w", err)
	}

	internal := make(map[string]T, len(internalRows))
	for _, row := range internalRows {
		key := c.Key(row)
		if key == "" {
			return nil, fmt.Errorf("reconciliation: empty internal key")
		}
		if _, exists := internal[key]; exists {
			return nil, fmt.Errorf("reconciliation: duplicate internal key %q", key)
		}
		internal[key] = row
	}
	external := make(map[string]T, len(externalRows))
	for _, row := range externalRows {
		key := c.Key(row)
		if key == "" {
			return nil, fmt.Errorf("reconciliation: empty external key")
		}
		if _, exists := external[key]; exists {
			return nil, fmt.Errorf("reconciliation: duplicate external key %q", key)
		}
		external[key] = row
	}

	var diffs []Diff[T]
	for key, in := range internal {
		ex, ok := external[key]
		if !ok {
			v := in
			diffs = append(diffs, Diff[T]{Key: key, Kind: MissingExternal, Internal: &v})
			continue
		}
		if !c.Equal(in, ex) {
			a, b := in, ex
			diffs = append(diffs, Diff[T]{Key: key, Kind: Mismatch, Internal: &a, External: &b})
		}
	}
	for key, ex := range external {
		if _, ok := internal[key]; !ok {
			v := ex
			diffs = append(diffs, Diff[T]{Key: key, Kind: MissingInternal, External: &v})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Key < diffs[j].Key })
	return diffs, nil
}

// Summary 统计各差异类型的数量。
func Summary[T any](diffs []Diff[T]) map[Kind]int {
	out := make(map[Kind]int)
	for _, d := range diffs {
		out[d.Kind]++
	}
	return out
}
