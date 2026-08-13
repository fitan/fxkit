package crudx

import "gorm.io/gorm"

// Preload 返回对每个 association 名执行 GORM Preload 的 scope。
// 用于传给 [FirstByID] 或自定义 Model().Scopes(...) 的 list/get 链。
func Preload(names ...string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		for _, n := range names {
			if n != "" {
				db = db.Preload(n)
			}
		}
		return db
	}
}
