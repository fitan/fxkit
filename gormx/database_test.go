package gormx

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSlogAdapterRespectsSilent(t *testing.T) {
	a := newSlogAdapter("silent", 0).(*slogAdapter)
	if a.level <= slog.LevelError {
		t.Fatalf("silent level should be above Error, got %v", a.level)
	}
	// 在 silent 级别下 Trace 不得 panic。
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, nil)
}

func TestSlogAdapterLogMode(t *testing.T) {
	a := newSlogAdapter("info", 0).(*slogAdapter)
	next := a.LogMode(logger.Silent).(*slogAdapter)
	if next.level <= slog.LevelError {
		t.Fatalf("LogMode(Silent) level=%v", next.level)
	}
	if a.level != slog.LevelInfo {
		t.Fatalf("original mutated: %v", a.level)
	}
}

func TestParseLogLevelDefaults(t *testing.T) {
	a := newSlogAdapter("", 0).(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("empty default=%v", a.level)
	}
	a = newSlogAdapter("warn", 0).(*slogAdapter)
	if a.level != slog.LevelWarn {
		t.Fatalf("warn=%v", a.level)
	}
	a = newSlogAdapter("info", 0).(*slogAdapter)
	if a.level != slog.LevelInfo {
		t.Fatalf("info=%v", a.level)
	}
}

func TestPrepareDSN_sqlitePragmas(t *testing.T) {
	got := prepareDSN("sqlite", "file:./app.db?cache=shared")
	if !strings.Contains(got, "_busy_timeout=5000") {
		t.Fatalf("missing busy_timeout: %s", got)
	}
	if !strings.Contains(got, "_journal_mode=WAL") {
		t.Fatalf("missing journal_mode: %s", got)
	}
	if !strings.Contains(got, "cache=shared") {
		t.Fatalf("dropped cache: %s", got)
	}
	custom := prepareDSN("sqlite", "file:app.db?_busy_timeout=1000")
	if !strings.Contains(custom, "_busy_timeout=1000") || strings.Contains(custom, "_busy_timeout=5000") {
		t.Fatalf("should keep user busy_timeout: %s", custom)
	}
	pg := prepareDSN("postgres", "postgres://x/y")
	if pg != "postgres://x/y" {
		t.Fatalf("postgres dsn mutated: %s", pg)
	}
}

func TestSlogAdapterIgnoresRecordNotFound(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := newSlogAdapter("info", 0).(*slogAdapter)
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT * FROM t WHERE id=1", 0
	}, gorm.ErrRecordNotFound)
	if strings.Contains(buf.String(), "database error") {
		t.Fatalf("record not found logged as error: %s", buf.String())
	}

	buf.Reset()
	a.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 0
	}, errors.New("connection reset"))
	if !strings.Contains(buf.String(), "database error") {
		t.Fatalf("real error not logged: %s", buf.String())
	}
}

func TestSlogAdapterLogsSlowQuery(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// 级别为 Warn，阈值为 50ms
	a := newSlogAdapter("warn", 50*time.Millisecond).(*slogAdapter)

	// 模拟快查询（1ms 前）
	a.Trace(context.Background(), time.Now().Add(-1*time.Millisecond), func() (string, int64) {
		return "SELECT fast", 1
	}, nil)
	if strings.Contains(buf.String(), "database slow query") {
		t.Fatalf("fast query logged as slow: %s", buf.String())
	}

	// 模拟慢查询（100ms 前）
	a.Trace(context.Background(), time.Now().Add(-100*time.Millisecond), func() (string, int64) {
		return "SELECT slow", 1
	}, nil)
	if !strings.Contains(buf.String(), "database slow query") {
		t.Fatalf("slow query not logged: %s", buf.String())
	}
}

// TestNew_appliesPoolDefaults 直接构造的 Config 也必须得到安全的连接池默认值。
func TestNew_appliesPoolDefaults(t *testing.T) {
	c, err := New(&Config{Driver: "SQLITE", DSN: ":memory:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("nil client")
	}
	sqlDB, err := c.Pool().DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if got := sqlDB.Stats().MaxOpenConnections; got != 100 {
		t.Fatalf("MaxOpenConnections = %d, want 100", got)
	}
}

// TestNew_rejectsPartialConfig driver/dsn 必须成对出现。
func TestNew_rejectsPartialConfig(t *testing.T) {
	if _, err := New(&Config{Driver: "sqlite"}); err == nil {
		t.Fatal("want error when dsn is empty")
	}
}

// TestModule_AssemblyDoesNotConnectDB 是回归测试：
//
// gormx.Module 曾在装配期直接 di.Invoke[*Client]，于是 di.New 就会
// sql.Open + PingContext(5s)。后果是 `app validate`（文档承诺
// 「不碰任何外部依赖」）、`app openapi`、`app mcp` 都会去连库。
//
// 现在健康检查项在 PreStart 才注册，装配期不解析 *Client。
func TestModule_AssemblyDoesNotConnectDB(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "db:\n  driver: sqlite\n  dsn: " + filepath.Join(dir, "test.db") + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := di.New(
		config.Module,
		func(i di.Injector) { di.ProvideValue(i, config.Options{ConfigFile: cfgPath}) },
		Module,
	)

	// 真正的断言：装配完成后 *Client 从未被构造。
	if name := findInvokedService(root, "gormx.Client"); name != "" {
		t.Fatalf("assembly constructed *Client (%s); validate/openapi/mcp would connect to the DB", name)
	}

	// 真正需要 *Client 时才连接。
	client, err := di.Invoke[*Client](root)
	if err != nil {
		t.Fatalf("resolve *Client: %v", err)
	}
	if client == nil || client.Pool() == nil {
		t.Fatal("expected a live client after explicit resolve")
	}
	if sqlDB, dbErr := client.Pool().DB(); dbErr == nil {
		defer func() { _ = sqlDB.Close() }()
	}

	// 自校验：显式解析后名字必须能被匹配到，否则上面的断言可能只是空转。
	if findInvokedService(root, "gormx.Client") == "" {
		t.Fatal("sanity check failed: resolved *Client not visible in ListInvokedServices()")
	}
	_ = di.Shutdown(root)
}

// findInvokedService 返回已构造服务中包含 substr 的服务名。
func findInvokedService(root di.Injector, substr string) string {
	for _, s := range root.ListInvokedServices() {
		if strings.Contains(s.Service, substr) {
			return s.Service
		}
	}
	return ""
}

// TestEnsureSQLitePragmas_KeepsUserQueryOnParseError 是回归测试：
// query 解析失败时早期会把 q 置为空 Values，于是用户显式写的
// _journal_mode/_busy_timeout 等参数被整段丢掉、再被默认值覆盖。
func TestEnsureSQLitePragmas_KeepsUserQueryOnParseError(t *testing.T) {
	dsn := "file:test.db?_busy_timeout=100&%zz=bad"
	if got := ensureSQLitePragmas(dsn); got != dsn {
		t.Fatalf("malformed DSN must be returned unchanged, got %q", got)
	}
}

// TestEnsureSQLitePragmas_AppliesDefaults 正常路径仍然注入默认值。
func TestEnsureSQLitePragmas_AppliesDefaults(t *testing.T) {
	got := ensureSQLitePragmas("file:test.db?_busy_timeout=100")
	if !strings.Contains(got, "_busy_timeout=100") {
		t.Fatalf("user value must be kept, got %q", got)
	}
	if !strings.Contains(got, "_journal_mode=WAL") {
		t.Fatalf("defaults must be applied, got %q", got)
	}
}
