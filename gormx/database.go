// Package gormx 装配 [gorm.io/gorm] 连接，使用 otelsql 插桩驱动与 slog 驱动的 GORM logger。
// 驱动由 `db.driver` 配置选择（mysql | postgres | sqlite）。`db.driver` 为空时模块短路返回 nil，
// 宿主应用可保留 Module 而不接入数据库。
//
// 从 di 注入 [*Client]；每个请求使用 [Client.Conn]，事务使用 [Client.Transaction]。
package gormx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/fitan/fxkit/config"
	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/server"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	if c == nil {
		return nil, nil
	}
	// 直接构造的 Config 不会经过 config.Load 的 SetDefaults；
	// 不补默认值时 database/sql 会得到 MaxOpenConns=0（无上限）、MaxIdleConns=0
	// （不保留空闲连接），压测下容易打爆数据库。
	cfg := *c
	cfg.Driver = strings.ToLower(strings.TrimSpace(cfg.Driver))
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.LogLevel = strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	if cfg.LogLevel == "" {
		cfg.LogLevel = "warn"
	}
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 100
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 10
	}
	if cfg.ConnMaxLifetime == 0 {
		cfg.ConnMaxLifetime = time.Hour
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	pool, err := openPool(&cfg)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, nil
	}
	return &Client{pool: pool, autoMigrate: cfg.AutoMigrate}, nil
}

// otelsql.Register 每次调用都会占用一个 database/sql 驱动槽（全局上限 1000）。
// 同一进程内反复 New（测试、多库实例）会耗尽槽位，因此按 driver+db.name 复用。
var (
	otelDriverMu    sync.Mutex
	otelDriverNames = map[string]string{}
)

func registerOTelDriver(driver, dbName string, opts []otelsql.Option) (string, error) {
	key := driver + "|" + dbName
	otelDriverMu.Lock()
	defer otelDriverMu.Unlock()
	if name, ok := otelDriverNames[key]; ok {
		return name, nil
	}
	name, err := otelsql.Register(driver, opts...)
	if err != nil {
		return "", err
	}
	otelDriverNames[key] = name
	return name, nil
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

	otelDriverName, err := registerOTelDriver(sqlDriver, dbName, otelOpts)
	if err != nil {
		// 回退到裸驱动会让这条连接完全失去 span/metrics（恰恰是排障时最需要的），
		// 且线上无法感知。宁可启动失败。
		return nil, fmt.Errorf("gormx: register otel driver for %s: %w", sqlDriver, err)
	}
	slog.Info("otelsql driver registered", "driver", otelDriverName)

	gormCfg := &gorm.Config{
		Logger:         newSlogAdapter(c.LogLevel, c.SlowThreshold),
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

// sqlDriverName 把配置中的 db.driver 映射为 database/sql 驱动名。
func sqlDriverName(driver string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
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
	level         slog.Level
	slowThreshold time.Duration
}

func newSlogAdapter(level string, slowThreshold time.Duration) logger.Interface {
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
	return &slogAdapter{level: l, slowThreshold: slowThreshold}
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
	// fc() 会格式化整条 SQL；只在确定要输出时才调用，避免每条查询都做一次
	// 字符串拼接（ErrRecordNotFound 是高频路径）。
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		if slog.LevelError < a.level {
			return
		}
		sqlStr, rows := fc()
		slog.ErrorContext(ctx, "database error",
			"error", err, "duration", elapsed, "rows", rows, "sql", sqlStr,
		)
		return
	}
	if a.slowThreshold > 0 && elapsed >= a.slowThreshold {
		if slog.LevelWarn < a.level {
			return
		}
		sqlStr, rows := fc()
		slog.WarnContext(ctx, "database slow query",
			"duration", elapsed, "threshold", a.slowThreshold, "rows", rows, "sql", sqlStr,
		)
		return
	}
	if slog.LevelInfo < a.level {
		return
	}
	sqlStr, rows := fc()
	slog.InfoContext(ctx, "database query",
		"duration", elapsed, "rows", rows, "sql", sqlStr,
	)
}

// Module 在配置 db.driver/db.dsn 时向 di 暴露 [*Client]。
var Module = di.Module("fxkit/gormx",
	config.Provide[Config]("db"),
	func(i di.Injector) {
		di.Provide(i, func(i di.Injector) (*Client, error) {
			cfg, err := di.Invoke[*Config](i)
			if err != nil {
				return nil, err
			}
			client, err := New(cfg)
			if err != nil {
				return nil, err
			}
			if client != nil && client.Pool() != nil {
				if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
					lc.Append(di.Hook{
						OnStop: func(context.Context) error {
							sqlDB, err := client.Pool().DB()
							if err != nil {
								return nil
							}
							return sqlDB.Close()
						},
					})
				}
			}
			return client, nil
		})

		// 数据库健康检查：由 server/readyz 聚合。
		// 必须使用 server.HealthChecker 这个**具名类型**注册 —— 曾在 gormx 内
		// 声明结构相同的匿名接口别名，导致 do 按接口名字符串区分服务，
		// group 被拆成多个互不相见的条目（/readyz 里看不到 "db"）。
		//
		// 注册推迟到 PreStart：在装配期直接 Invoke 会让 di.New 就连接数据库并 Ping，
		// 于是 `app validate`（文档承诺「不碰任何外部依赖」）、`app openapi`、`app mcp`
		// 都会去连库，且连接失败被吞掉。
		if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
			lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
				OnStart: func(context.Context) error {
					client, err := di.Invoke[*Client](i)
					if err != nil {
						return fmt.Errorf("gormx: %w", err)
					}
					if client != nil && client.Pool() != nil {
						di.ProvideGroupItem[server.HealthChecker](i, dbHealthChecker{client: client})
					}
					return nil
				},
			})
		}

		// 外部 SQL 平台执行 DDL 后，启动时校验 schema 契约（fail fast）。
		if cfg, err := di.Invoke[*Config](i); err == nil && cfg != nil && cfg.CheckSchema {
			if lc, err := di.Invoke[*di.Lifecycle](i); err == nil && lc != nil {
				lc.AppendPreStartPhase(di.PhaseResolveContributors, di.Hook{
					OnStart: func(ctx context.Context) error {
						client, err := di.Invoke[*Client](i)
						if err != nil || client == nil || client.Pool() == nil {
							return err
						}
						if err := CheckSchema(ctx, client.Pool()); err != nil {
							return fmt.Errorf("gormx: schema check failed (apply migrations via your SQL platform first, `myapp migrate print`): %w", err)
						}
						slog.Info("gormx: schema check passed")
						return nil
					},
				})
			}
		}
	},
)

// dbHealthChecker 实现 [server.HealthChecker]，对连接池做 Ping。
type dbHealthChecker struct {
	client *Client
}

// Name 返回检查项名称。
func (dbHealthChecker) Name() string { return "db" }

// Check 在超时内 Ping 数据库。
func (c dbHealthChecker) Check(ctx context.Context) error {
	// 防御 nil：HealthChecker 由 DI group 聚合而来，若某处误注册了零值，
	// 这里 panic 会让整个 /readyz 请求崩掉（并可能被 recover 成 500，
	// 掩盖真正的依赖故障）。返回错误更安全。
	if c.client == nil || c.client.Pool() == nil {
		return fmt.Errorf("db client is nil")
	}
	sqlDB, err := c.client.Pool().DB()
	if err != nil {
		return fmt.Errorf("db handle: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("db ping: %w", err)
	}
	return nil
}
