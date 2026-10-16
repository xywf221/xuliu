package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// PlanService 负责产品计划的维护。
//
// 产品计划表达「这批需求打算在哪个版本/时间窗交付」，是需求与发布
// 之间的中间层。它没有复杂的生命周期：状态只有未开始/进行中/已完成/已关闭，
// 且状态变更都是管理员的直接意图，不需要状态机守门。
type PlanService struct {
	db       *gorm.DB
	plans    *repository.PlanRepo
	products *repository.ProductRepo
	stories  *repository.StoryRepo
	gate     *Gate
}

// NewPlanService 构造产品计划服务。
func NewPlanService(
	db *gorm.DB,
	plans *repository.PlanRepo,
	products *repository.ProductRepo,
	stories *repository.StoryRepo,
	gate *Gate,
) *PlanService {
	return &PlanService{db: db, plans: plans, products: products, stories: stories, gate: gate}
}

// List 分页查询产品计划。
func (s *PlanService) List(ctx context.Context, productID uint, status, keywords string, params pagination.Params) (*pagination.Result[dto.PlanResp], error) {
	if err := s.gate.AssertProductVisible(ctx, productID); err != nil {
		return nil, err
	}

	result, err := s.plans.List(ctx, productID, status, keywords, params)
	if err != nil {
		return nil, err
	}

	out := make([]dto.PlanResp, 0, len(result.List))
	for i := range result.List {
		out = append(out, toPlanResp(&result.List[i]))
	}
	return &pagination.Result[dto.PlanResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// Get 取计划详情（含需求数）。
func (s *PlanService) Get(ctx context.Context, id uint) (*dto.PlanResp, error) {
	plan, err := s.plans.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, plan.ProductID); err != nil {
		return nil, err
	}

	out := toPlanResp(plan)
	if count, err := s.stories.CountByPlan(ctx, id); err == nil {
		out.StoryCount = count
	}
	return &out, nil
}

// Create 新建计划。
func (s *PlanService) Create(ctx context.Context, req dto.PlanReq) (*dto.PlanResp, error) {
	if err := s.gate.AssertProductVisible(ctx, req.ProductID); err != nil {
		return nil, err
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, err
	}

	taken, err := s.plans.TitleTaken(ctx, req.ProductID, req.Title, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.Newf(apperr.Conflict, "该产品下已存在同名计划")
	}

	now := time.Now().UTC()
	plan := &model.ProductPlan{
		ProductID: req.ProductID, Title: req.Title,
		Status: defaultString(req.Status, model.PlanStatusWait),
		Desc:   req.Desc, Begin: begin, End: end,
		OpenedBy: currentUserID(ctx), OpenedDate: &now,
		Order: req.Order,
	}

	if err := s.plans.Create(ctx, nil, plan); err != nil {
		return nil, err
	}
	return s.Get(ctx, plan.ID)
}

// Update 编辑计划。
func (s *PlanService) Update(ctx context.Context, id uint, req dto.PlanReq) (*dto.PlanResp, error) {
	current, err := s.plans.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, current.ProductID); err != nil {
		return nil, err
	}

	begin, end, err := parseDateRange(req.Begin, req.End)
	if err != nil {
		return nil, err
	}

	taken, err := s.plans.TitleTaken(ctx, req.ProductID, req.Title, id)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.Newf(apperr.Conflict, "该产品下已存在同名计划")
	}

	status := defaultString(req.Status, model.PlanStatusWait)
	fields := map[string]any{
		"product_id": req.ProductID,
		"title":      req.Title,
		"status":     status,
		"desc":       req.Desc,
		"order":      req.Order,
	}
	if begin == nil {
		fields["begin"] = nil
	} else {
		fields["begin"] = *begin
	}
	if end == nil {
		fields["end"] = nil
	} else {
		fields["end"] = *end
	}
	// 完成/关闭时补上相应时间戳，历史里才能看出「什么时候做完的」。
	if status == model.PlanStatusDone && current.FinishedDate == nil {
		now := time.Now().UTC()
		fields["finished_date"] = now
	}
	if status == model.PlanStatusClosed && current.ClosedDate == nil {
		now := time.Now().UTC()
		fields["closed_date"] = now
	}

	if err := s.plans.UpdateFields(ctx, nil, id, fields); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除计划。
//
// 计划下的需求只是「解除计划归属」而不是被删掉，所以删除计划时
// 把需求上的 plan_id 清空即可 —— 需求本身另有归属（产品/模块），
// 不该因为计划被删而消失。
func (s *PlanService) Delete(ctx context.Context, id uint) error {
	plan, err := s.plans.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.gate.AssertProductVisible(ctx, plan.ProductID); err != nil {
		return err
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if res := tx.WithContext(ctx).Model(&model.Story{}).
			Where("plan_id = ?", id).Update("plan_id", 0); res.Error != nil {
			return res.Error
		}
		return s.plans.Delete(ctx, tx, id)
	})
}

// parseDateRange 解析计划的起止日期，并校验先后顺序。
//
// 两个日期都可为空（表示「不限时间」）；只有当两端都给且颠倒时才报错。
func parseDateRange(beginStr, endStr string) (begin, end *time.Time, err error) {
	const layout = "2006-01-02"
	if beginStr != "" {
		t, perr := time.ParseInLocation(layout, beginStr, time.UTC)
		if perr != nil {
			return nil, nil, apperr.Newf(apperr.PlanDateInvalid, "开始日期格式应为 YYYY-MM-DD")
		}
		begin = &t
	}
	if endStr != "" {
		t, perr := time.ParseInLocation(layout, endStr, time.UTC)
		if perr != nil {
			return nil, nil, apperr.Newf(apperr.PlanDateInvalid, "结束日期格式应为 YYYY-MM-DD")
		}
		end = &t
	}
	if begin != nil && end != nil && end.Before(*begin) {
		return nil, nil, apperr.New(apperr.PlanDateInvalid)
	}
	return begin, end, nil
}

// toPlanResp 把计划模型转为出参。
func toPlanResp(p *model.ProductPlan) dto.PlanResp {
	out := dto.PlanResp{
		ID: p.ID, ProductID: p.ProductID,
		Title: p.Title, Status: p.Status, Desc: p.Desc,
		OpenedBy: p.OpenedBy, Order: p.Order,
	}
	if p.Begin != nil {
		out.Begin = p.Begin.Format("2006-01-02")
	}
	if p.End != nil {
		out.End = p.End.Format("2006-01-02")
	}
	if p.OpenedDate != nil {
		out.OpenedAt = p.OpenedDate.UTC().Format(time.RFC3339)
	}
	return out
}
