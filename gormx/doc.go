// Package gormx 为 fxkit 提供生产级 GORM 客户端集成与生命周期管理。
//
// # 核心特性
//
//   - 多驱动支持：内置支持 MySQL、PostgreSQL 和 SQLite（自动补齐 WAL/busy_timeout/外键检查等优化）。
//   - 结构化可观测性：内置 slogAdapter 将 GORM 日志无缝桥接至标准库 log/slog，集成上下文 TraceID。
//   - 慢查询告警：支持 SlowThreshold 配置（默认 200ms），超过阈值的 SQL 执行自动输出 WARN 级别慢查询日志。
//   - 请求级事务与会话：通过 Client.Conn(ctx) 与 Client.Transaction(ctx, fn) 实现请求级上下文隔离与嵌套安全的事务透传。
//   - 错误自动翻译：始终启用 TranslateError，确保底层唯一约束或外键冲突可被上层 MapDBError 识别。
package gormx
