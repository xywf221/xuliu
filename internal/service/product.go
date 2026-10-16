package service

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// ProductlineService 负责产品线树的维护。
type ProductlineService struct {
	db           *gorm.DB
	productlines *repository.ProductlineRepo
	products     *repository.ProductRepo
	users        *repository.UserRepo
}

// NewProductlineService 构造产品线服务。
func NewProductlineService(
	db *gorm.DB,
	productlines *repository.ProductlineRepo,
	products *repository.ProductRepo,
	users *repository.UserRepo,
) *ProductlineService {
	return &ProductlineService{db: db, productlines: productlines, products: products, users: users}
}

// Tree 返回产品线树。
func (s *ProductlineService) Tree(ctx context.Context) ([]dto.ProductlineResp, error) {
	lines, err := s.productlines.Tree(ctx)
	if err != nil {
		return nil, err
	}

	nodes := make(map[uint]dto.ProductlineResp, len(lines))
	for _, l := range lines {
		nodes[l.ID] = dto.ProductlineResp{
			ID: l.ID, Name: l.Name, ParentID: l.ParentID,
			Path: l.Path, Grade: l.Grade, Order: l.Order,
			PO: l.PO, QD: l.QD, RD: l.RD,
		}
	}
	return buildTree(nodes,
		func(n *dto.ProductlineResp) uint { return n.ParentID },
		func(n *dto.ProductlineResp, children []dto.ProductlineResp) { n.Children = children },
		func(n dto.ProductlineResp) (int, uint) { return n.Order, n.ID },
	), nil
}

// Create 新建产品线。
func (s *ProductlineService) Create(ctx context.Context, req dto.ProductlineReq) (*dto.ProductlineResp, error) {
	line := &model.Productline{
		Name: req.Name, ParentID: req.ParentID,
		PO: req.PO, QD: req.QD, RD: req.RD, Order: req.Order,
	}

	if req.ParentID == 0 {
		line.Grade = 1
	} else {
		parent, err := s.productlines.FindByID(ctx, req.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, apperr.Newf(apperr.ProductlineNotFound, "上级产品线不存在")
		}
		line.Grade = parent.Grade + 1
	}

	taken, err := s.productlines.NameTakenInParent(ctx, req.Name, req.ParentID, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.Newf(apperr.Conflict, "同级下已存在同名产品线")
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.productlines.Create(ctx, tx, line); err != nil {
			return err
		}
		// 物化路径要带上自己的 id，只能插入后回写。
		path := fmt.Sprintf("%d", line.ID)
		if req.ParentID != 0 {
			parent, err := s.productlines.FindByID(ctx, req.ParentID)
			if err != nil {
				return err
			}
			path = parent.Path + "," + path
		}
		return s.productlines.UpdateFields(ctx, tx, line.ID, map[string]any{"path": path})
	})
	if err != nil {
		return nil, err
	}
	return s.getByID(ctx, line.ID)
}

// Update 编辑产品线。
func (s *ProductlineService) Update(ctx context.Context, id uint, req dto.ProductlineReq) (*dto.ProductlineResp, error) {
	current, err := s.productlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	taken, err := s.productlines.NameTakenInParent(ctx, req.Name, current.ParentID, id)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.Newf(apperr.Conflict, "同级下已存在同名产品线")
	}

	fields := map[string]any{
		"name":  req.Name,
		"po":    req.PO,
		"qd":    req.QD,
		"rd":    req.RD,
		"order": req.Order,
	}
	// 不允许通过编辑改父节点：那需要连同子孙的 path/grade 一起重算，
	// 是一个独立的「移动」操作。留到需要时再加，避免留下半截一致性。
	if err := s.productlines.UpdateFields(ctx, nil, id, fields); err != nil {
		return nil, err
	}
	return s.getByID(ctx, id)
}

// Delete 删除产品线。
func (s *ProductlineService) Delete(ctx context.Context, id uint) error {
	if _, err := s.productlines.GetByID(ctx, id); err != nil {
		return err
	}

	hasChildren, err := s.productlines.HasChildren(ctx, id)
	if err != nil {
		return err
	}
	if hasChildren {
		return apperr.New(apperr.ProductlineHasProducts)
	}
	hasProducts, err := s.productlines.HasProducts(ctx, id)
	if err != nil {
		return err
	}
	if hasProducts {
		return apperr.New(apperr.ProductlineHasProducts)
	}
	return s.productlines.Delete(ctx, nil, id)
}

// getByID 取单个产品线（不带子树）。
func (s *ProductlineService) getByID(ctx context.Context, id uint) (*dto.ProductlineResp, error) {
	line, err := s.productlines.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &dto.ProductlineResp{
		ID: line.ID, Name: line.Name, ParentID: line.ParentID,
		Path: line.Path, Grade: line.Grade, Order: line.Order,
		PO: line.PO, QD: line.QD, RD: line.RD,
	}, nil
}

// ---------------------------------------------------------------------------
// 产品
// ---------------------------------------------------------------------------

// ProductService 负责产品的增删改查。
type ProductService struct {
	db       *gorm.DB
	products *repository.ProductRepo
	lines    *repository.ProductlineRepo
	stories  *repository.StoryRepo
	plans    *repository.PlanRepo
	users    *repository.UserRepo
	gate     *Gate
}

// NewProductService 构造产品服务。
func NewProductService(
	db *gorm.DB,
	products *repository.ProductRepo,
	lines *repository.ProductlineRepo,
	stories *repository.StoryRepo,
	plans *repository.PlanRepo,
	users *repository.UserRepo,
	gate *Gate,
) *ProductService {
	return &ProductService{
		db: db, products: products, lines: lines,
		stories: stories, plans: plans, users: users, gate: gate,
	}
}

// List 分页查询产品。
//
// 可见性过滤在仓储层用 IN 完成，因此分页计数也是准确的
// （若改成先全查再内存过滤，Total 会把不可见的产品也算进去）。
func (s *ProductService) List(ctx context.Context, req dto.ProductFilterReq) (*pagination.Result[dto.ProductResp], error) {
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.products.List(ctx, repository.ProductFilter{
		ProductlineID: req.ProductlineID,
		Status:        req.Status,
		Type:          req.Type,
		Keywords:      req.Keywords,
		VisibleIDs:    visible,
		Pagination:    pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}

	// 一次补齐产品线名与负责人姓名。
	lineNames, err := s.lineNameMap(ctx, result.List)
	if err != nil {
		return nil, err
	}
	userNames, err := s.userNames(ctx, result.List)
	if err != nil {
		return nil, err
	}

	out := make([]dto.ProductResp, 0, len(result.List))
	for i := range result.List {
		out = append(out, toProductResp(&result.List[i], lineNames, userNames))
	}

	return &pagination.Result[dto.ProductResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// Get 取产品详情（含需求数与计划数）。
func (s *ProductService) Get(ctx context.Context, id uint) (*dto.ProductResp, error) {
	product, err := s.products.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, id); err != nil {
		return nil, err
	}

	lineNames, err := s.lineNameMap(ctx, []model.Product{*product})
	if err != nil {
		return nil, err
	}
	userNames, err := s.userNames(ctx, []model.Product{*product})
	if err != nil {
		return nil, err
	}
	out := toProductResp(product, lineNames, userNames)

	// 两个统计是独立的，任一失败都不该让详情页打不开。
	if count, err := s.stories.Count(ctx, "product_id = ? AND status <> ?",
		id, model.StoryStatusClosed); err == nil {
		out.StoryCount = count
	}
	if count, err := s.plans.Count(ctx, "product_id = ?", id); err == nil {
		out.PlanCount = count
	}
	return &out, nil
}

// Create 新建产品。
func (s *ProductService) Create(ctx context.Context, req dto.ProductReq) (*dto.ProductResp, error) {
	if taken, err := s.products.NameTaken(ctx, req.Name, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProductNameTaken)
	}
	if taken, err := s.products.CodeTaken(ctx, req.Code, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProductCodeTaken)
	}
	if err := s.assertLineExists(ctx, req.ProductlineID); err != nil {
		return nil, err
	}

	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}
	reviewers, err := encodeIDs(req.Reviewer)
	if err != nil {
		return nil, err
	}

	product := &model.Product{
		Name: req.Name, Code: req.Code,
		ProductlineID: req.ProductlineID,
		Type:          defaultString(req.Type, model.ProductTypeNormal),
		Status:        defaultString(req.Status, model.ProductStatusNormal),
		Desc:          req.Desc,
		PO:            req.PO, QD: req.QD, RD: req.RD,
		ACL:         defaultString(req.ACL, model.ACLOpen),
		Whitelist:   whitelist,
		Reviewer:    reviewers,
		ReviewStory: req.ReviewStory == nil || *req.ReviewStory,
		Order:       req.Order,
	}

	if err := s.products.Create(ctx, nil, product); err != nil {
		// 并发下唯一索引兜底：名字与代号在模型上都有唯一约束。
		if repository.IsDuplicated(err) {
			return nil, apperr.New(apperr.ProductCodeTaken)
		}
		return nil, err
	}
	return s.Get(ctx, product.ID)
}

// Update 编辑产品。
func (s *ProductService) Update(ctx context.Context, id uint, req dto.ProductReq) (*dto.ProductResp, error) {
	if _, err := s.products.GetByID(ctx, id); err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, id); err != nil {
		return nil, err
	}

	if taken, err := s.products.NameTaken(ctx, req.Name, id); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProductNameTaken)
	}
	if taken, err := s.products.CodeTaken(ctx, req.Code, id); err != nil {
		return nil, err
	} else if taken {
		return nil, apperr.New(apperr.ProductCodeTaken)
	}
	if err := s.assertLineExists(ctx, req.ProductlineID); err != nil {
		return nil, err
	}

	whitelist, err := encodeIDs(req.Whitelist)
	if err != nil {
		return nil, err
	}
	reviewers, err := encodeIDs(req.Reviewer)
	if err != nil {
		return nil, err
	}

	fields := map[string]any{
		"name": req.Name, "code": req.Code,
		"productline_id": req.ProductlineID,
		"type":           defaultString(req.Type, model.ProductTypeNormal),
		"status":         defaultString(req.Status, model.ProductStatusNormal),
		"desc":           req.Desc,
		"po":             req.PO, "qd": req.QD, "rd": req.RD,
		"acl":       defaultString(req.ACL, model.ACLOpen),
		"whitelist": whitelist,
		"reviewer":  reviewers,
		"order":     req.Order,
	}
	// ReviewStory 用指针：不传表示保持原值，传 false 才是「关掉评审」。
	if req.ReviewStory != nil {
		fields["review_story"] = *req.ReviewStory
	}

	if err := s.products.UpdateFields(ctx, nil, id, fields); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除产品。
//
// 只要还有未关闭的需求就不允许删除：产品是需求的归属主体，
// 删掉产品会让那些需求变成无主数据（外键指向不存在的产品）。
func (s *ProductService) Delete(ctx context.Context, id uint) error {
	if _, err := s.products.GetByID(ctx, id); err != nil {
		return err
	}
	if err := s.gate.AssertProductVisible(ctx, id); err != nil {
		return err
	}

	count, err := s.products.CountStories(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return apperr.Newf(apperr.ProductHasStories,
			"该产品下还有 %d 条未关闭的需求，请先关闭或转移后再删除", count)
	}

	// 模块树与产品计划随产品一起删除：它们没有独立存在的意义。
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 注意用 tx 而不是 s.db：用根句柄的话，
		// 下面的删除失败时这里已经提交了，模块会被白白清掉。
		if res := tx.WithContext(ctx).Where("root = ?", id).
			Delete(&model.Module{}); res.Error != nil {
			return fmt.Errorf("service: 清理产品模块失败: %w", res.Error)
		}
		if res := tx.WithContext(ctx).Where("product_id = ?", id).
			Delete(&model.ProductPlan{}); res.Error != nil {
			return fmt.Errorf("service: 清理产品计划失败: %w", res.Error)
		}
		return s.products.Delete(ctx, tx, id)
	})
}

// Brief 返回可见产品的简要列表，供下拉与筛选栏使用。
func (s *ProductService) Brief(ctx context.Context) ([]dto.ProductBrief, error) {
	visible, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	products, err := s.products.ListBrief(ctx, visible, 500)
	if err != nil {
		return nil, err
	}
	out := make([]dto.ProductBrief, 0, len(products))
	for _, p := range products {
		out = append(out, dto.ProductBrief{ID: p.ID, Name: p.Name, Code: p.Code, ACL: p.ACL})
	}
	return out, nil
}

// assertLineExists 校验产品线存在（0 表示不属于任何产品线）。
func (s *ProductService) assertLineExists(ctx context.Context, lineID uint) error {
	if lineID == 0 {
		return nil
	}
	line, err := s.lines.FindByID(ctx, lineID)
	if err != nil {
		return err
	}
	if line == nil {
		return apperr.New(apperr.ProductlineNotFound)
	}
	return nil
}

// lineNameMap 批量取产品线名。
func (s *ProductService) lineNameMap(ctx context.Context, products []model.Product) (map[uint]string, error) {
	ids := make([]uint, 0, len(products))
	for _, p := range products {
		if p.ProductlineID > 0 {
			ids = append(ids, p.ProductlineID)
		}
	}
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}

	return s.lines.NameMap(ctx, ids)
}

// userNames 批量取三位负责人的姓名。
func (s *ProductService) userNames(ctx context.Context, products []model.Product) (map[uint]string, error) {
	ids := make([]uint, 0, len(products)*3)
	for _, p := range products {
		ids = append(ids, p.PO, p.QD, p.RD)
	}
	if len(ids) == 0 {
		return map[uint]string{}, nil
	}
	return s.users.NameMap(ctx, ids)
}

// toProductResp 把产品模型转为出参。
func toProductResp(p *model.Product, lineNames, userNames map[uint]string) dto.ProductResp {
	whitelist, _ := decodeIDs(p.Whitelist)
	reviewer, _ := decodeIDs(p.Reviewer)

	return dto.ProductResp{
		ID: p.ID, Name: p.Name, Code: p.Code,
		ProductlineID: p.ProductlineID,
		ProductlineNm: lineNames[p.ProductlineID],
		Type:          p.Type, Status: p.Status, Desc: p.Desc,
		PO: p.PO, QD: p.QD, RD: p.RD,
		POName: userNames[p.PO], QDName: userNames[p.QD], RDName: userNames[p.RD],
		ACL: p.ACL, Whitelist: whitelist, Reviewer: reviewer,
		ReviewStory: p.ReviewStory, Order: p.Order,
	}
}

// ---------------------------------------------------------------------------
// 通用辅助
// ---------------------------------------------------------------------------

// encodeIDs 把 id 列表编码为 JSON 数组字符串；空列表存空串。
//
// 空值存空串而不是 "[]"：模型注释里承诺「空表示无」，存 "[]" 会让
// decodeIDs 的结果在「没有」与「空数组」之间产生无意义的区别。
func encodeIDs(ids []uint) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	clean := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return "", nil
	}
	out, err := json.Marshal(clean)
	if err != nil {
		return "", fmt.Errorf("service: 序列化 id 列表失败: %w", err)
	}
	return string(out), nil
}

// decodeIDs 解析 JSON 数组字符串；空串或脏数据返回 nil。
//
// 脏数据静默忽略：白名单字段解析失败时应当「谁都不放行」，
// 而不是让整个产品详情页报错。
func decodeIDs(raw string) ([]uint, error) {
	if raw == "" {
		return nil, nil
	}
	var out []uint
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, nil
	}
	return out, nil
}

// buildTree 把扁平节点组装成树。
//
// 用泛型是因为产品线、模块、部门三棵树的结构完全一样，
// 只有类型不同。三个 getter/setter 形参看着笨，但比反射清晰得多，
// 也不会在字段改名时静默失效。
func buildTree[T any](
	nodes map[uint]T,
	parentOf func(*T) uint,
	setChildren func(*T, []T),
	sortKey func(T) (int, uint),
) []T {
	// 记录每个父节点下的子节点 id。
	index := make(map[uint][]uint, len(nodes))
	roots := make([]uint, 0)
	for id := range nodes {
		node := nodes[id]
		pid := parentOf(&node)
		if pid == 0 {
			roots = append(roots, id)
			continue
		}
		if _, ok := nodes[pid]; !ok {
			// 父节点不在本次结果集里（如权限过滤掉了），当成根节点，
			// 否则这些节点会整棵消失。
			roots = append(roots, id)
			continue
		}
		index[pid] = append(index[pid], id)
	}

	sortIDs := func(ids []uint) {
		sort.Slice(ids, func(i, j int) bool {
			ao, ai := sortKey(nodes[ids[i]])
			bo, bi := sortKey(nodes[ids[j]])
			if ao != bo {
				return ao < bo
			}
			return ai < bi
		})
	}
	sortIDs(roots)
	for pid := range index {
		sortIDs(index[pid])
	}

	// 自底向上展开。用显式递归，深度受树高限制（实际不超过 10 层）。
	var expand func(id uint) T
	expand = func(id uint) T {
		node := nodes[id]
		childIDs := index[id]
		if len(childIDs) == 0 {
			setChildren(&node, nil)
			return node
		}
		children := make([]T, 0, len(childIDs))
		for _, cid := range childIDs {
			children = append(children, expand(cid))
		}
		setChildren(&node, children)
		return node
	}

	out := make([]T, 0, len(roots))
	for _, id := range roots {
		out = append(out, expand(id))
	}
	return out
}
