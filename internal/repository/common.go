package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 通用仓储：评论、附件、操作历史。
//
// 这三类都挂在「任意业务对象」上（多态双列 object_type + object_id），
// 因此不归任何单一领域，集中在本文件。

var commentSorter = pagination.Sorter{
	"id":   "sys_comment.id",
	"date": "sys_comment.created_at",
}

var actionSorter = pagination.Sorter{
	"id":   "sys_action.id",
	"date": "sys_action.date",
}

// ---------------------------------------------------------------------------
// 评论
// ---------------------------------------------------------------------------

// CommentRepo 是评论仓储。
type CommentRepo struct {
	*Repo[model.Comment]
}

// NewCommentRepo 构造评论仓储。
func NewCommentRepo(db *gorm.DB) *CommentRepo {
	return &CommentRepo{Repo: New[model.Comment](db)}
}

// ListByObject 分页查询某对象下的评论（按时间倒序）。
func (r *CommentRepo) ListByObject(ctx context.Context, objectType string, objectID uint, params pagination.Params) (*pagination.Result[model.Comment], error) {
	q := r.DB.WithContext(ctx).Model(&model.Comment{}).
		Where("object_type = ? AND object_id = ?", objectType, objectID)
	return r.Paginate(ctx, q, params, commentSorter, "sys_comment.id DESC")
}

// CountByObject 统计某对象下的评论数（列表页显示「N 条评论」）。
func (r *CommentRepo) CountByObject(ctx context.Context, objectType string, objectID uint) (int64, error) {
	return r.Count(ctx, "object_type = ? AND object_id = ?", objectType, objectID)
}

// CountByObjects 批量统计多个对象的评论数，供列表接口一次取回。
func (r *CommentRepo) CountByObjects(ctx context.Context, objectType string, objectIDs []uint) (map[uint]int64, error) {
	if len(objectIDs) == 0 {
		return map[uint]int64{}, nil
	}
	var rows []struct {
		ObjectID uint
		Total    int64
	}
	err := r.DB.WithContext(ctx).Model(&model.Comment{}).
		Select("object_id, COUNT(*) AS total").
		Where("object_type = ? AND object_id IN ?", objectType, objectIDs).
		Group("object_id").Scan(&rows).Error
	if err != nil {
		return nil, translate(err)
	}
	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.ObjectID] = row.Total
	}
	return out, nil
}

// DeleteByObject 删除某对象的全部评论，返回删除条数。
//
// 业务对象被删除时调用，避免留下指向不存在对象的孤儿评论。
func (r *CommentRepo) DeleteByObject(ctx context.Context, tx *gorm.DB, objectType string, objectID uint) (int64, error) {
	return r.DeleteWhere(ctx, tx, "object_type = ? AND object_id = ?", objectType, objectID)
}

// ---------------------------------------------------------------------------
// 附件
// ---------------------------------------------------------------------------

// FileRepo 是附件仓储。
type FileRepo struct {
	*Repo[model.File]
}

// NewFileRepo 构造附件仓储。
func NewFileRepo(db *gorm.DB) *FileRepo {
	return &FileRepo{Repo: New[model.File](db)}
}

// ListByObject 返回某对象下的全部附件。
//
// 不分页：单个业务对象的附件数通常在 10 条以内，
// 分页只会让前端多写一层翻页逻辑。
func (r *FileRepo) ListByObject(ctx context.Context, objectType string, objectID uint) ([]model.File, error) {
	var out []model.File
	if err := r.DB.WithContext(ctx).
		Where("object_type = ? AND object_id = ?", objectType, objectID).
		Order("id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByIDs 按 id 批量取附件。
func (r *FileRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.File, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.File
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// BindToObject 把附件挂到业务对象上。
//
// 三个前提都要在这里夹住：
//   - 附件必须存在（否则会挂上一个不存在的 id，日后下载才报错）；
//   - 不能改嫁：附件一旦归属某对象，就不允许再挂到别处，
//     否则会出现两个对象都能看到同一份文件的混乱状态；
//   - 重复挂到同一对象是幂等的（编辑时原样提交很常见）。
func (r *FileRepo) BindToObject(ctx context.Context, tx *gorm.DB, ids []uint, objectType string, objectID uint) error {
	wanted := dedup(ids)
	if len(wanted) == 0 {
		return nil
	}

	files, err := r.ListByIDs(ctx, wanted)
	if err != nil {
		return err
	}
	if len(files) != len(wanted) {
		return apperr.New(apperr.FileNotFound)
	}
	for _, f := range files {
		if f.ObjectID != 0 && (f.ObjectType != objectType || f.ObjectID != objectID) {
			return apperr.Newf(apperr.Conflict,
				"附件「%s」已归属其他对象，不能重复使用", f.Name)
		}
	}

	res := r.db(tx).WithContext(ctx).Model(&model.File{}).Where("id IN ?", wanted).
		Updates(map[string]any{"object_type": objectType, "object_id": objectID})
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}

// SetObject 无条件设置附件归属（内部使用，不做占用校验）。
func (r *FileRepo) SetObject(ctx context.Context, tx *gorm.DB, ids []uint, objectType string, objectID uint) error {
	if len(ids) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.File{}).Where("id IN ?", ids).
		Updates(map[string]any{"object_type": objectType, "object_id": objectID})
	if res.Error != nil {
		return translate(res.Error)
	}
	return nil
}

// IncrDownloads 累加下载次数。
func (r *FileRepo) IncrDownloads(ctx context.Context, id uint) error {
	err := r.DB.WithContext(ctx).Model(&model.File{}).Where("id = ?", id).
		Update("downloads", gorm.Expr("downloads + 1")).Error
	if err != nil {
		return translate(err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 操作历史
// ---------------------------------------------------------------------------

// ActionRepo 是操作历史仓储。
type ActionRepo struct {
	*Repo[model.Action]
}

// NewActionRepo 构造操作历史仓储。
func NewActionRepo(db *gorm.DB) *ActionRepo {
	return &ActionRepo{Repo: New[model.Action](db)}
}

// ActionFilter 是操作历史的过滤条件。
type ActionFilter struct {
	ObjectType  string
	ObjectID    uint
	ProductID   uint
	ProjectID   uint
	ExecutionID uint
	ActorID     uint
	// Action 是动词白名单，非空时只返回这些动作。
	Actions []string

	// VisibleProductIDs / VisibleProjectIDs 是当前用户可见的产品与项目。
	//
	// **空切片表示「一个都看不到」**，与其它域同一口径：动态里带着
	// 需求标题、解决说明这类业务内容，不做可见性过滤就等于把私有产品
	// 的事摊开给所有人看。
	VisibleProductIDs []uint
	VisibleProjectIDs []uint

	Pagination pagination.Params
}

// List 分页查询操作历史（按时间倒序）。
func (r *ActionRepo) List(ctx context.Context, f ActionFilter) (*pagination.Result[model.Action], error) {
	q := r.DB.WithContext(ctx).Model(&model.Action{})

	q = pagination.Eq(q, "sys_action.object_type", f.ObjectType)
	q = pagination.Eq(q, "sys_action.object_id", f.ObjectID)
	q = pagination.Eq(q, "sys_action.product_id", f.ProductID)
	q = pagination.Eq(q, "sys_action.project_id", f.ProjectID)
	q = pagination.Eq(q, "sys_action.execution_id", f.ExecutionID)
	q = pagination.Eq(q, "sys_action.actor_id", f.ActorID)
	q = pagination.In(q, "sys_action.action", f.Actions)
	q = applyActionVisibility(q, f)

	return r.Paginate(ctx, q, f.Pagination, actionSorter, "sys_action.id DESC")
}

// applyActionVisibility 按「最贴近的那一层」过滤动态。
//
// 与 Gate 同一口径：挂着项目的按项目判，只挂产品的按产品判；
// 两层都没有的（不属于任何产品/项目的对象）不拦 —— 与 Gate 对
// 这类对象的处理保持一致。
//
// 例外：已经按对象查的（objectType + objectID）不再叠加这层过滤。
// 调用方在那种查询里已经验过对象可见性，再叠一次会出现「详情页能打开、
// 却看不到它的任何动态」这种自相矛盾的界面。
func applyActionVisibility(q *gorm.DB, f ActionFilter) *gorm.DB {
	if f.ObjectType != "" && f.ObjectID > 0 {
		return q
	}
	return q.Where(
		"(sys_action.project_id > 0 AND sys_action.project_id IN ?) "+
			"OR (sys_action.project_id = 0 AND sys_action.product_id > 0 AND sys_action.product_id IN ?) "+
			"OR (sys_action.project_id = 0 AND sys_action.product_id = 0)",
		f.VisibleProjectIDs, f.VisibleProductIDs)
}

// LastStatusChange 取某对象最近一次状态变更记录。
//
// 需求的「激活」需要它：禅道的实现是从操作历史里读回关闭前的状态，
// 而不是把前状态另存一列。这里沿用同一思路 —— 状态变更动作的
// Extra 存的是 "old|new"，取最后一条即可。
//
// 判定条件用 action 动词白名单而不是「extra 里有竖线」：
// 指派动作的 extra 存的是用户 id，解决动作存的是原因，
// 都不该被误当成状态变更。
func (r *ActionRepo) LastStatusChange(ctx context.Context, objectType string, objectID uint) (*model.Action, error) {
	var out model.Action
	err := r.DB.WithContext(ctx).Model(&model.Action{}).
		Where("object_type = ? AND object_id = ? AND action IN ?",
			objectType, objectID, statusChangeActions).
		Order("id DESC").Limit(1).Take(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// CountByObject 统计某对象的动态条数。
func (r *ActionRepo) CountByObject(ctx context.Context, objectType string, objectID uint) (int64, error) {
	return r.Count(ctx, "object_type = ? AND object_id = ?", objectType, objectID)
}

// statusChangeActions 是会改变状态的动作动词。
//
// 与 workflow.Transition.LogAction 里的取值对应；这里单独列一份是为了
// 让 repository 不必反向依赖 workflow 包（那会让仓储层依赖业务规则）。
var statusChangeActions = []string{
	model.ActionClosed,
	model.ActionActivated,
	model.ActionResolved,
	model.ActionReviewed,
	model.ActionSubmitReview,
	model.ActionRecalled,
	model.ActionFinished,
	model.ActionStarted,
	model.ActionPaused,
	model.ActionRestarted,
	model.ActionCanceled,
	model.ActionConfirmed,
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// dedup 去重，保持首次出现的顺序。
func dedup(ids []uint) []uint {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
