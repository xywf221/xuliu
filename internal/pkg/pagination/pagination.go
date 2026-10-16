// Package pagination 定义全站列表接口共用的分页、排序与过滤协议。
//
// 所有列表接口的 query 参数形状一致：
//
//	GET /api/v1/bugs?page=1&size=20&order=severity_asc,id_desc&status=active
//
// 响应形状也一致：
//
//	{"data":{"list":[...],"total":137,"page":1,"size":20}}
package pagination

import (
	"context"
	"log/slog"
	"strings"

	"gorm.io/gorm"
)

const (
	// DefaultSize 是未指定 size 时的每页条数。
	DefaultSize = 20
	// MaxSize 是每页条数上限，防止一次把整张表拉走。
	MaxSize = 200
)

// Params 是所有列表接口共用的分页/排序参数。
type Params struct {
	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// Normalize 把越界或缺省的分页参数收敛到合法范围，并返回自身副本。
func (p Params) Normalize() Params {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Size < 1 {
		p.Size = DefaultSize
	}
	if p.Size > MaxSize {
		p.Size = MaxSize
	}
	return p
}

// Offset 返回 SQL OFFSET。
func (p Params) Offset() int { return (p.Page - 1) * p.Size }

// Result 是所有列表接口共用的响应结构。
type Result[T any] struct {
	List  []T   `json:"list"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

// Sorter 是可排序字段的白名单，键是对外的排序名，值是真实的 SQL 表达式。
//
//	bugSorter := pagination.Sorter{"id": "pm_bug.id", "severity": "pm_bug.severity"}
//
// 这是安全边界：ORDER BY 无法用占位符参数化，所以排序字段只能是白名单里的
// 常量。**绝不要把用户输入直接拼进 SQL** —— 这也是不引入第三方分页库的原因，
// 那样就得相信它对注入的处理。
type Sorter map[string]string

// clause 把 "severity_asc,id_desc" 这样的排序串翻译成安全的 ORDER BY。
//
// 白名单外的键会被丢弃（而不是报错）：前端表格常把当前排序状态原样透传，
// 为一次纯展示性的排序失败而中断整个列表请求不划算。
func (s Sorter) clause(order string, fallback string) string {
	var parts []string
	for _, item := range strings.Split(order, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		key, dir := item, "asc"
		if base, suffix, found := strings.CutLast(item, "_"); found {
			switch strings.ToLower(suffix) {
			case "asc":
				key, dir = base, "ASC"
			case "desc":
				key, dir = base, "DESC"
			}
		}

		col, ok := s[key]
		if !ok {
			slog.Debug("忽略白名单外的排序字段", slog.String("key", key))
			continue
		}
		parts = append(parts, col+" "+dir)
	}

	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, ", ")
}

// Paginate 对传入的查询执行「先计数、再取当页」。
//
// 调用方需先用 Model(&Entity{}) 与 Where(...) 把查询条件搭好，例如：
//
//	q := db.Model(&model.Bug{}).Where("product_id = ?", productID)
//	res, err := pagination.Paginate[model.Bug](ctx, q, params, bugSorter, "pm_bug.id DESC")
//
// defaultOrder 由调用方给出（不能用常量拼接，必须是代码里写死的表达式）。
func Paginate[T any](ctx context.Context, q *gorm.DB, p Params, sorter Sorter, defaultOrder string) (*Result[T], error) {
	p = p.Normalize()

	// Session(&gorm.Session{}) 让计数与取数各用一个干净的 statement，
	// 否则 Count 会污染后续 Find 的 SQL。
	var total int64
	if err := q.WithContext(ctx).Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, err
	}

	result := &Result[T]{Total: total, Page: p.Page, Size: p.Size, List: []T{}}
	if total == 0 {
		// 空结果直接返回空数组（而非 null），前端少一层判空。
		return result, nil
	}

	list := make([]T, 0, p.Size)
	if err := q.WithContext(ctx).
		Order(sorter.clause(p.Order, defaultOrder)).
		Limit(p.Size).
		Offset(p.Offset()).
		Find(&list).Error; err != nil {
		return nil, err
	}

	result.List = list
	return result, nil
}
