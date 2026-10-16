package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
)

// 需求域仓储：需求主体、版本快照、评审记录，以及与项目/迭代的关联。

var storySorter = pagination.Sorter{
	"id":       "pm_story.id",
	"title":    "pm_story.title",
	"pri":      "pm_story.pri",
	"estimate": "pm_story.estimate",
	"status":   "pm_story.status",
	"stage":    "pm_story.stage",
	"opened":   "pm_story.opened_date",
	"changed":  "pm_story.changed_date",
}

// StoryRepo 是需求仓储。
type StoryRepo struct {
	*Repo[model.Story]
}

// NewStoryRepo 构造需求仓储。
func NewStoryRepo(db *gorm.DB) *StoryRepo {
	return &StoryRepo{Repo: New[model.Story](db)}
}

// StoryFilter 是需求列表的过滤条件。
type StoryFilter struct {
	ProductID uint
	PlanID    uint
	// ModuleID 为 nil 表示不过滤；传 0 表示「未分类」。
	ModuleID *uint
	Status   string
	Stage    string
	Type     string
	Category string
	Source   string
	Pri      *int
	// AssignedTo 为 nil 表示不过滤。
	AssignedTo *uint
	OpenedBy   uint
	// ParentID 为 nil 表示不过滤；传 0 表示只看顶层需求。
	ParentID *uint
	// IncludeSubModule 为 true 时连同子模块下的需求一并返回。
	IncludeSubModule bool
	Keywords         string
	// ChangedOnly 只看「已变更但未被下游确认」的需求。
	ChangedOnly bool

	// ProjectID/ExecutionID 只看排进某项目（或某迭代）的需求。
	// 关联关系在 pm_projectstory，所以要走 EXISTS 而不是等值条件。
	ProjectID   uint
	ExecutionID uint

	// ProductIDs 是可见产品范围。空切片表示一个都看不到。
	ProductIDs []uint

	Pagination pagination.Params
}

// List 分页查询需求。
func (r *StoryRepo) List(ctx context.Context, f StoryFilter) (*pagination.Result[model.Story], error) {
	q := r.DB.WithContext(ctx).Model(&model.Story{})

	q = pagination.Eq(q, "pm_story.product_id", f.ProductID)
	q = pagination.Eq(q, "pm_story.plan_id", f.PlanID)
	q = pagination.Eq(q, "pm_story.status", f.Status)
	q = pagination.Eq(q, "pm_story.stage", f.Stage)
	q = pagination.Eq(q, "pm_story.type", f.Type)
	q = pagination.Eq(q, "pm_story.category", f.Category)
	q = pagination.Eq(q, "pm_story.source", f.Source)
	q = pagination.EqPtr(q, "pm_story.pri", f.Pri)
	q = pagination.Eq(q, "pm_story.opened_by", f.OpenedBy)
	q = pagination.EqPtr(q, "pm_story.assigned_to", f.AssignedTo)
	q = pagination.EqPtr(q, "pm_story.parent_id", f.ParentID)
	q = pagination.Keyword(q, f.Keywords,
		"pm_story.title", "pm_story.keywords", "pm_story.id")

	if f.ChangedOnly {
		q = q.Where("pm_story.story_changed = ?", true)
	}

	if f.ModuleID != nil {
		q = r.applyModuleFilter(ctx, q, *f.ModuleID, f.ProductID, f.IncludeSubModule)
	}

	// 排进某项目/迭代的需求。用 EXISTS 而不是 join：
	// 需求与项目是多对多，join 会让同一条需求出现多次。
	if f.ProjectID > 0 {
		q = q.Where("EXISTS (SELECT 1 FROM "+model.ProjectStory{}.TableName()+
			" ps WHERE ps.story_id = pm_story.id AND ps.project_id = ?)", f.ProjectID)
	}
	if f.ExecutionID > 0 {
		q = q.Where("EXISTS (SELECT 1 FROM "+model.ProjectStory{}.TableName()+
			" ps WHERE ps.story_id = pm_story.id AND ps.execution_id = ?)", f.ExecutionID)
	}

	// 可见产品范围。空切片会产出 `IN (NULL)`-式的必然为空条件，
	// 这正是我们要的：不可见就是一条都查不到，而不是毫无过滤。
	q = q.Where("pm_story.product_id IN ?", f.ProductIDs)

	return r.Paginate(ctx, q, f.Pagination, storySorter,
		"pm_story.pri ASC, pm_story.id DESC")
}

// applyModuleFilter 按模块过滤，可选连子模块一起。
func (r *StoryRepo) applyModuleFilter(ctx context.Context, q *gorm.DB, moduleID, productID uint, includeSub bool) *gorm.DB {
	if moduleID == 0 {
		// 「未分类」是合法且常用的筛选条件。
		return q.Where("pm_story.module_id = 0")
	}
	if !includeSub {
		return q.Where("pm_story.module_id = ?", moduleID)
	}

	// 子模块用物化路径前缀匹配，一次查询拿到子树范围。
	sub := r.DB.Model(&model.Module{}).Select("path").
		Where("id = ? AND root = ? AND type = ?", moduleID, productID, model.ModuleTypeStory)

	descendants := r.DB.Model(&model.Module{}).Select("id").
		Where("path = (?) OR path LIKE CONCAT((?), ',%')", sub, sub)

	return q.Where("pm_story.module_id IN (?)", descendants)
}

// CountByModule 统计模块下的需求数（用于阻止删除非空模块）。
func (r *StoryRepo) CountByModule(ctx context.Context, moduleID uint) (int64, error) {
	return r.Count(ctx, "module_id = ?", moduleID)
}

// CountByPlan 统计计划下的需求数。
func (r *StoryRepo) CountByPlan(ctx context.Context, planID uint) (int64, error) {
	return r.Count(ctx, "plan_id = ?", planID)
}

// CountChildren 统计子需求数。
func (r *StoryRepo) CountChildren(ctx context.Context, storyID uint) (int64, error) {
	return r.Count(ctx, "parent_id = ?", storyID)
}

// ListByIDs 按 id 批量取需求。
func (r *StoryRepo) ListByIDs(ctx context.Context, ids []uint) ([]model.Story, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []model.Story
	if err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// TitleMap 返回 id → 标题的映射，供列表里显示「关联需求」。
func (r *StoryRepo) TitleMap(ctx context.Context, ids []uint) (map[uint]string, error) {
	stories, err := r.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]string, len(stories))
	for _, s := range stories {
		out[s.ID] = s.Title
	}
	return out, nil
}

// UpdateVersioned 带版本条件的字段更新，实现需求的乐观锁。
//
// expectVersion 是调用方读到的版本号。只有库里仍是这个版本时才写入 ——
// 两个人同时编辑同一需求时，后者会拿到 StoryVersionConflict 而不是
// 悄悄覆盖前者的修改。
//
// 与 Repo.UpdateFields 的区别：这里额外把 version 纳入 WHERE，
// 且**不**用「行数 0 就等于不存在」的推断，因为版本不匹配才是主要意图。
func (r *StoryRepo) UpdateVersioned(ctx context.Context, tx *gorm.DB, id uint, expectVersion int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db(tx).WithContext(ctx).Model(&model.Story{}).
		Where("id = ? AND version = ?", id, expectVersion).Updates(fields)
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}

	// 0 行有两种可能，必须分清：版本被别人改了（409，可重试），
	// 还是需求压根不存在（404）。
	var current struct {
		Version int
	}
	err := r.db(tx).WithContext(ctx).Model(&model.Story{}).
		Select("version").Where("id = ?", id).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperr.New(apperr.StoryNotFound)
	}
	if err != nil {
		return translate(err)
	}
	if current.Version != expectVersion {
		return apperr.New(apperr.StoryVersionConflict)
	}
	// 版本一致却没有影响行数：说明字段没变化（如 MySQL 不计「无变化行」）。
	return nil
}

// FindByIDWithSpec 取需求及其当前版本的规格。
func (r *StoryRepo) FindByIDWithSpec(ctx context.Context, id uint) (*model.Story, *model.StorySpec, error) {
	story, err := r.FindByID(ctx, id)
	if err != nil || story == nil {
		return nil, nil, err
	}
	spec, err := NewStorySpecRepo(r.DB).Find(ctx, id, story.Version)
	if err != nil {
		return nil, nil, err
	}
	return story, spec, nil
}

// ---------------------------------------------------------------------------
// 需求规格（版本快照）
// ---------------------------------------------------------------------------

// StorySpecRepo 是需求规格仓储。
type StorySpecRepo struct {
	*Repo[model.StorySpec]
}

// NewStorySpecRepo 构造需求规格仓储。
func NewStorySpecRepo(db *gorm.DB) *StorySpecRepo {
	return &StorySpecRepo{Repo: New[model.StorySpec](db)}
}

// Find 取指定版本的规格。不存在返回 (nil, nil)。
func (r *StorySpecRepo) Find(ctx context.Context, storyID uint, version int) (*model.StorySpec, error) {
	var out model.StorySpec
	err := r.DB.WithContext(ctx).
		Where("story_id = ? AND version = ?", storyID, version).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// ListVersions 返回某需求全部版本的规格，按版本倒序。
//
// 供「变更历史」抽屉使用：用户可以逐版对比需求描述。
func (r *StorySpecRepo) ListVersions(ctx context.Context, storyID uint) ([]model.StorySpec, error) {
	var out []model.StorySpec
	if err := r.DB.WithContext(ctx).
		Where("story_id = ?", storyID).
		Order("version DESC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// DeleteLatest 删除某需求的最新一条规格，返回删除的版本号。
//
// 只服务于评审的「撤销变更」（revert）：禅道的 revert 会把版本退回去，
// 最新那版内容随之作废。删除最新版而不是最新「行」，
// 是为了防止并发下误删别的内容。
func (r *StorySpecRepo) DeleteLatest(ctx context.Context, tx *gorm.DB, storyID uint, version int) (int, error) {
	res := r.db(tx).WithContext(ctx).
		Where("story_id = ? AND version = ?", storyID, version).
		Delete(&model.StorySpec{})
	if res.Error != nil {
		return 0, translate(res.Error)
	}
	return int(res.RowsAffected), nil
}

// ---------------------------------------------------------------------------
// 需求评审
// ---------------------------------------------------------------------------

// StoryReviewRepo 是需求评审仓储。
type StoryReviewRepo struct {
	*Repo[model.StoryReview]
}

// NewStoryReviewRepo 构造需求评审仓储。
func NewStoryReviewRepo(db *gorm.DB) *StoryReviewRepo {
	return &StoryReviewRepo{Repo: New[model.StoryReview](db)}
}

// ListByVersion 返回某需求某版本的全部评审记录。
func (r *StoryReviewRepo) ListByVersion(ctx context.Context, storyID uint, version int) ([]model.StoryReview, error) {
	var out []model.StoryReview
	if err := r.DB.WithContext(ctx).
		Where("story_id = ? AND version = ?", storyID, version).
		Order("id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// Replace 覆盖写入某需求某版本的评审人列表。
//
// 提交评审时用来「按当前评审人重排」：已评审的结论要清掉，
// 因为换了评审人、版本也变了，旧结论不再适用。
func (r *StoryReviewRepo) Replace(ctx context.Context, tx *gorm.DB, storyID uint, version int, reviewerIDs []uint) error {
	handle := r.db(tx)

	if err := handle.WithContext(ctx).
		Where("story_id = ? AND version = ?", storyID, version).
		Delete(&model.StoryReview{}).Error; err != nil {
		return translate(err)
	}
	if len(reviewerIDs) == 0 {
		return nil
	}

	rows := make([]model.StoryReview, 0, len(reviewerIDs))
	seen := make(map[uint]struct{}, len(reviewerIDs))
	for _, uid := range reviewerIDs {
		if uid == 0 {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		rows = append(rows, model.StoryReview{StoryID: storyID, Version: version, Reviewer: uid})
	}
	if len(rows) == 0 {
		return nil
	}
	if err := handle.WithContext(ctx).Create(&rows).Error; err != nil {
		return translate(err)
	}
	return nil
}

// MarkResult 记录某位评审人对某版本的结论。
//
// 用条件更新而不是先查后写：评审是并发动作（多位评审人同时看），
// 先查后写会互相覆盖。更新不到行时说明该评审人不在名单里。
func (r *StoryReviewRepo) MarkResult(ctx context.Context, tx *gorm.DB, storyID uint, version int, reviewer uint, result string, at any) error {
	res := r.db(tx).WithContext(ctx).Model(&model.StoryReview{}).
		Where("story_id = ? AND version = ? AND reviewer = ?", storyID, version, reviewer).
		Updates(map[string]any{"result": result, "review_date": at})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		// 确认一下是真不在名单里，还是结论与旧值相同（MySQL 不算「无变化行」）。
		ok, err := r.Exists(ctx, "story_id = ? AND version = ? AND reviewer = ?",
			storyID, version, reviewer)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.New(apperr.StoryReviewNotFound)
		}
	}
	return nil
}

// ListPendingByReviewer 返回待某人评审的需求 id。
//
// 供工作台的「待我评审」用：只要有一条记录的 result 为空，就算待评审。
func (r *StoryReviewRepo) ListPendingByReviewer(ctx context.Context, reviewer uint, limit int) ([]model.StoryReview, error) {
	q := r.DB.WithContext(ctx).Model(&model.StoryReview{}).
		Where("reviewer = ? AND (result IS NULL OR result = '')", reviewer).
		Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []model.StoryReview
	if err := q.Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 需求 ↔ 项目/迭代
// ---------------------------------------------------------------------------

// ProjectStoryRepo 是需求与项目/迭代的关联仓储。
type ProjectStoryRepo struct {
	*Repo[model.ProjectStory]
}

// NewProjectStoryRepo 构造关联仓储。
func NewProjectStoryRepo(db *gorm.DB) *ProjectStoryRepo {
	return &ProjectStoryRepo{Repo: New[model.ProjectStory](db)}
}

// ListByProject 返回项目下的全部需求关联。
func (r *ProjectStoryRepo) ListByProject(ctx context.Context, projectID uint) ([]model.ProjectStory, error) {
	var out []model.ProjectStory
	if err := r.DB.WithContext(ctx).
		Where("project_id = ?", projectID).
		Order(orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByExecution 返回迭代下的全部需求关联。
func (r *ProjectStoryRepo) ListByExecution(ctx context.Context, executionID uint) ([]model.ProjectStory, error) {
	var out []model.ProjectStory
	if err := r.DB.WithContext(ctx).
		Where("execution_id = ?", executionID).
		Order(orderColumn(r.DB) + " ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListByStories 批量返回多条需求的项目关联。
//
// 供需求列表一次取回「已排入哪些项目」，避免逐行查询。
func (r *ProjectStoryRepo) ListByStories(ctx context.Context, storyIDs []uint) ([]model.ProjectStory, error) {
	if len(storyIDs) == 0 {
		return nil, nil
	}
	var out []model.ProjectStory
	if err := r.DB.WithContext(ctx).
		Where("story_id IN ?", storyIDs).
		Order("project_id ASC, id ASC").Find(&out).Error; err != nil {
		return nil, translate(err)
	}
	return out, nil
}

// ListStoryIDs 返回排进某项目/迭代的需求 id。
func (r *ProjectStoryRepo) ListStoryIDs(ctx context.Context, projectID, executionID uint) ([]uint, error) {
	q := r.DB.WithContext(ctx).Model(&model.ProjectStory{})
	q = pagination.Eq(q, "project_id", projectID)
	q = pagination.Eq(q, "execution_id", executionID)
	var ids []uint
	if err := q.Order(orderColumn(r.DB)+" ASC, id ASC").
		Pluck("story_id", &ids).Error; err != nil {
		return nil, translate(err)
	}
	return ids, nil
}

// CountByProject 统计项目下排入了多少需求。
func (r *ProjectStoryRepo) CountByProject(ctx context.Context, projectID uint) (int64, error) {
	return r.Count(ctx, "project_id = ?", projectID)
}

// CountByExecution 统计迭代下排入了多少需求。
func (r *ProjectStoryRepo) CountByExecution(ctx context.Context, executionID uint) (int64, error) {
	return r.Count(ctx, "execution_id = ?", executionID)
}

// Find 取一条关联。不存在返回 (nil, nil)。
func (r *ProjectStoryRepo) Find(ctx context.Context, projectID, storyID uint) (*model.ProjectStory, error) {
	var out model.ProjectStory
	err := r.DB.WithContext(ctx).
		Where("project_id = ? AND story_id = ?", projectID, storyID).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, translate(err)
	}
	return &out, nil
}

// CountByStory 统计需求被多少个项目引用。
func (r *ProjectStoryRepo) CountByStory(ctx context.Context, storyID uint) (int64, error) {
	return r.Count(ctx, "story_id = ?", storyID)
}

// Link 把需求排进项目/迭代；已存在则更新迭代归属。
//
// 用「先查后写」而不是 upsert：三库的 upsert 语法各不相同
// （ON CONFLICT / ON DUPLICATE KEY），而这里的并发压力可以忽略 ——
// 排需求是人工动作，不是高频接口。
func (r *ProjectStoryRepo) Link(ctx context.Context, tx *gorm.DB, link model.ProjectStory) error {
	existing, err := r.Find(ctx, link.ProjectID, link.StoryID)
	if err != nil {
		return err
	}

	handle := r.db(tx).WithContext(ctx)
	if existing == nil {
		if err := handle.Create(&link).Error; err != nil {
			return translate(err)
		}
		return nil
	}

	fields := map[string]any{
		"execution_id": link.ExecutionID,
		"version":      link.Version,
		"order":        link.Order,
	}
	// 显式带上主键的更新，避免把 id=0 也写进去。
	if err := handle.Model(&model.ProjectStory{}).
		Where("id = ?", existing.ID).Updates(fields).Error; err != nil {
		return translate(err)
	}
	return nil
}

// Unlink 解除需求与项目的关联。
func (r *ProjectStoryRepo) Unlink(ctx context.Context, tx *gorm.DB, projectID, storyID uint) error {
	res := r.db(tx).WithContext(ctx).
		Where("project_id = ? AND story_id = ?", projectID, storyID).
		Delete(&model.ProjectStory{})
	if res.Error != nil {
		return translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return apperr.Newf(apperr.NotFound, "该需求未排入此项目")
	}
	return nil
}
