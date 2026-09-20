# crudx: 面向 GORM 的受控列表查询引擎与领域辅助库

`crudx` 是 `fxkit` 生态中用于构建生产级 REST API 列表和受控查询的专用引擎。

不同于传统的“泛型万能 CRUD 框架”，`crudx` 的定位是 **受控的 List 查询引擎（统一 `q=` 过滤、多表 JOIN、Keyset 游标分页、字段白名单）+ 显式的领域写操作辅助**。

---

## 核心设计理念

1. **白名单受控（ListSpec）**：客户端只能查询和排序在 `ListSpec` 中显式登记的字段，防止未建索引的全表扫描或恶意注入。
2. **统一查询协议（ZStack 风格）**：所有微服务共用统一的 `q=` 语法（如 `q=status=active`, `q=or(a=1,b=2)`）。
3. **消除深度分页黑洞（Keyset Cursor）**：内置双向键集游标分页，`O(1)` 时间复杂度获取下一页数据，彻底解决传统 `OFFSET 1000000` 造成的数据库抖动。
4. **安全与鲁棒**：
   - 自动转义模糊查询中的 SQL 通配符（`%`、`_` 与 `\`）；
   - 自动防范空集 `IN []`（自动转为 `1 = 0`）；
   - 自动递归解析嵌入结构体（如 `gorm.Model`）的主键与排序字段；
   - 自动识别并还原游标中的 RFC3339 时间戳对象，适配各类数据库驱动。

---

## 查询语法速查表（`q=` 参数）

支持通过 HTTP Query 参数传递多个 `q`（`?q=field1=val1&q=field2=val2`）：

| 语法 | 说明 | 示例 | 备注 |
| :--- | :--- | :--- | :--- |
| `field=value` | 等于 | `q=status=active` | 字符串可带引号保护：`q=name="Doe, John"` |
| `field!=value` | 不等于 | `q=status!=disabled` | 支持 `q=deleted_at!=null` |
| `field~value` | 模糊匹配 (LIKE %v%) | `q=name~alice` | 自动转义通配符，字段必须标明 `Indexed: true` |
| `field!~value` | 模糊不匹配 (NOT LIKE) | `q=email!~spam` | 字段必须标明 `Indexed: true` |
| `field>value` | 大于 | `q=cpuNum>4` | 支持数值与时间 |
| `field>=value` | 大于等于 | `q=age>=18` | 操作符自动长词优先匹配 |
| `field<value` | 小于 | `q=score<60` | 支持数值与时间 |
| `field<=value` | 小于等于 | `q=price<=99.9` | |
| `field.in:v1,v2` | 枚举集合 (IN) | `q=status.in:active,pending` | 自动受 `MaxInValues` 上限保护 |
| `field.notin:v1,v2`| 排除枚举 (NOT IN) | `q=role.notin:guest,banned` | |
| `field.isnull` | 为 NULL | `q=deleted_at.isnull` | |
| `field.notnull` | 非 NULL | `q=confirmed_at.notnull` | |
| `or(cond1,cond2)` | 受控 OR 组 | `q=or(status=active,role=admin)` | 组内条件用逗号分隔，引号内逗号不受影响 |

---

## 快速上手

### 1. 定义实体与 `ListSpec`

```go
package users

import (
    "time"
    "github.com/fitan/fxkit/crudx"
    "gorm.io/gorm"
)

type User struct {
    gorm.Model
    Name     string `gorm:"size:64;index"`
    Status   string `gorm:"size:32;index"`
    Role     string `gorm:"size:32"`
    OrgID    int64  `gorm:"index"`
}

type UserRow struct {
    ID        uint      `json:"id"`
    Name      string    `json:"name"`
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"createdAt"`
}

var UserListSpec = crudx.ListSpec{
    Fields: map[string]crudx.FieldSpec{
        "id":        {Column: "users.id", Kind: crudx.FieldNumber},
        "name":      {Column: "users.name", Kind: crudx.FieldString, Indexed: true},
        "status":    {Column: "users.status", Kind: crudx.FieldString},
        "role":      {Column: "users.role", Kind: crudx.FieldString},
        "createdAt": {Column: "users.created_at", Kind: crudx.FieldTime},
    },
    SortFields: map[string]string{
        "id":        "users.id",
        "createdAt": "users.created_at",
    },
    DefaultSort: "-createdAt", // 默认降序
}
```

### 2. 在 Service 中执行 `crudx.List`

```go
func (s *Service) List(ctx context.Context, params crudx.ListParams) (crudx.ListResult[UserRow], error) {
    return crudx.List(ctx, crudx.ListInput[User, UserRow]{
        DB:       s.db.Conn(ctx).Model(&User{}),
        Spec:     UserListSpec,
        Params:   params,
        Preloads: []string{"Profile"}, // 声明式预加载
        ToRow: func(u User) UserRow {
            return UserRow{
                ID:        u.ID,
                Name:      u.Name,
                Status:    u.Status,
                CreatedAt: u.CreatedAt,
            }
        },
    })
}
```

### 3. 多表关联查询（Relations）

支持多级关联（最高支持 3 级嵌套），当且仅当查询条件用到关联字段时，才动态按需拼装 `LEFT JOIN`：

```go
var OrderListSpec = crudx.ListSpec{
    Fields: map[string]crudx.FieldSpec{
        "orderNo": {Column: "orders.order_no", Kind: crudx.FieldString},
    },
    Relations: map[string]*crudx.RelationSpec{
        "user": {
            Table: "users",
            Alias: "user",
            On:    "user.id = orders.user_id",
            Fields: map[string]crudx.FieldSpec{
                "name": {Column: "user.name", Kind: crudx.FieldString, Indexed: true},
            },
        },
    },
}
```
此时前端可通过 `q=user.name~Alice` 进行跨表过滤，底层自动引入 `LEFT JOIN users AS user ON user.id = orders.user_id` 并对根模型做 `DISTINCT` 去重！

---

## 游标分页（Keyset Cursor）

### 客户端调用模式
- **首屏请求**：传 `useCursor=true` 或 `limit=20`。
- **返回数据**：响应中包含 `nextCursor` 字符串（例如 `"eyJ2Ijox...`）。
- **翻下一页**：客户端将该字符串作为 `cursor` 参数带入（`?cursor=eyJ2Ijox...`），无需再传递 `start`。
- **降级保护**：游标自动锁定首次排序方向，防止翻页时混用不同排序破坏分页连续性。

---

## 领域实体辅助工具

- **主键解析与置零**：
  ```go
  crudx.ResolveModel[User](db)     // 服务初始化时缓存元数据
  crudx.ZeroPrimaryKey(&user)       // Create 前将自增主键清零
  ```
- **单行安全加载**：
  ```go
  // 根据主键加载单行并投影为 DTO，若不存在返回统一的 fxerrors.NotFound 错误
  detail, err := crudx.GetByID(ctx, db, idStr, func(u User) UserDetail {
      return UserDetail{ID: u.ID, Name: u.Name}
  })
  ```
- **数据库异常翻译**：
  ```go
  if err := db.Create(&user).Error; err != nil {
      return crudx.MapDBError(err) // 唯一索引冲突自动转为 Conflict 错误等
  }
  ```
