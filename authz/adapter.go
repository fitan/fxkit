package authz

import (
	"fmt"
	"strings"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"gorm.io/gorm"
)

// casbinRule 是 Casbin 策略的 Postgres 行（兼容 casbin_rule 表结构）。
// 唯一键为 ptype+v0+v1+v2（本模型只存 sub/obj/act）。v1 取 255 以对齐
// authz_api_permission.path；v3–v5 不进入唯一索引，使 MySQL utf8mb4
// 保持在 3072 字节限制内。
type casbinRule struct {
	// ID 自增主键。
	ID uint `gorm:"primaryKey;autoIncrement"`
	// Ptype 策略类型列（ptype），取值为 p 或 g。
	Ptype string `gorm:"column:ptype;size:100;uniqueIndex:uk_casbin_rule"`
	// V0 策略第 1 列（v0）：p 规则为 sub（角色），g 规则为用户。
	V0 string `gorm:"column:v0;size:100;uniqueIndex:uk_casbin_rule"`
	// V1 策略第 2 列（v1）：p 规则为 dom（租户 domain），g 规则为角色。
	V1 string `gorm:"column:v1;size:255;uniqueIndex:uk_casbin_rule"`
	// V2 策略第 3 列（v2）：p 规则为 obj（路径模式），g 规则为 dom。
	V2 string `gorm:"column:v2;size:100;uniqueIndex:uk_casbin_rule"`
	// V3 策略第 4 列（v3）：p 规则为 act（HTTP 方法）；g 规则不使用。
	V3 string `gorm:"column:v3;size:100;uniqueIndex:uk_casbin_rule"`
	// V4 策略第 5 列（v4）；当前模型不使用，恒为空串。
	V4 string `gorm:"column:v4;size:100"`
	// V5 策略第 6 列（v5）；当前模型不使用，恒为空串。
	V5 string `gorm:"column:v5;size:100"`
}

// gormAdapter 通过 GORM 持久化 Casbin 策略（避免 gorm-adapter / dbresolver 冲突）。
type gormAdapter struct {
	// db 是策略表所在的 GORM 数据库连接。
	db *gorm.DB
	// tableName 是策略表名（默认 casbin_rule）。
	tableName string
}

// newGormAdapter 创建 Casbin 适配器。autoMigrate=false 时不建表（生产由 SQL 平台负责）。
func newGormAdapter(db *gorm.DB, tableName string, autoMigrate bool) (*gormAdapter, error) {
	if db == nil {
		return nil, fmt.Errorf("auth casbin: nil db")
	}
	if strings.TrimSpace(tableName) == "" {
		return nil, fmt.Errorf("auth casbin: table name is required")
	}
	a := &gormAdapter{db: db, tableName: tableName}
	if autoMigrate {
		if err := a.table().AutoMigrate(&casbinRule{}); err != nil {
			return nil, fmt.Errorf("auth casbin migrate: %w", err)
		}
	}
	return a, nil
}

// table 返回指向策略表的 GORM 会话。
func (a *gormAdapter) table() *gorm.DB {
	return a.db.Table(a.tableName)
}

// LoadPolicy 读取策略表全部行并逐行加载进 Casbin 模型。
func (a *gormAdapter) LoadPolicy(m model.Model) error {
	var rows []casbinRule
	if err := a.table().Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := persist.LoadPolicyLine(policyLine(row), m); err != nil {
			return err
		}
	}
	return nil
}

// SavePolicy 在一个事务内清空策略表，再写入模型中的全部 p/g 规则。
func (a *gormAdapter) SavePolicy(m model.Model) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		t := tx.Table(a.tableName)
		if err := t.Where("1 = 1").Delete(&casbinRule{}).Error; err != nil {
			return err
		}
		var rows []casbinRule
		for ptype, ast := range m["p"] {
			for _, rule := range ast.Policy {
				rows = append(rows, ruleToRow(ptype, rule))
			}
		}
		for ptype, ast := range m["g"] {
			for _, rule := range ast.Policy {
				rows = append(rows, ruleToRow(ptype, rule))
			}
		}
		if len(rows) == 0 {
			return nil
		}
		return t.CreateInBatches(rows, 100).Error
	})
}

// AddPolicy 幂等插入一条规则：已存在相同行时不做任何事。
func (a *gormAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	row := ruleToRow(ptype, rule)
	return a.table().Where(casbinRule{
		Ptype: row.Ptype, V0: row.V0, V1: row.V1, V2: row.V2, V3: row.V3, V4: row.V4, V5: row.V5,
	}).FirstOrCreate(&row).Error
}

// RemovePolicy 删除与给定规则完全匹配的行。
func (a *gormAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	row := ruleToRow(ptype, rule)
	return a.table().Where(casbinRule{
		Ptype: row.Ptype, V0: row.V0, V1: row.V1, V2: row.V2, V3: row.V3, V4: row.V4, V5: row.V5,
	}).Delete(&casbinRule{}).Error
}

// RemoveFilteredPolicy 删除 ptype 下按列值匹配的行；空值列不参与过滤。
// fieldIndex 指定 fieldValues[0] 对应的列（0=v0），越界的列被忽略。
func (a *gormAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	q := a.table().Where("ptype = ?", ptype)
	cols := []string{"v0", "v1", "v2", "v3", "v4", "v5"}
	for i, v := range fieldValues {
		colIdx := fieldIndex + i
		if colIdx < 0 || colIdx >= len(cols) || v == "" {
			continue
		}
		q = q.Where(cols[colIdx]+" = ?", v)
	}
	return q.Delete(&casbinRule{}).Error
}

// replacePoliciesForSubDomain 原子替换某个角色在某个 domain 的 p 规则。
func (a *gormAdapter) replacePoliciesForSubDomain(sub, dom string, rules [][]string) error {
	return a.replaceRulesWhere("p", "v0 = ? AND v1 = ?", []any{sub, dom}, rules)
}

// replaceGroupingsForUserDomain 原子替换某个用户在某 domain 的 g 规则。
func (a *gormAdapter) replaceGroupingsForUserDomain(user, dom string, rules [][]string) error {
	return a.replaceRulesWhere("g", "v0 = ? AND v2 = ?", []any{user, dom}, rules)
}

// replaceRulesWhere 在一个事务内删除 ptype 且满足 where 的行，再批量写入 rules。
func (a *gormAdapter) replaceRulesWhere(ptype, where string, args []any, rules [][]string) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		t := tx.Table(a.tableName).Where("ptype = ?", ptype).Where(where, args...)
		if err := t.Delete(&casbinRule{}).Error; err != nil {
			return err
		}
		if len(rules) == 0 {
			return nil
		}
		rows := make([]casbinRule, 0, len(rules))
		for _, rule := range rules {
			rows = append(rows, ruleToRow(ptype, rule))
		}
		return tx.Table(a.tableName).CreateInBatches(rows, 100).Error
	})
}

// replacePoliciesForSub 原子替换所有 v0 为 sub 的 p 规则。
func (a *gormAdapter) replacePoliciesForSub(sub string, rules [][]string) error {
	return a.replaceRules("p", sub, rules)
}

// replaceGroupingsForUser 原子替换所有 v0 为 user 的 g 规则。
func (a *gormAdapter) replaceGroupingsForUser(user string, rules [][]string) error {
	return a.replaceRules("g", user, rules)
}

// replaceRules 在一个事务内替换 ptype 下 v0 等于给定主体的全部规则。
func (a *gormAdapter) replaceRules(ptype, v0 string, rules [][]string) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		t := tx.Table(a.tableName)
		if err := t.Where("ptype = ? AND v0 = ?", ptype, v0).Delete(&casbinRule{}).Error; err != nil {
			return err
		}
		if len(rules) == 0 {
			return nil
		}
		rows := make([]casbinRule, 0, len(rules))
		for _, rule := range rules {
			rows = append(rows, ruleToRow(ptype, rule))
		}
		return t.CreateInBatches(rows, 100).Error
	})
}

// ruleToRow 把 Casbin 规则转换为策略行，字段不足 6 个时补空串。
func ruleToRow(ptype string, rule []string) casbinRule {
	fields := padFields(rule, 6)
	return casbinRule{
		Ptype: ptype,
		V0:    fields[0],
		V1:    fields[1],
		V2:    fields[2],
		V3:    fields[3],
		V4:    fields[4],
		V5:    fields[5],
	}
}

// policyLine 把策略行拼成 Casbin 策略文本，并去掉末尾的空字段。
func policyLine(row casbinRule) string {
	parts := []string{row.Ptype, row.V0, row.V1, row.V2, row.V3, row.V4, row.V5}
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ", ")
}

// padFields 把 in 拷贝成长度为 n 的切片，不足部分留零值。
func padFields(in []string, n int) []string {
	out := make([]string, n)
	for i := 0; i < n && i < len(in); i++ {
		out[i] = in[i]
	}
	return out
}

var _ persist.Adapter = (*gormAdapter)(nil)
