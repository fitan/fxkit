package crudx

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/fitan/fxkit/fxerrors"
)

// TestList_CursorWithSelectIncludesSortColumn 回归：
// Select 只投影部分列时，游标必须自动补上主键与排序列，否则第二页会重复第一页。
func TestList_CursorWithSelectIncludesSortColumn(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		m := listTestModel{
			Name:      fmt.Sprintf("n%d", i),
			Email:     fmt.Sprintf("u%d@example.com", i),
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}

	spec := listTestSpec()
	spec.SortFields["created_at"] = "list_test_models.created_at"

	params := ListParams{Limit: 2, UseCursor: true, SortBy: "created_at", SortDirection: "asc"}
	seen := map[int64]bool{}
	for page := 0; page < 10; page++ {
		out, err := List(ctx, ListInput[listTestModel, listTestRow]{
			DB:     listTestBaseDB(db, ctx),
			Spec:   spec,
			Params: params,
			Select: []string{"name"},
			ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} },
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, it := range out.Items {
			if seen[it.ID] {
				t.Fatalf("page %d returned duplicate id %d", page, it.ID)
			}
			seen[it.ID] = true
		}
		if out.NextCursor == nil {
			break
		}
		params.Cursor = *out.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("cursor pagination saw %d rows, want 5", len(seen))
	}
}

// TestApplyList_CursorWithoutSortRejected 回归：
// 游标模式必须有确定排序，否则首页无 ORDER BY、次页必 400。
func TestApplyList_CursorWithoutSortRejected(t *testing.T) {
	db := newListTestDB(t)
	ctx := context.Background()
	spec := listTestSpec()
	spec.DefaultSort = ""

	_, err := List(ctx, ListInput[listTestModel, listTestRow]{
		DB:     listTestBaseDB(db, ctx),
		Spec:   spec,
		Params: ListParams{Limit: 2, UseCursor: true},
		ToRow:  func(m listTestModel) listTestRow { return listTestRow{ID: m.ID, Name: m.Name} },
	})
	if err == nil {
		t.Fatal("want error when cursor pagination has no sort")
	}
	var fe *fxerrors.Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *fxerrors.Error, got %T", err)
	}
	if got := fe.Details["reason"]; got != ReasonInvalidSortField {
		t.Fatalf("reason=%v, want %s", got, ReasonInvalidSortField)
	}
}

// TestDecodeCursor_RejectsNonScalarValue 回归：客户端构造的对象/数组游标必须 400，而不是打到 SQL 变 500。
func TestDecodeCursor_RejectsNonScalarValue(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"v":{"a":1},"pk":"1"}`))
	if _, _, _, _, err := DecodeCursor(raw); err == nil {
		t.Fatal("want error for non-scalar cursor value")
	}
}

// TestCoerceCursorPK_RejectsInvalidInteger 回归：int 主键解析失败必须报错，不能回退成字符串。
func TestCoerceCursorPK_RejectsInvalidInteger(t *testing.T) {
	if _, err := coerceCursorPK("abc", reflect.Int64); err == nil {
		t.Fatal("want error for non-numeric primary key")
	}
}

// TestCursor_StringValueThatLooksLikeTimeStaysString 回归：
// varchar 列里形似时间的值不能被游标解码成 time.Time（会导致方言类型错误）。
func TestCursor_StringValueThatLooksLikeTimeStaysString(t *testing.T) {
	tok, err := EncodeCursor("code", "asc", "2024-01-01T00:00:00Z", "1")
	if err != nil {
		t.Fatal(err)
	}
	_, _, v, _, err := DecodeCursor(tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(string); !ok {
		t.Fatalf("sort value type=%T, want string", v)
	}
}
