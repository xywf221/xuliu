package service

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/repository"
)

// ModuleService 负责产品模块树的维护。
//
// 同一产品下按用途分多棵树（需求树、任务树、缺陷树），由 type 区分。
// 树用「物化路径」表达：path 存从根到自身的 id 逗号串（**含自身**），
// grade 是深度。这样的好处是「取子树」只需一次 LIKE 前缀匹配，
// 三种数据库行为完全一致，不依赖递归 CTE。
type ModuleService struct {
	db       *gorm.DB
	modules  *repository.ModuleRepo
	products *repository.ProductRepo
	stories  *repository.StoryRepo
	tasks    *repository.TaskRepo
	bugs     *repository.BugRepo
	cases    *repository.TestCaseRepo
	gate     *Gate
}

// NewModuleService 构造模块服务。
func NewModuleService(
	db *gorm.DB,
	modules *repository.ModuleRepo,
	products *repository.ProductRepo,
	stories *repository.StoryRepo,
	tasks *repository.TaskRepo,
	bugs *repository.BugRepo,
	cases *repository.TestCaseRepo,
	gate *Gate,
) *ModuleService {
	return &ModuleService{
		db: db, modules: modules, products: products, stories: stories,
		tasks: tasks, bugs: bugs, cases: cases, gate: gate,
	}
}

// Tree 返回某产品下指定用途的模块树。
//
// 返回**整棵树**而不是某一层的子节点：模块树规模在几十到几百节点，
// 一次给全比让前端逐层拉取更省事，也让「按模块筛选」的下拉能直接展开。
func (s *ModuleService) Tree(ctx context.Context, root uint, moduleType string) ([]dto.ModuleResp, error) {
	if err := s.assertType(moduleType); err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, root); err != nil {
		return nil, err
	}

	modules, err := s.modules.Tree(ctx, root, moduleType)
	if err != nil {
		return nil, err
	}

	nodes := make(map[uint]dto.ModuleResp, len(modules))
	for _, m := range modules {
		nodes[m.ID] = toModuleResp(m)
	}
	return buildTree(nodes,
		func(n *dto.ModuleResp) uint { return n.ParentID },
		func(n *dto.ModuleResp, children []dto.ModuleResp) { n.Children = children },
		func(n dto.ModuleResp) (int, uint) { return n.Order, n.ID },
	), nil
}

// Get 取单个模块。
func (s *ModuleService) Get(ctx context.Context, id uint) (*dto.ModuleResp, error) {
	m, err := s.modules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, m.Root); err != nil {
		return nil, err
	}
	out := toModuleResp(*m)
	return &out, nil
}

// Create 新建模块。
func (s *ModuleService) Create(ctx context.Context, req dto.ModuleReq) (*dto.ModuleResp, error) {
	if err := s.assertType(req.Type); err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, req.Root); err != nil {
		return nil, err
	}
	if _, err := s.products.GetByID(ctx, req.Root); err != nil {
		return nil, apperr.Wrap(apperr.ProductNotFound, err)
	}

	module := &model.Module{
		Root: req.Root, Type: req.Type, ParentID: req.ParentID,
		Name: req.Name, Short: req.Short, Owner: req.Owner,
		Desc: req.Desc, Order: req.Order,
	}

	if req.ParentID == 0 {
		module.Grade = 1
	} else {
		parent, err := s.modules.FindByID(ctx, req.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, apperr.Newf(apperr.ModuleNotFound, "上级模块不存在")
		}
		// 父子必须同产品同类型，否则模块树会串到一起。
		if parent.Root != req.Root || parent.Type != req.Type {
			return nil, apperr.New(apperr.ModuleWrongRoot)
		}
		module.Grade = parent.Grade + 1
	}

	taken, err := s.modules.NameTakenInParent(ctx, req.Name, req.Root, req.Type, req.ParentID, 0)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.New(apperr.ModuleNameTaken)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.modules.Create(ctx, tx, module); err != nil {
			return err
		}
		// path 要含自身 id，只能插入后回写。
		path := fmt.Sprintf("%d", module.ID)
		if req.ParentID != 0 {
			parent, err := s.modules.FindByID(ctx, req.ParentID)
			if err != nil {
				return err
			}
			path = parent.Path + "," + path
		}
		return s.modules.UpdateFields(ctx, tx, module.ID, map[string]any{"path": path})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, module.ID)
}

// Update 编辑模块，支持移动节点。
//
// 移动是这里唯一有难度的操作：换父节点后，自己的 path/grade 要变，
// **全部子孙的也要跟着变**。做法是重新算一遍自己的路径，再把每个子孙
// 的路径前缀整体替换 —— 自根向下重写会漏掉深层节点，自底向上又依赖
// 旧路径，所以按「父路径已确定」的顺序逐层推进最稳。
func (s *ModuleService) Update(ctx context.Context, id uint, req dto.ModuleReq) (*dto.ModuleResp, error) {
	current, err := s.modules.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.gate.AssertProductVisible(ctx, current.Root); err != nil {
		return nil, err
	}
	// 不允许改变树归属：换产品/换用途等同于把子树搬到另一棵树，
	// 应当是新建成一棵，而不是原地改根。这里显式拒绝，避免出现
	// 「父子不同 root」的脏数据。
	if req.Root != current.Root || req.Type != current.Type {
		return nil, apperr.New(apperr.ModuleWrongRoot)
	}

	moving := req.ParentID != current.ParentID
	if moving {
		if err := s.assertNotDescendant(ctx, id, req.ParentID); err != nil {
			return nil, err
		}
	}

	taken, err := s.modules.NameTakenInParent(ctx, req.Name, current.Root, current.Type, req.ParentID, id)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, apperr.New(apperr.ModuleNameTaken)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fields := map[string]any{
			"name": req.Name, "short": req.Short,
			"owner": req.Owner, "desc": req.Desc, "order": req.Order,
		}
		if moving {
			fields["parent_id"] = req.ParentID
		}
		if err := s.modules.UpdateFields(ctx, tx, id, fields); err != nil {
			return err
		}
		if !moving {
			return nil
		}
		return s.rebuildSubtree(ctx, tx, id, req.ParentID)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete 删除模块。
//
// 有子模块或挂着业务对象时拒绝：模块一旦被删，挂在它下面的需求
// 就会失去分类归属，而「恢复」需要人工重挂。
func (s *ModuleService) Delete(ctx context.Context, id uint) error {
	m, err := s.modules.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.gate.AssertProductVisible(ctx, m.Root); err != nil {
		return err
	}

	hasChildren, err := s.modules.HasChildren(ctx, id)
	if err != nil {
		return err
	}
	if hasChildren {
		return apperr.New(apperr.ModuleHasChildren)
	}

	// 四棵树各有自己的实体：需求树挂需求，任务树挂任务，缺陷树挂缺陷，
	// 用例树挂用例。按 type 分别统计 —— 一个需求模块下有没有任务与
	// 该模块能否删除无关（任务的 module_id 指向的是任务树的节点，
	// 两者 id 空间不同）。
	switch m.Type {
	case model.ModuleTypeStory:
		count, err := s.stories.CountByModule(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return apperr.Newf(apperr.ModuleHasObjects,
				"该模块下还有 %d 条需求，请先转移后再删除", count)
		}
	case model.ModuleTypeTask:
		count, err := s.tasks.CountByModule(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return apperr.Newf(apperr.ModuleHasObjects,
				"该模块下还有 %d 个任务，请先转移后再删除", count)
		}
	case model.ModuleTypeBug:
		count, err := s.bugs.CountByModule(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return apperr.Newf(apperr.ModuleHasObjects,
				"该模块下还有 %d 个缺陷，请先转移后再删除", count)
		}
	case model.ModuleTypeCase:
		count, err := s.cases.CountByModule(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return apperr.Newf(apperr.ModuleHasObjects,
				"该模块下还有 %d 条用例，请先转移后再删除", count)
		}
	}

	return s.modules.Delete(ctx, nil, id)
}

// assertNotDescendant 阻止把模块挂到自己的子孙下面。
//
// 靠物化路径判断，不需要遍历整棵树：候选父节点的路径里若含有自己，
// 就说明它是自己的子孙。
func (s *ModuleService) assertNotDescendant(ctx context.Context, id, parentID uint) error {
	if parentID == 0 {
		return nil
	}
	parent, err := s.modules.FindByID(ctx, parentID)
	if err != nil {
		return err
	}
	if parent == nil {
		return apperr.Newf(apperr.ModuleNotFound, "上级模块不存在")
	}
	if parent.ID == id {
		return apperr.New(apperr.ModuleCycle)
	}
	for _, part := range strings.Split(parent.Path, ",") {
		if part == fmt.Sprint(id) {
			return apperr.New(apperr.ModuleCycle)
		}
	}
	return nil
}

// rebuildSubtree 重算某模块及其全部子孙的 path 与 grade。
//
// 逐条更新而不是拼一条 REPLACE(path, ...) 的 SQL：模块树在几十个节点量级，
// 一次移动多花几十条 UPDATE 完全可以接受，而换回来的是「不依赖各家
// 字符串函数的细节差异」——REPLACE 三库都有，但路径前缀替换这种
// 语义敏感的写法，读代码的人很难一眼确认它对所有情形都成立。
func (s *ModuleService) rebuildSubtree(ctx context.Context, tx *gorm.DB, id, parentID uint) error {
	self, err := s.modules.GetByID(ctx, id)
	if err != nil {
		return err
	}

	newPath := fmt.Sprintf("%d", id)
	newGrade := 1
	if parentID != 0 {
		parent, err := s.modules.FindByID(ctx, parentID)
		if err != nil {
			return err
		}
		if parent == nil {
			return apperr.Newf(apperr.ModuleNotFound, "上级模块不存在")
		}
		newPath = parent.Path + "," + newPath
		newGrade = parent.Grade + 1
	}

	oldPath := self.Path
	gradeDelta := newGrade - self.Grade

	// 先更新自己。
	if err := s.modules.UpdateFields(ctx, tx, id,
		map[string]any{"path": newPath, "grade": newGrade}); err != nil {
		return err
	}
	if oldPath == "" || oldPath == newPath {
		return nil
	}

	// 再重写子孙：它们的 path 一定以「旧自身路径 + 逗号」开头。
	var descendants []model.Module
	if err := tx.WithContext(ctx).
		Where("path LIKE ?", oldPath+",%").
		Find(&descendants).Error; err != nil {
		return fmt.Errorf("service: 查询子孙模块失败: %w", err)
	}

	for i := range descendants {
		d := &descendants[i]
		if d.ID == id {
			continue
		}
		rewritten := newPath + strings.TrimPrefix(d.Path, oldPath)
		if err := s.modules.UpdateFields(ctx, tx, d.ID, map[string]any{
			"path":  rewritten,
			"grade": d.Grade + gradeDelta,
		}); err != nil {
			return err
		}
	}
	return nil
}

// assertType 校验模块树类型。
func (s *ModuleService) assertType(moduleType string) error {
	switch moduleType {
	case model.ModuleTypeStory, model.ModuleTypeTask,
		model.ModuleTypeBug, model.ModuleTypeCase:
		return nil
	default:
		return apperr.Newf(apperr.InvalidParam, "不支持的模块树类型：%s", moduleType)
	}
}

// toModuleResp 把模块模型转为出参。
func toModuleResp(m model.Module) dto.ModuleResp {
	return dto.ModuleResp{
		ID: m.ID, Root: m.Root, Type: m.Type,
		Name: m.Name, Short: m.Short,
		ParentID: m.ParentID, Path: m.Path, Grade: m.Grade,
		Order: m.Order, Owner: m.Owner, Desc: m.Desc,
	}
}
