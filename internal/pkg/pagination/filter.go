package pagination

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// 本文件的辅助函数都接收列名参数。
//
// **列名必须来自代码里写死的常量，绝不能传用户输入** —— 这些函数内部会把它
// 直接拼进 SQL。用户可控的部分永远只走 ? 占位符。

// likeEscaper 转义 LIKE 模式里的通配符，避免用户输入的 % 变成「匹配一切」。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Keyword 在多个列上做 OR 模糊匹配。
//
// 列名前缀必须带上表名（如 "pm_bug.title"），因为仓库层的查询常带 join。
func Keyword(q *gorm.DB, value string, columns ...string) *gorm.DB {
	value = strings.TrimSpace(value)
	if value == "" || len(columns) == 0 {
		return q
	}

	pattern := "%" + likeEscaper.Replace(value) + "%"
	conds := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, col := range columns {
		conds = append(conds, col+` LIKE ? ESCAPE '\'`)
		args = append(args, pattern)
	}
	// 整体加括号，否则 OR 会和外层的 AND 条件错位。
	return q.Where("("+strings.Join(conds, " OR ")+")", args...)
}

// Eq 在 value 非零值时追加等值条件。
//
// 用途：过滤 DTO 里「空值表示不过滤」的字段。
func Eq[T comparable](q *gorm.DB, column string, value T) *gorm.DB {
	var zero T
	if value == zero {
		return q
	}
	return q.Where(column+" = ?", value)
}

// EqPtr 在指针非 nil 时追加等值条件。
//
// 用于「0 也是合法取值」的场景（如 pri=0 表示无优先级）。
func EqPtr[T any](q *gorm.DB, column string, value *T) *gorm.DB {
	if value == nil {
		return q
	}
	return q.Where(column+" = ?", *value)
}

// In 追加 IN 条件。values 为空时视为「不加该过滤条件」。
func In[V any](q *gorm.DB, column string, values []V) *gorm.DB {
	if len(values) == 0 {
		return q
	}
	return q.Where(column+" IN ?", values)
}

// TimeRange 追加闭区间时间过滤，任一端为 nil 表示该端不限。
func TimeRange(q *gorm.DB, column string, from, to *time.Time) *gorm.DB {
	if from != nil {
		q = q.Where(column+" >= ?", *from)
	}
	if to != nil {
		q = q.Where(column+" <= ?", *to)
	}
	return q
}
