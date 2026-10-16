// Package repository 封装数据访问。
//
// 分两层：
//
//	Repo[T]  —— 泛型基类，提供所有实体共用的 CRUD 与分页；
//	XxxRepo —— 各实体的具名类型，承载该实体特有的查询。
//
// 具名类型存在的意义不只是「有个地方放特有查询」：它还让 service 的
// 依赖声明变得可读（依赖 *StoryRepo，而不是泛型实例 *Repo[model.Story]），
// 并且在测试里更容易替换成假实现。
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// Repo 是泛型仓储基类。
//
// 用 Go 1.18+ 的泛型把「按 id 取、计数、分页」这类与实体无关的操作
// 收敛到一处，避免在 30 张表上复制粘贴同样的代码。
type Repo[T any] struct {
	// DB 是根数据库句柄。
	DB *gorm.DB
}

// New 构造仓储基类。
func New[T any](db *gorm.DB) *Repo[T] { return &Repo[T]{DB: db} }

// db 返回指定事务句柄（为 nil 时用根句柄）。
//
// 让同一份仓储方法既能用于普通查询，也能参与调用方的事务，
// 从而不必为事务场景另写一套方法。
func (r *Repo[T]) db(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.DB
}

// WithTx 返回绑定到指定事务的仓储副本。
//
// service 在事务里需要复用仓储时，用这个方法拿到绑定句柄的副本，
// 而不是把 tx 一路当参数传下去。
func (r *Repo[T]) WithTx(tx *gorm.DB) *Repo[T] {
	return &Repo[T]{DB: tx}
}

// GetByID 按主键取一条记录。不存在时返回 apperr.NotFound。
func (r *Repo[T]) GetByID(ctx context.Context, id uint) (*T, error) {
	if id == 0 {
		return nil, apperr.New(apperr.NotFound)
	}
	var out T
	if err := r.DB.WithContext(ctx).First(&out, id).Error; err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// FindByID 按主键取一条记录。不存在时返回 (nil, nil)。
//
// 与 GetByID 的区别在于「不存在」是不是错误：需要向外报 404 的用前者，
// 只做存在性判断或做可选关联的用后者。
func (r *Repo[T]) FindByID(ctx context.Context, id uint) (*T, error) {
	if id == 0 {
		return nil, nil
	}
	var out T
	err := r.DB.WithContext(ctx).First(&out, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// Exists 报告指定条件是否存在记录。
func (r *Repo[T]) Exists(ctx context.Context, query string, args ...any) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(new(T)).Where(query, args...).Limit(1).Count(&count).Error
	if err != nil {
		return false, translate(err)
	}
	return count > 0, nil
}

// Count 统计满足条件的记录数。
func (r *Repo[T]) Count(ctx context.Context, query string, args ...any) (int64, error) {
	q := r.DB.WithContext(ctx).Model(new(T))
	if query != "" {
		q = q.Where(query, args...)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return 0, translate(err)
	}
	return count, nil
}

// List 按条件取列表（不分页）。调用方需自行限制数量，避免拉全表。
func (r *Repo[T]) List(ctx context.Context, q *gorm.DB, limit int) ([]T, error) {
	if q == nil {
		q = r.DB.WithContext(ctx).Model(new(T))
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []T
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// Create 插入一条记录。
func (r *Repo[T]) Create(ctx context.Context, tx *gorm.DB, obj *T) error {
	if err := r.db(tx).WithContext(ctx).Create(obj).Error; err != nil {
		return translate(err)
	}
	return nil
}

// CreateBatch 批量插入。
func (r *Repo[T]) CreateBatch(ctx context.Context, tx *gorm.DB, objs []T) error {
	if len(objs) == 0 {
		return nil
	}
	if err := r.db(tx).WithContext(ctx).Create(&objs).Error; err != nil {
		return translate(err)
	}
	return nil
}

// Save 全量保存一条记录。
func (r *Repo[T]) Save(ctx context.Context, tx *gorm.DB, obj *T) error {
	if err := r.db(tx).WithContext(ctx).Save(obj).Error; err != nil {
		return translate(err)
	}
	return nil
}

// UpdateFields 更新指定字段。
//
// 用 map 而非结构体：结构体更新会跳过零值字段，导致
// 「把 assignedTo 改成 0」这类清空操作静默失效。
func (r *Repo[T]) UpdateFields(ctx context.Context, tx *gorm.DB, id uint, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(new(T)).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}

	// 影响 0 行有两种可能：记录不存在，或所有字段的新值与旧值相同。
	//
	// 后者不是边角情况：前端整表单提交时，总有字段没被改动。
	// 而 MySQL 默认只统计「真正变化的行」（clientFoundRows=false），
	// 与 sqlite / postgres 的“匹配行数”语义不同。
	// 若把它一律当成 NotFound，就会出现「没改该字段就报 404」的跨库行为差异。
	// 所以再确认一次存在性 —— 仅在 0 行时多这一次查询。
	var count int64
	err := r.db(tx).WithContext(ctx).Model(new(T)).Where("id = ?", id).
		Limit(1).Count(&count).Error
	if err != nil {
		return translate(err)
	}
	if count == 0 {
		return apperr.New(apperr.NotFound)
	}
	return nil
}

// UpdateWhere 按条件批量更新指定字段，返回影响行数。
func (r *Repo[T]) UpdateWhere(ctx context.Context, tx *gorm.DB, query string, args []any, fields map[string]any) (int64, error) {
	if len(fields) == 0 || query == "" {
		return 0, nil
	}
	res := r.db(tx).WithContext(ctx).Model(new(T)).Where(query, args...).Updates(fields)
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}

// Delete 软删除一条记录。
func (r *Repo[T]) Delete(ctx context.Context, tx *gorm.DB, id uint) error {
	res := r.db(tx).WithContext(ctx).Delete(new(T), id)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return apperr.New(apperr.NotFound)
	}
	return nil
}

// DeleteWhere 按条件软删除。
func (r *Repo[T]) DeleteWhere(ctx context.Context, tx *gorm.DB, query string, args ...any) (int64, error) {
	res := r.db(tx).WithContext(ctx).Where(query, args...).Delete(new(T))
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}

// Paginate 对查询做分页并返回统一结构。
func (r *Repo[T]) Paginate(ctx context.Context, q *gorm.DB, params pagination.Params, sorter pagination.Sorter, defaultOrder string) (*pagination.Result[T], error) {
	if q == nil {
		q = r.DB.WithContext(ctx).Model(new(T))
	}
	out, err := pagination.Paginate[T](ctx, q, params, sorter, defaultOrder)
	if err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// translate 把 GORM 错误翻译为应用错误。
//
// 集中处理的价值在于：上层不必到处写 errors.Is(err, gorm.ErrRecordNotFound)，
// 也不会把驱动层的错误类型泄漏到 handler。
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return apperr.New(apperr.NotFound)
	case errors.Is(err, gorm.ErrDuplicatedKey):
		// 唯一索引冲突 → 409。具体是哪个字段冲突由调用方补充文案，
		// 因为仓储层不知道业务语义（「账号已占用」还是「代号已占用」）。
		//
		// 必须用 Wrap 而不是 New 把 cause 带上：调用方靠
		// errors.Is(err, gorm.ErrDuplicatedKey)（即 IsDuplicated）来判断
		// 「是不是唯一冲突」；丢掉 cause 就等于把这个信息抹掉，
		// IsDuplicated 将永远返回 false，「账号已被占用」这类具体提示
		// 就永远生效不了。
		return apperr.Wrap(apperr.Conflict, err)
	default:
		return apperr.Wrap(apperr.Internal, fmt.Errorf("repository: %w", err))
	}
}

// IsDuplicated 报告错误是否为唯一索引冲突。
//
// 供 service 把一个笼统的 Conflict 细化成「账号已存在」这类具体提示。
func IsDuplicated(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateMessage(err)
}

// isDuplicateMessage 兜底识别唯一冲突。
//
// 原因：GORM 只有在驱动正确实现 ErrDuplicatedKey 转换时才会返回该哨兵错误，
// 而 sqlite 驱动（ncruces/go-sqlite3）走的是通用路径，冲突会以
// "UNIQUE constraint failed" 文本形式出现。为了让「账号已占用」这类
// 提示在三库下行为一致，这里按错误文本再识别一次。
func isDuplicateMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"UNIQUE constraint failed", // sqlite
		"Duplicate entry",          // mysql
		"duplicate key value",      // postgres
		"UNIQUE constraint",        // 通用
	} {
		if containsFold(msg, marker) {
			return true
		}
	}
	return false
}

// containsFold 是大小写不敏感的包含判断。
//
// 直接用 strings 的组合，而不自己逐字节比较：这里只在出错路径上跑，
// 一次 ToLower 的分配完全不值得换成手写循环。
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
