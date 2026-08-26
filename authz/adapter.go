package authz

import (
	"fmt"
	"strings"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"gorm.io/gorm"
)

// casbinRule is the Postgres row for Casbin policies (compatible with casbin_rule schema).
// Unique key is ptype+v0+v1+v2 (this model only stores sub/obj/act). v1 is 255 to match
// authz_api_permission.path; v3–v5 are omitted from the unique index so MySQL utf8mb4
// stays under the 3072-byte limit.
type casbinRule struct {
	ID    uint   `gorm:"primaryKey;autoIncrement"`
	Ptype string `gorm:"column:ptype;size:100;uniqueIndex:uk_casbin_rule"`
	V0    string `gorm:"column:v0;size:100;uniqueIndex:uk_casbin_rule"`
	V1    string `gorm:"column:v1;size:255;uniqueIndex:uk_casbin_rule"`
	V2    string `gorm:"column:v2;size:100;uniqueIndex:uk_casbin_rule"`
	V3    string `gorm:"column:v3;size:100"`
	V4    string `gorm:"column:v4;size:100"`
	V5    string `gorm:"column:v5;size:100"`
}

// gormAdapter persists Casbin policies via GORM (avoids gorm-adapter / dbresolver clashes).
type gormAdapter struct {
	db        *gorm.DB
	tableName string
}

func newGormAdapter(db *gorm.DB, tableName string) (*gormAdapter, error) {
	if db == nil {
		return nil, fmt.Errorf("auth casbin: nil db")
	}
	if strings.TrimSpace(tableName) == "" {
		return nil, fmt.Errorf("auth casbin: table name is required")
	}
	a := &gormAdapter{db: db, tableName: tableName}
	if err := a.table().AutoMigrate(&casbinRule{}); err != nil {
		return nil, fmt.Errorf("auth casbin migrate: %w", err)
	}
	return a, nil
}

func (a *gormAdapter) table() *gorm.DB {
	return a.db.Table(a.tableName)
}

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

func (a *gormAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	row := ruleToRow(ptype, rule)
	return a.table().Where(casbinRule{
		Ptype: row.Ptype, V0: row.V0, V1: row.V1, V2: row.V2, V3: row.V3, V4: row.V4, V5: row.V5,
	}).FirstOrCreate(&row).Error
}

func (a *gormAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	row := ruleToRow(ptype, rule)
	return a.table().Where(casbinRule{
		Ptype: row.Ptype, V0: row.V0, V1: row.V1, V2: row.V2, V3: row.V3, V4: row.V4, V5: row.V5,
	}).Delete(&casbinRule{}).Error
}

func (a *gormAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	q := a.table().Where("ptype = ?", ptype)
	values := padFields(fieldValues, 6)
	cols := []string{"v0", "v1", "v2", "v3", "v4", "v5"}
	for i, v := range values {
		if i < fieldIndex || v == "" {
			continue
		}
		q = q.Where(cols[i]+" = ?", v)
	}
	return q.Delete(&casbinRule{}).Error
}

// replacePoliciesForSub atomically replaces all p rules whose v0 is sub.
func (a *gormAdapter) replacePoliciesForSub(sub string, rules [][]string) error {
	return a.replaceRules("p", sub, rules)
}

// replaceGroupingsForUser atomically replaces all g rules whose v0 is user.
func (a *gormAdapter) replaceGroupingsForUser(user string, rules [][]string) error {
	return a.replaceRules("g", user, rules)
}

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

func policyLine(row casbinRule) string {
	parts := []string{row.Ptype, row.V0, row.V1, row.V2, row.V3, row.V4, row.V5}
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ", ")
}

func padFields(in []string, n int) []string {
	out := make([]string, n)
	for i := 0; i < n && i < len(in); i++ {
		out[i] = in[i]
	}
	return out
}

var _ persist.Adapter = (*gormAdapter)(nil)
