package gormx

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// extractDBName 从 DSN 提取数据库名用于遥测标签。
// 无法识别格式时返回 ""，调用方可回退到驱动级默认值。
func extractDBName(driver, dsn string) string {
	switch driver {
	case "postgres":
		return extractPostgresDBName(dsn)
	case "mysql":
		return extractMySQLDBName(dsn)
	case "sqlite":
		// sqlite DSN 通常为文件路径或 :memory:
		return strings.TrimSpace(dsn)
	default:
		return ""
	}
}

func extractPostgresDBName(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			if u.Path != "" && u.Path != "/" {
				return strings.TrimPrefix(u.Path, "/")
			}
		}
	}
	for _, part := range strings.Fields(dsn) {
		if strings.HasPrefix(part, "dbname=") {
			return strings.TrimPrefix(part, "dbname=")
		}
	}
	return ""
}

func extractMySQLDBName(dsn string) string {
	// 交给驱动解析：手写按第一个 '/' 切分会把
	// user:pass@unix(/tmp/mysql.sock)/dbname 解析成 tmp/mysql.sock)/dbname。
	if cfg, err := mysql.ParseDSN(dsn); err == nil {
		return cfg.DBName
	}
	return ""
}

// prepareDSN 应用驱动相关的 DSN 默认值（SQLite busy timeout / WAL）。
func prepareDSN(driver, dsn string) string {
	if !strings.EqualFold(strings.TrimSpace(driver), "sqlite") {
		return dsn
	}
	return ensureSQLitePragmas(dsn)
}

func ensureSQLitePragmas(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return dsn
	}
	base, rawQuery := dsn, ""
	if i := strings.Index(dsn, "?"); i >= 0 {
		base, rawQuery = dsn[:i], dsn[i+1:]
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		// 不能回退成空 Values：那会把用户显式写的 _journal_mode/_busy_timeout
		// 等参数整段丢掉，再补上我们的默认值，行为静默改变。
		slog.Warn("gormx: sqlite dsn query is malformed; leaving DSN unchanged",
			"query", rawQuery, "error", err)
		return dsn
	}
	defaults := map[string]string{
		"_busy_timeout": "5000",
		"_journal_mode": "WAL",
		"_fk":           "1",
	}
	for k, v := range defaults {
		if q.Get(k) == "" {
			q.Set(k, v)
		}
	}
	enc := q.Encode()
	if enc == "" {
		return base
	}
	return base + "?" + enc
}
