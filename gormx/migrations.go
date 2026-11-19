package gormx

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// 框架自有表的 DDL。执行由你们的 SQL 上线平台负责；本包只负责
//   - 按方言输出 DDL（`myapp migrate print`）
//   - 启动时校验 schema 契约（`db.check_schema: true`）
//
// 不使用 AutoMigrate 建表（`db.auto_migrate` 仅用于本地开发）。
//
//go:embed migrations/*/*.sql
var migrationsFS embed.FS

// MigrationDialects 返回内置 DDL 支持的方言。
func MigrationDialects() []string { return []string{"postgres", "mysql", "sqlite"} }

// normalizeDialect 把方言别名归一化为 postgres / mysql / sqlite，不支持时返回错误。
func normalizeDialect(d string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "postgres", "postgresql", "pg":
		return "postgres", nil
	case "mysql", "mariadb":
		return "mysql", nil
	case "sqlite", "sqlite3":
		return "sqlite", nil
	default:
		return "", fmt.Errorf("gormx: unsupported dialect %q (want postgres|mysql|sqlite)", d)
	}
}

// PrintMigrations 返回指定方言的 DDL（按版本排序后拼接）。
//
// version 为空表示输出全部；否则按文件名前缀过滤（例如 "0001"）。
// 只包含 up 脚本，不包含 *.rollback.sql。
func PrintMigrations(dialect, version string) (string, error) {
	d, err := normalizeDialect(dialect)
	if err != nil {
		return "", err
	}
	dir := "migrations/" + d
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return "", fmt.Errorf("gormx: read %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".rollback.sql") {
			continue
		}
		if version != "" && !strings.HasPrefix(n, version) {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("gormx: no migrations for dialect=%s version=%q", d, version)
	}

	var b strings.Builder
	for _, n := range names {
		raw, readErr := migrationsFS.ReadFile(dir + "/" + n)
		if readErr != nil {
			return "", fmt.Errorf("gormx: read %s/%s: %w", dir, n, readErr)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("-- " + n + "\n")
		b.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// SchemaCheckOptions 调整 schema 契约校验。
type SchemaCheckOptions struct {
	// CasbinTable 默认 "casbin_rule"；与 auth.casbin.table_name 保持一致。
	CasbinTable string
	// Tables 限定只校验这些表；为空表示校验所有已登记的表。
	Tables []string
}

// schemaContractRegistry 是「框架表 → 必需列」的登记表。
//
// 由各子系统在装配时调用 [RegisterSchemaContract] 登记（例如 outbox、authz），
// gormx 不再反向硬编码它们的表结构 —— 否则只用 gormx 的服务开启
// `db.check_schema: true` 时，会因为「缺 outbox_events 表」而启动失败。
var (
	// schemaContractMu 保护 schemaContractRegistry 的读写。
	schemaContractMu sync.RWMutex
	// schemaContractRegistry 是「表名 → 必需列」的登记表。
	schemaContractRegistry = map[string][]string{}
)

// RegisterSchemaContract 登记一张框架表的必需列（重复登记以最后一次为准）。
//
// 平台执行 DDL 后，[CheckSchema] 在启动时 fail fast，避免发布顺序错误
// （代码先上、DDL 未执行）时在请求路径上才报 unknown column。
func RegisterSchemaContract(table string, columns ...string) {
	table = strings.TrimSpace(table)
	if table == "" || len(columns) == 0 {
		return
	}
	schemaContractMu.Lock()
	defer schemaContractMu.Unlock()
	schemaContractRegistry[table] = append([]string(nil), columns...)
}

// snapshotSchemaContracts 返回已登记契约的深拷贝，供校验时无锁读取。
func snapshotSchemaContracts() map[string][]string {
	schemaContractMu.RLock()
	defer schemaContractMu.RUnlock()
	out := make(map[string][]string, len(schemaContractRegistry))
	for t, cols := range schemaContractRegistry {
		out[t] = append([]string(nil), cols...)
	}
	return out
}

// CheckSchema 校验已登记的框架表与列是否存在，返回聚合错误（nil 表示满足契约）。
//
// 没有任何子系统登记契约时直接返回 nil（gormx 单独使用时的正常情况）。
func CheckSchema(ctx context.Context, db *gorm.DB, opts ...SchemaCheckOptions) error {
	if db == nil {
		return fmt.Errorf("gormx: CheckSchema: nil db")
	}
	o := SchemaCheckOptions{CasbinTable: "casbin_rule"}
	if len(opts) > 0 {
		if t := strings.TrimSpace(opts[0].CasbinTable); t != "" {
			o.CasbinTable = t
		}
		o.Tables = opts[0].Tables
	}

	contracts := snapshotSchemaContracts()
	if len(o.Tables) > 0 {
		want := make(map[string]struct{}, len(o.Tables))
		for _, t := range o.Tables {
			want[strings.TrimSpace(t)] = struct{}{}
		}
		for t := range contracts {
			if _, ok := want[t]; !ok {
				delete(contracts, t)
			}
		}
	}
	if len(contracts) == 0 {
		return nil
	}

	m := db.WithContext(ctx).Migrator()
	tables := make([]string, 0, len(contracts))
	for t := range contracts {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	var errs []error
	for _, t := range tables {
		name := t
		if t == "casbin_rule" {
			name = o.CasbinTable
		}
		if !m.HasTable(name) {
			errs = append(errs, fmt.Errorf("missing table %s", name))
			continue
		}
		for _, col := range contracts[t] {
			if !m.HasColumn(name, col) {
				errs = append(errs, fmt.Errorf("missing column %s.%s", name, col))
			}
		}
	}
	return errors.Join(errs...)
}
