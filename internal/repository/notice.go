package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 通知仓储。
//
// 通知天然是「一个人的」，所以这里的每个查询都带 userID：
// 读只读自己的、改只改自己的。把限制写在仓储签名里，而不是交给
// 调用方记得带上条件 —— 少写一次就是一个越权漏洞。

var noticeSorter = pagination.Sorter{
	"id":   "sys_notice.id",
	"date": "sys_notice.created_at",
}

// NoticeRepo 是通知仓储。
type NoticeRepo struct {
	*Repo[model.Notice]
}

// NewNoticeRepo 构造通知仓储。
func NewNoticeRepo(db *gorm.DB) *NoticeRepo {
	return &NoticeRepo{Repo: New[model.Notice](db)}
}

// NoticeFilter 是通知列表的过滤条件。
type NoticeFilter struct {
	// ToUser 必填：通知列表永远是「某人的通知」。
	ToUser uint
	// ObjectType + ObjectID 用于「这条需求上发生过什么通知」。
	ObjectType string
	ObjectID   uint
	// Unread 为 nil 表示不过滤；true 只看未读。
	Unread   *bool
	Keywords string

	Pagination pagination.Params
}

// List 分页查询某人的通知（按时间倒序）。
func (r *NoticeRepo) List(ctx context.Context, f NoticeFilter) (*pagination.Result[model.Notice], error) {
	q := r.DB.WithContext(ctx).Model(&model.Notice{}).
		Where("sys_notice.to_user = ?", f.ToUser)

	q = pagination.Eq(q, "sys_notice.object_type", f.ObjectType)
	q = pagination.Eq(q, "sys_notice.object_id", f.ObjectID)
	if f.Unread != nil {
		// 字段说的是「要不要只看未读」，不是「read 列该等于什么」，
		// 所以这里必须取反：Unread=true → read = false。
		// 写反了不会报错，只会让「只看未读」列出已读、反之亦然。
		q = q.Where("sys_notice.read = ?", !*f.Unread)
	}
	q = pagination.Keyword(q, f.Keywords, "sys_notice.title", "sys_notice.content")

	return r.Paginate(ctx, q, f.Pagination, noticeSorter, "sys_notice.id DESC")
}

// CountUnread 统计某人的未读通知数（前端红点用）。
func (r *NoticeRepo) CountUnread(ctx context.Context, userID uint) (int64, error) {
	return r.Count(ctx, "to_user = ? AND read = ?", userID, false)
}

// UpsertUnread 写入一条通知：同一个对象上已有的未读通知会被覆盖而不是追加。
//
// 这是刻意的取舍 —— 通知在这里是「待办」而不是「流水」：
// 一条缺陷被反复指派五次，收件人需要知道的是「现在要处理它」，
// 而不是五条一模一样的提醒。想看重放有操作历史那条线。
func (r *NoticeRepo) UpsertUnread(ctx context.Context, tx *gorm.DB, n *model.Notice) error {
	handle := r.db(tx).WithContext(ctx)

	var existing model.Notice
	err := handle.Model(&model.Notice{}).
		Where("to_user = ? AND object_type = ? AND object_id = ? AND read = ?",
			n.ToUser, n.ObjectType, n.ObjectID, false).
		Order("id DESC").Limit(1).Take(&existing).Error

	switch {
	case err == nil:
		// 已有未读：改写它，并把它的时间提前到最前（列表按时间倒序）。
		return r.UpdateFields(ctx, tx, existing.ID, map[string]any{
			"from_user":  n.FromUser,
			"title":      n.Title,
			"content":    n.Content,
			"link":       n.Link,
			"created_at": n.CreatedAt,
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		return r.Create(ctx, tx, n)
	default:
		return translate(err)
	}
}

// MarkRead 把某人的若干条通知标记为已读，返回受影响的条数。
//
// 条件里的 to_user 不能省：否则任何人拿到 id 就能把别人的通知标记已读。
func (r *NoticeRepo) MarkRead(ctx context.Context, tx *gorm.DB, userID uint, ids []uint, at time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.Notice{}).
		Where("to_user = ? AND id IN ? AND read = ?", userID, ids, false).
		Updates(map[string]any{"read": true, "read_at": at})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}

// MarkAllRead 把某人的全部未读标记为已读。
func (r *NoticeRepo) MarkAllRead(ctx context.Context, tx *gorm.DB, userID uint, at time.Time) (int64, error) {
	res := r.db(tx).WithContext(ctx).Model(&model.Notice{}).
		Where("to_user = ? AND read = ?", userID, false).
		Updates(map[string]any{"read": true, "read_at": at})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return res.RowsAffected, nil
}

// ListUnread 取某人的最近若干条未读通知，供工作台首屏。
func (r *NoticeRepo) ListUnread(ctx context.Context, userID uint, limit int) ([]model.Notice, error) {
	q := r.DB.WithContext(ctx).Model(&model.Notice{}).
		Where("to_user = ? AND read = ?", userID, false).
		Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.Notice
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// DeleteByObject 删除某对象上的通知。
//
// 对象被删除时调用：一条指向不存在缺陷的通知点开只会报 404。
func (r *NoticeRepo) DeleteByObject(ctx context.Context, tx *gorm.DB, objectType string, objectID uint) (int64, error) {
	return r.DeleteWhere(ctx, tx, "object_type = ? AND object_id = ?", objectType, objectID)
}
