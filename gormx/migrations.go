package gormx

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

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
}

// schemaContract 是框架代码依赖的最小 schema（表 → 必需列）。
//
// 平台执行 DDL 后，用 CheckSchema 在启动时 fail fast，避免发布顺序错误
// （代码先上、DDL 未执行）时在请求路径上才报 unknown column。
var schemaContract = map[string][]string{
	"outbox_events": {
		"id", "pubsub", "topic", "payload", "idempotency_key", "status",
		"attempts", "last_error", "lease_id", "locked_at", "created_at", "published_at",
	},
	"outbox_inbox_events": {"id", "topic", "processed_at"},
	"casbin_rule":         {"id", "ptype", "v0", "v1", "v2", "v3", "v4", "v5"},
	"authz_api_permission": {
		"id", "method", "path", "pattern", "operation_id", "summary",
		"description", "tags", "updated_at",
	},
	"authz_role": {"name", "display_name", "description", "created_at", "updated_at"},
}

// CheckSchema 校验框架表与列是否存在，返回聚合错误（nil 表示满足契约）。
func CheckSchema(ctx context.Context, db *gorm.DB, opts ...SchemaCheckOptions) error {
	if db == nil {
		return fmt.Errorf("gormx: CheckSchema: nil db")
	}
	o := SchemaCheckOptions{CasbinTable: "casbin_rule"}
	if len(opts) > 0 && strings.TrimSpace(opts[0].CasbinTable) != "" {
		o.CasbinTable = strings.TrimSpace(opts[0].CasbinTable)
	}

	m := db.WithContext(ctx).Migrator()
	tables := make([]string, 0, len(schemaContract))
	for t := range schemaContract {
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
		for _, col := range schemaContract[t] {
			if !m.HasColumn(name, col) {
				errs = append(errs, fmt.Errorf("missing column %s.%s", name, col))
			}
		}
	}
	return errors.Join(errs...)
}
