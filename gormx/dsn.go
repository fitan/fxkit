package gormx

import (
	"net/url"
	"strings"
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
	slashIdx := strings.Index(dsn, "/")
	if slashIdx == -1 {
		return ""
	}
	rest := dsn[slashIdx+1:]
	if idx := strings.Index(rest, "?"); idx != -1 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "#"); idx != -1 {
		rest = rest[:idx]
	}
	return rest
}
