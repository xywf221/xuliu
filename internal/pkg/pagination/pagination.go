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
	"gorm.io/gorm/clause"
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

// orderBy 把 "severity_asc,id_desc" 这样的排序串翻译成 GORM 的排序子句。
//
// 为什么不直接拼字符串交给 Order()：Order 收到字符串时会把整段标成
// clause.Column{Raw: true} —— Raw 表示「原样输出、不加引号」。于是
// `ORDER BY pm_bug.order DESC` 会一字不改地发出去，而 order 是
// SQLite / MySQL / PostgreSQL 三家的保留字，语句直接语法错误
// （pm_bug.order、pm_task.order、sys_dept.order 这些列都在白名单里，
// 也就是七个列表接口的 ?order=order_desc 都会崩）。
// 用 clause.Column 表达「表名 + 列名」，GORM 才会按方言加引号：
// MySQL 反引号，PG 与 SQLite 双引号。这正是「换库只改配置」应有的样子。
//
// 白名单外的键会被丢弃（而不是报错）：前端表格常把当前排序状态原样透传，
// 为一次纯展示性的排序失败而中断整个列表请求不划算。
func (s Sorter) orderBy(order string, fallback string) clause.OrderBy {
	columns := make([]clause.OrderByColumn, 0, 4)
	for _, item := range strings.Split(order, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		key, desc := item, false
		if base, suffix, found := strings.CutLast(item, "_"); found {
			switch strings.ToLower(suffix) {
			case "asc":
				key, desc = base, false
			case "desc":
				key, desc = base, true
			}
		}

		col, ok := s[key]
		if !ok {
			slog.Debug("忽略白名单外的排序字段", slog.String("key", key))
			continue
		}
		columns = append(columns, orderByColumn(col, desc))
	}

	if len(columns) == 0 {
		// fallback 是调用方写死的表达式，形如 "pm_bug.id DESC"，
		// 也有 "pm_effort.date DESC, pm_effort.id DESC" 这种多列的。
		// 逐段拆开同样交给 GORM 加引号：里面就出现过 pm_product.order
		// 这种带保留字的默认排序，原样拼字符串它会崩。
		for _, item := range strings.Split(fallback, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name, dir, found := strings.CutLast(item, " ")
			if !found {
				name = item
			}
			columns = append(columns, orderByColumn(name, strings.Contains(strings.ToUpper(dir), "DESC")))
		}
	}
	return clause.OrderBy{Columns: columns}
}

// orderByColumn 把白名单里的 "pm_bug.severity" 拆成表名与列名。
//
// 拆开后 GORM 会分别加引号（MySQL 得到 `pm_bug`.`severity`，PG 得到
// "pm_bug"."severity"），这是三家都认的写法；整串当一个列名的话，
// 引号会套在含点号的整个串外面，成了非法标识符。
func orderByColumn(expr string, desc bool) clause.OrderByColumn {
	table, name, found := strings.CutLast(expr, ".")
	if !found {
		table, name = "", expr
	}
	return clause.OrderByColumn{
		Column: clause.Column{Table: table, Name: name},
		Desc:   desc,
	}
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
		Order(sorter.orderBy(p.Order, defaultOrder)).
		Limit(p.Size).
		Offset(p.Offset()).
		Find(&list).Error; err != nil {
		return nil, err
	}

	result.List = list
	return result, nil
}
