// Package gormx 装配 [gorm.io/gorm] 连接，使用 otelsql 插桩驱动与 slog 驱动的 GORM logger。
// 驱动由 `db.driver` 配置选择（mysql | postgres | sqlite）。`db.driver` 为空时模块短路返回 nil，
// 宿主应用可保留 Module 而不接入数据库。
//
// 从 Fx 注入 [*Client]；每个请求使用 [Client.Conn]，事务使用 [Client.Transaction]。
package gormx

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/fitan/fxkit/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/fx"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

// New 使用配置的 driver/DSN 打开 [Client]，启用 otelsql trace 与 metrics。
// db.driver 未设置时返回 (nil, nil)，无 DB 服务仍可保留 gormx.Module。
func New(c *Config) (*Client, error) {
	pool, err := openPool(c)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, nil
	}
	return &Client{pool: pool}, nil
}

func openPool(c *Config) (*gorm.DB, error) {
	if c == nil || c.Driver == "" || c.DSN == "" {
		slog.Info("gormx disabled: db.driver/db.dsn not set")
		return nil, nil
	}

	dbName := extractDBName(c.Driver, c.DSN)
	sqlDriver, err := sqlDriverName(c.Driver)
	if err != nil {
		return nil, err
	}

	otelOpts := []otelsql.Option{
		otelsql.WithAttributes(attribute.String("db.system", c.Driver)),
		otelsql.WithTracerProvider(otel.GetTracerProvider()),
		otelsql.WithMeterProvider(otel.GetMeterProvider()),
	}
	if dbName != "" {
		otelOpts = append(otelOpts, otelsql.WithAttributes(attribute.String("db.name", dbName)))
	}

	otelDriverName, err := otelsql.Register(sqlDriver, otelOpts...)
	if err != nil {
		slog.Warn("otelsql register failed, falling back to standard driver", "error", err)
		otelDriverName = sqlDriver
	} else {
		slog.Info("otelsql driver registered", "driver", otelDriverName)
	}

	gormCfg := &gorm.Config{
		Logger:         newSlogAdapter(c.LogLevel),
		TranslateError: true,
	}

	dsn := prepareDSN(c.Driver, c.DSN)
	sqlDB, err := sql.Open(otelDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database with otel driver: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	var dialector gorm.Dialector
	switch c.Driver {
	case "postgres":
		dialector = postgres.New(postgres.Config{Conn: sqlDB})
	case "mysql":
		dialector = mysql.New(mysql.Config{Conn: sqlDB})
	case "sqlite":
		dialector = sqlite.Dialector{Conn: sqlDB}
	default:
		_ = sqlDB.Close()
		return nil, fmt.Errorf("unsupported db driver: %s", c.Driver)
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}

	if _, err := otelsql.RegisterDBStatsMetrics(sqlDB, otelOpts...); err != nil {
		slog.Warn("otelsql db stats metrics register failed", "error", err)
	} else {
		slog.Info("otelsql db stats metrics registered", "db", dbName)
	}

	sqlDB.SetMaxIdleConns(c.MaxIdleConns)
	sqlDB.SetMaxOpenConns(c.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(c.ConnMaxLifetime)

	slog.Info("database connected",
		"driver", c.Driver,
		"database", dbName,
		"otel_driver", otelDriverName,
	)
	return db, nil
}

// sqlDriverName maps config db.driver to database/sql driver names.
func sqlDriverName(driver string) (string, error) {
	switch driver {
	case "postgres":
		return "pgx", nil
	case "mysql":
		return "mysql", nil
	case "sqlite":
		return "sqlite3", nil
	default:
		return "", fmt.Errorf("unsupported db driver: %s", driver)
	}
}

// slogAdapter 将 GORM 的 logger.Interface 转发到全局 slog handler。
type slogAdapter struct {
	level slog.Level
}

func newSlogAdapter(level string) logger.Interface {
	var l slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "silent":
		l = slog.LevelDebug + 100
	case "error":
		l = slog.LevelError
	case "info":
		l = slog.LevelInfo
	default:
		l = slog.LevelWarn
	}
	return &slogAdapter{level: l}
}

func (a *slogAdapter) LogMode(level logger.LogLevel) logger.Interface {
	next := *a
	switch level {
	case logger.Silent:
		next.level = slog.LevelDebug + 100
	case logger.Error:
		next.level = slog.LevelError
	case logger.Warn:
		next.level = slog.LevelWarn
	default:
		next.level = slog.LevelInfo
	}
	return &next
}

func (a *slogAdapter) Info(ctx context.Context, msg string, data ...any) {
	if slog.LevelInfo >= a.level {
		slog.InfoContext(ctx, msg, data...)
	}
}

func (a *slogAdapter) Warn(ctx context.Context, msg string, data ...any) {
	if slog.LevelWarn >= a.level {
		slog.WarnContext(ctx, msg, data...)
	}
}

func (a *slogAdapter) Error(ctx context.Context, msg string, data ...any) {
	if slog.LevelError >= a.level {
		slog.ErrorContext(ctx, msg, data...)
	}
}

func (a *slogAdapter) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	sqlStr, rows := fc()
	if err != nil {
		if slog.LevelError >= a.level {
			slog.ErrorContext(ctx, "database error",
				"error", err, "duration", elapsed, "rows", rows, "sql", sqlStr,
			)
		}
		return
	}
	if slog.LevelInfo >= a.level {
		slog.InfoContext(ctx, "database query",
			"duration", elapsed, "rows", rows, "sql", sqlStr,
		)
	}
}

// Module 在配置 db.driver/db.dsn 时向 Fx 暴露 [*Client]。
var Module = fx.Module("fxkit/gormx",
	config.Provide[Config]("db"),
	fx.Provide(New),
	fx.Invoke(registerClose),
)

type closeParams struct {
	fx.In
	LC     fx.Lifecycle
	Client *Client `optional:"true"`
}

func registerClose(p closeParams) {
	if p.Client == nil || p.Client.Pool() == nil {
		return
	}
	p.LC.Append(fx.Hook{
		OnStop: func(context.Context) error {
			sqlDB, err := p.Client.Pool().DB()
			if err != nil {
				return nil
			}
			return sqlDB.Close()
		},
	})
}
