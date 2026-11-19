package crudx

import (
	"regexp"
	"strings"

	"github.com/fitan/fxkit/fxerrors"
)

// identifierRE 只允许普通 SQL 标识符（可带 schema/table 前缀）。
// 不允许空格、引号、括号、运算符——这些只应出现在开发者手写的 SQL 里，
// 不应来自 ListSpec 或 ListInput.Select。
var identifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// validSQLIdentifier 报告 s 是否为合法 SQL 标识符（允许 *、table.* 与 schema.table.col）。
func validSQLIdentifier(s string) bool {
	s = strings.TrimSpace(s)
	if s == "*" {
		return true
	}
	// 允许 table.* / schema.table.*
	if strings.HasSuffix(s, ".*") {
		s = strings.TrimSuffix(s, ".*")
	}
	return identifierRE.MatchString(s)
}

// validateIdentifiers 校验 ListSpec 中所有会直接拼接进 SQL 的列名。
//
// ListSpec 通常由开发者编写，但如果它来自管理端/租户配置，未校验的
// Column/SortFields 就是 SQL 注入面。这里在查询前统一拒绝非法标识符。
func (s ListSpec) validateIdentifiers() error {
	for name, f := range s.Fields {
		if strings.TrimSpace(f.Column) != "" && !validSQLIdentifier(f.Column) {
			return fxerrors.Internal("crudx: field %q has invalid column %q", name, f.Column)
		}
	}
	for name, col := range s.SortFields {
		if !validSQLIdentifier(col) {
			return fxerrors.Internal("crudx: sort field %q has invalid column %q", name, col)
		}
	}
	for name, rel := range s.Relations {
		if rel == nil {
			continue
		}
		if err := validateRelationIdentifiers(name, rel); err != nil {
			return err
		}
	}
	return nil
}

// validateRelationIdentifiers 递归校验关联上的列名；prefix 用于错误消息中的路径前缀。
func validateRelationIdentifiers(prefix string, rel *RelationSpec) error {
	for name, f := range rel.Fields {
		if strings.TrimSpace(f.Column) != "" && !validSQLIdentifier(f.Column) {
			return fxerrors.Internal("crudx: relation %q field %q has invalid column %q", prefix, name, f.Column)
		}
	}
	for name, sub := range rel.Nested {
		if sub == nil {
			continue
		}
		if err := validateRelationIdentifiers(prefix+"."+name, sub); err != nil {
			return err
		}
	}
	return nil
}

// validateSelectColumns 校验 ListInput.Select。
func validateSelectColumns(cols []string) error {
	for _, col := range cols {
		if !validSQLIdentifier(col) {
			return fxerrors.Internal("crudx: invalid select column %q", col)
		}
	}
	return nil
}
