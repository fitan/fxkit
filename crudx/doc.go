// Package crudx 是面向 GORM 的受控 list 查询引擎，不是泛型 CRUD 框架。
//
// 核心是统一的列表协议，让多服务共用同一种 q= / 分页语义：
//
//   - 查询语言：[ListParams]、[ListSpec]、[ParseQGroups]、[ApplyList]
//   - 受控 OR：q=or(a=1,b=2)；空值：field.isnull / field.notnull；[FieldTime]
//   - LIKE 字面匹配（转义 %/_，ESCAPE '\'）；游标分页：Cursor / NextCursor
//   - 一站式查询：[List]（filter + sort + cursor + Count Distinct + DTO 投影）
//   - 读辅助：[GetByID]、[FirstByID]、[Preload]
//   - 写辅助（由 Service 显式调用）：[ZeroPrimaryKey]、[MapDBError]、[ResolveModel]
//   - 响应封装：[ListResult]；HTTP 胶水见 fxkit/huma（ListQueryInput；可选 RegisterResource）
//
// 推荐用法：在 Service 里写 List/Get/Create/Update/Delete；List 走 [List] + [ListSpec]
// 白名单，写操作直接用 gormx。软删请用 gorm.DeletedAt。
// [Repo] 是可选兼容封装，新代码不必嵌入。
//
// # Transactions
//
// 事务用 [gormx.Client.Transaction]；回调内 Conn(txCtx) 自动加入同一 tx。
// 多表写入与 outbox 同事务请用 Transactional Outbox（fxkit/outbox）。
package crudx
