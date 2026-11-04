package crudx_test

import (
	"testing"

	"github.com/fitan/fxkit/crudx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFieldSpec_IndexedAndLikeableAreIndependent 是回归测试：
// 过去只有一个 Indexed 字段兼任「数据库有索引」和「允许 ~ 模糊查询」两件事，
// 导致有索引的数值列（如 author_id）被迫可以 LIKE，语义荒谬且破坏安全边界。
//
// 现在 Likeable 独立控制模糊查询；Indexed 只反映真实索引情况。
func TestFieldSpec_IndexedAndLikeableAreIndependent(t *testing.T) {
	spec := crudx.ListSpec{
		Fields: map[string]crudx.FieldSpec{
			// 有索引、但**不允许**模糊匹配（数值列）
			"author_id": {Column: "articles.author_id", Kind: crudx.FieldNumber, Indexed: true},
			// 有索引且允许模糊匹配
			"title": {Column: "articles.title", Kind: crudx.FieldString, Indexed: true, Likeable: true},
			// 无索引但显式允许（TEXT 列，承认全表扫描）
			"body": {Column: "articles.body", Kind: crudx.FieldString, Likeable: true},
		},
	}

	_, err := crudx.ParseQConditions(spec, []string{"author_id~123"})
	require.Error(t, err, "indexed numeric column must not allow LIKE just because it has an index")

	_, err = crudx.ParseQConditions(spec, []string{"title~alp"})
	assert.NoError(t, err, "Likeable column must allow LIKE")

	_, err = crudx.ParseQConditions(spec, []string{"body~text"})
	assert.NoError(t, err, "Likeable without index must still allow LIKE")
}

// TestFieldSpec_IndexAloneDoesNotGrantLike 明确锁定：
// 只有 Indexed 不再意味着可 LIKE（这是本次拆分的核心目的）。
func TestFieldSpec_IndexAloneDoesNotGrantLike(t *testing.T) {
	spec := crudx.ListSpec{
		Fields: map[string]crudx.FieldSpec{
			"name": {Column: "users.name", Kind: crudx.FieldString, Indexed: true},
		},
	}
	_, err := crudx.ParseQConditions(spec, []string{"name~ali"})
	require.Error(t, err, "Indexed alone must not grant LIKE permission")
}

// TestFieldSpec_PlainFilterAlwaysAllowed 无索引、无 Likeable 的普通字段
// 仍支持等值/IN 过滤，只是不支持 LIKE。
func TestFieldSpec_PlainFilterAlwaysAllowed(t *testing.T) {
	spec := crudx.ListSpec{
		Fields: map[string]crudx.FieldSpec{
			"status": {Column: "articles.status", Kind: crudx.FieldString},
		},
	}
	_, err := crudx.ParseQConditions(spec, []string{"status=draft"})
	assert.NoError(t, err)

	_, err = crudx.ParseQConditions(spec, []string{"status~dra"})
	assert.Error(t, err, "plain field must not allow LIKE")
}
