// Package model 定义与数据库表一一对应的实体。
//
// 跨库约束（sqlite / mysql / postgres 共用一套 AutoMigrate）：
//
//   - 枚举一律存 varchar，不用各家的原生 enum；
//   - 结构化数据用 text 存 JSON 字符串，不用 PG 的 jsonb；
//   - 时间统一 UTC；
//   - 工时/金额用 decimal(10,2)，不用 float（浮点求和会漂）。
package model

import (
	"time"

	"gorm.io/gorm"
)

// BaseModel 是所有业务实体的公共字段。
//
// ID 用自增整数：跨库行为统一，前端展示为 #12。
// 全局唯一的外部标识另由 Code 字段承担（见各实体的业务编号）。
type BaseModel struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// DeletedAt 让「删除」变成打时间戳。相比禅道的 deleted tinyint，
	// 这里额外保留了删除时间；GORM 的默认查询会自动过滤掉已删除行。
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// 优先级与严重程度取值范围（沿用禅道：1 最高，4 最低，0 表示未设置）。
const (
	PriUnset  = 0
	PriHigh   = 1
	PriMid    = 2
	PriLow    = 3
	PriLowest = 4
)

// ACL 取值。
const (
	ACLOpen    = "open"    // 全员可见
	ACLPrivate = "private" // 仅白名单可见
)
