// Package crudx 提供面向类型化 CRUD 的 GORM 辅助与一层泛型仓库：
//
//   - 泛型仓库：[Repo]、[NewRepo]（Create/GetByID/List/Update/Delete + First/Exists）
//   - ZStack 风格 list 引擎：[ListParams]、[ListSpec]、[ParseQGroups]、[ApplyList]
//   - 受控 OR：q=or(a=1,b=2)；空值：field.isnull / field.notnull；[FieldTime]
//   - LIKE 字面匹配（转义 %/_，ESCAPE '\'）；游标分页：Cursor / NextCursor
//   - 便捷自由函数：[List]（filter+sort+cursor+映射）、[GetByID]
//   - 标量辅助：[FirstByID]、[ResolveModel]、[FormatPrimaryKey]、[ZeroPrimaryKey]、
//     [Preload]、[MapDBError]
//   - 响应封装：[ListResult]；HTTP 胶水见 fxkit/huma（ListQueryInput / RegisterResource）
//
// 推荐用法：业务仓库嵌入 *Repo[T]，只写领域查询（如 GetByEmail）；Service 做校验、
// 事务与 DTO 映射。软删请直接用 gorm.DeletedAt。
//
// # Transactions
//
// 事务可用 [Repo.Transaction] 或 [gormx.Client.Transaction]；回调内 Conn(txCtx)
// 自动加入同一 tx。多表写入与 outbox 同事务请用 Transactional Outbox（fxkit/outbox）。
package crudx
