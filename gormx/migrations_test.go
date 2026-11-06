package gormx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPrintMigrations(t *testing.T) {
	for _, d := range MigrationDialects() {
		out, err := PrintMigrations(d, "")
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		if !strings.Contains(out, "outbox_events") || !strings.Contains(out, "authz_role") {
			t.Fatalf("%s: unexpected output:\n%s", d, out)
		}
		if strings.Contains(out, "rollback") {
			t.Fatalf("%s: rollback must not be included", d)
		}
	}

	if _, err := PrintMigrations("oracle", ""); err == nil {
		t.Fatal("unsupported dialect must fail")
	}
	if _, err := PrintMigrations("postgres", "9999"); err == nil {
		t.Fatal("unknown version must fail")
	}
}

func resetSchemaContracts() {
	schemaContractMu.Lock()
	schemaContractRegistry = map[string][]string{}
	schemaContractMu.Unlock()
}

// registerFrameworkContracts 模拟 outbox / authz 模块在装配时登记 schema 契约。
func registerFrameworkContracts() {
	RegisterSchemaContract("outbox_events",
		"id", "pubsub", "topic", "payload", "idempotency_key", "status",
		"attempts", "last_error", "lease_id", "locked_at", "created_at", "published_at",
	)
	RegisterSchemaContract("outbox_inbox_events", "id", "topic", "processed_at")
	RegisterSchemaContract("casbin_rule", "id", "ptype", "v0", "v1", "v2", "v3", "v4", "v5")
	RegisterSchemaContract("authz_role", "name", "display_name", "description", "created_at", "updated_at")
}

// TestCheckSchema_noContractsIsNoop 是回归测试：
//
// schemaContract 曾经把 outbox/authz 的表硬编码在 gormx 里，于是只用 gormx
// 的服务开启 db.check_schema 时会因为「缺 outbox_events 表」启动失败。
// 现在没有子系统登记契约时 CheckSchema 直接返回 nil。
func TestCheckSchema_noContractsIsNoop(t *testing.T) {
	resetSchemaContracts()
	t.Cleanup(resetSchemaContracts)

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSchema(context.Background(), db); err != nil {
		t.Fatalf("no registered contract must be a no-op, got %v", err)
	}
}

// execStatements 逐条执行 DDL（方言驱动对多语句支持不一致）。
func execStatements(db *gorm.DB, script string) error {
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// TestMigrationsPostgres 验证内置 PostgreSQL DDL 能真的执行，且执行后满足 schema 契约。
//
// 需要 FXKIT_TEST_POSTGRES_DSN（CI 的 dialect job 会注入）。
func TestMigrationsPostgres(t *testing.T) {
	dsn := os.Getenv("FXKIT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set FXKIT_TEST_POSTGRES_DSN to run")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	ctx := context.Background()

	sql, err := PrintMigrations("postgres", "")
	if err != nil {
		t.Fatal(err)
	}
	// 从 rollback 里取 DROP，先清干净。
	if err := db.Exec(`DROP TABLE IF EXISTS authz_role, authz_api_permission, casbin_rule, outbox_inbox_events, outbox_events`).Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	if err := execStatements(db, sql); err != nil {
		t.Fatalf("apply postgres DDL: %v", err)
	}
	if err := CheckSchema(ctx, db); err != nil {
		t.Fatalf("schema check after DDL: %v", err)
	}
}

// TestMigrationsMySQL 验证内置 MySQL DDL 能真的执行，且执行后满足 schema 契约。
//
// 需要 FXKIT_TEST_MYSQL_DSN（CI 的 dialect job 会注入）。
func TestMigrationsMySQL(t *testing.T) {
	registerFrameworkContracts()
	dsn := os.Getenv("FXKIT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FXKIT_TEST_MYSQL_DSN to run")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	ctx := context.Background()

	sql, err := PrintMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS authz_role",
		"DROP TABLE IF EXISTS authz_api_permission",
		"DROP TABLE IF EXISTS casbin_rule",
		"DROP TABLE IF EXISTS outbox_inbox_events",
		"DROP TABLE IF EXISTS outbox_events",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("drop: %v", err)
		}
	}

	if err := execStatements(db, sql); err != nil {
		t.Fatalf("apply mysql DDL: %v", err)
	}
	if err := CheckSchema(ctx, db); err != nil {
		t.Fatalf("schema check after DDL: %v", err)
	}
}

func TestCheckSchemaSQLite(t *testing.T) {
	registerFrameworkContracts()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schema.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 空库：必须报缺失。
	if err := CheckSchema(ctx, db); err == nil {
		t.Fatal("empty database must fail schema check")
	}

	sql, err := PrintMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(sql).Error; err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if err := CheckSchema(ctx, db); err != nil {
		t.Fatalf("schema check should pass after DDL: %v", err)
	}

	// 自定义 casbin 表名：默认检查应报缺失，指定后应通过（这里只验证错误提示）。
	if err := CheckSchema(ctx, db, SchemaCheckOptions{CasbinTable: "custom_casbin"}); err == nil {
		t.Fatal("custom casbin table should be reported missing")
	}
}
