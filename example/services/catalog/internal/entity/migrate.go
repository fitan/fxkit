package entity

import "gorm.io/gorm"

// 迁移按聚合拆分：feature 只迁移自己拥有的表，测试也只迁移需要的表。
// 这样既保留了表归属，又避免 comment 为了 JOIN 去 import article 包。

// MigrateArticles 迁移文章与 fan-out 副表。
func MigrateArticles(db *gorm.DB) error {
	return db.AutoMigrate(&Article{}, &ArticleFanout{})
}

// MigrateComments 迁移评论表。
func MigrateComments(db *gorm.DB) error {
	return db.AutoMigrate(&Comment{})
}

// MigrateSystem 迁移 actor 计数器与心跳表。
func MigrateSystem(db *gorm.DB) error {
	return db.AutoMigrate(&Counter{}, &Heartbeat{})
}

// MigratePayments 迁移支付单与退款单表。
func MigratePayments(db *gorm.DB) error {
	return db.AutoMigrate(&Payment{}, &Refund{})
}
