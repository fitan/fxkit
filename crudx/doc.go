// Package crudx 是面向 GORM 的受控 list 查询与领域辅助引擎，统一多服务列表协议与安全边界。
//
// # 核心特性
//
//   - 受控查询语言：基于 [ListSpec] 白名单解析 q= 参数，防止任意 SQL 注入或全表扫描。
//   - 丰富的比较操作符：=, !=, ~, !~, >, >=, <, <=, .in:, .notin:, .isnull, .notnull。
//   - 受控 OR 组：q=or(status=active,status=pending)；支持引号字面量保护（例如 or(tag='a,b',status=active)）。
//   - 键集游标分页（Keyset Pagination）：基于排序字段 + 唯一主键构建不透明 Cursor/NextCursor，消除大偏移深度分页性能黑洞；
//     自动支持 time.Time 时间字段的 RFC3339 序列化与原生驱动还原；递归支持 gorm.Model 等内嵌结构体排序。
//   - 安全的模糊搜索：自动转义 LIKE 中的 %、_ 与 \，严格要求字段声明 Indexed: true。
//   - 一站式查询封装：[List]（关联去重、Count Distinct、Limit/Offset 或 Keyset Cursor、声明式 Preloads 与 DTO 投影）。
//   - 领域读写辅助：[FirstByID]、[GetByID]、[ZeroPrimaryKey]、[CopyPrimaryKey]、[MapDBError]。
//
// # 操作符语法速查表
//
//	q=name=alice                 // 等于
//	q=status!=disabled           // 不等于
//	q=name~john                  // 包含（LIKE %john%，需 Indexed: true）
//	q=name!~test                 // 不包含（NOT LIKE %test%，需 Indexed: true）
//	q=age>=18                    // 大于等于
//	q=score<60                   // 小于
//	q=status.in:active,pending   // IN 枚举
//	q=role.notin:guest,banned    // NOT IN
//	q=deleted_at.isnull          // IS NULL
//	q=updated_at.notnull         // IS NOT NULL
//	q=or(status=1,role=admin)    // 受控 OR 组合
//	q=remark='test=123'          // 带特殊字符或逗号的引号字面量
//
// # 标准使用示例
//
//	var userListSpec = crudx.ListSpec{
//	    Fields: map[string]crudx.FieldSpec{
//	        "name":      {Column: "users.name", Kind: crudx.FieldString, Indexed: true},
//	        "status":    {Column: "users.status", Kind: crudx.FieldString},
//	        "createdAt": {Column: "users.created_at", Kind: crudx.FieldTime},
//	    },
//	    SortFields: map[string]string{
//	        "createdAt": "users.created_at",
//	    },
//	    DefaultSort: "-createdAt",
//	}
//
//	res, err := crudx.List(ctx, crudx.ListInput[User, UserRow]{
//	    DB:       client.Conn(ctx).Model(&User{}),
//	    Spec:     userListSpec,
//	    Params:   params,
//	    Preloads: []string{"Profile"},
//	    ToRow:    func(u User) UserRow { return UserRow{ID: u.ID, Name: u.Name} },
//	})
//
// # 事务与写操作
//
// 事务使用 gormx.Client.Transaction；在事务回调中 Conn(txCtx) 自动加入同一事务上下文。
// 跨表/多服务一致性请配合 fxkit/outbox（Transactional Outbox 模块）使用。
package crudx
