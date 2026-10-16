package repository

import "gorm.io/gorm"

// 本文件处理「同一段 SQL 在三库下都要对」的少数几处差异。
//
// 目前只有一处：排序列名叫 order。

// orderColumn 返回按当前方言正确引用的排序列名。
//
// 为什么需要这个函数：`order` 在 MySQL 里是保留字，在 PostgreSQL 里也是，
// 只有 SQLite 允许它不加引号。而引用符号三家还不一样 ——
// MySQL 用反引号，SQLite 与 PostgreSQL 用双引号。
//
// 之前各处写的是 `ORDER BY "order" ASC`：在 SQLite / PG 下正确，
// 但在 MySQL 下双引号是「字符串字面量」而不是标识符，
// 于是 `ORDER BY "order"` 变成按一个常量排序 —— **不报错，只是不排序**。
// 这类静默失效正是「换库只改配置」最怕的形态，所以集中在
// 一个函数里按方言分支。
func orderColumn(db *gorm.DB) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		return "`order`"
	}
	return `"order"`
}
