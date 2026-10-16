package service

import (
	"context"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/repository"
)

// visibility 是数据级可见性的判定能力。
//
// 定义成接口而不是直接依赖 *acl.Enforcer：service 只用到这几个方法，
// 窄接口让「服务需要什么」一目了然，也让单测可以用几行假实现替换掉
// 真实的权限计算。acl.Enforcer 天然满足它。
type visibility interface {
	// CanAccessProduct 判定用户能否访问某产品（含私有产品的白名单判定）。
	CanAccessProduct(ctx context.Context, userID, productID uint) (bool, error)
	// CanAccessProject 判定用户能否访问某项目。
	CanAccessProject(ctx context.Context, userID, projectID uint) (bool, error)
	// CanAccessExecution 判定用户能否访问某迭代（含所属项目的可见性）。
	CanAccessExecution(ctx context.Context, userID, executionID uint) (bool, error)

	// VisibleProductIDs 返回用户可见的产品 id。
	//
	// **返回空切片表示「一个都看不到」，而不是「不过滤」** ——
	// 调用方必须无条件把它当成 IN 过滤条件应用，否则私有产品会漏出去。
	VisibleProductIDs(ctx context.Context, userID uint) ([]uint, error)
	// VisibleProjectIDs 同样：空切片表示一个都看不到。
	VisibleProjectIDs(ctx context.Context, userID uint) ([]uint, error)
}

// Location 描述一个业务对象在数据层级中的位置。
//
// 产品/项目/迭代/任务构成一条四层的包含链，而评论、附件、操作历史
// 挂在「任意业务对象」上。要判断「这个人能不能看这条评论」，就得先
// 知道评论挂的对象处在链条的哪一层 —— 这个结构就是那次解析的结果。
type Location struct {
	// ProductID/ProjectID/ExecutionID 都是 0 表示该对象不属于对应层级。
	ProductID   uint
	ProjectID   uint
	ExecutionID uint

	// Conjunctive 为 true 时，上面给出的**每一层都要通过**；
	// 为 false 时按「最贴近的那一层说了算」判定。
	//
	// 这个区分来自数据本身，不是实现口味：
	//
	//   - 缺陷是**或**的关系：一条提给私有产品的缺陷，如果是在某次迭代里
	//     验出来的，那次迭代里的人就该看得见它 —— 他们在处理它。
	//   - 任务是**与**的关系：任务在项目里，但若它属于一个我看不懂的私有产品，
	//     那它就是那个产品的内部工作，项目是公开的并不构成公开的理由。
	//
	// 判定方式也必须与列表过滤保持一致，否则会出现「列表里有、点开 403」
	// 或者更糟的「列表里没有、直接打开却看得见」。
	Conjunctive bool
}

// Gate 是按对象定位并校验可见性的入口。
//
// 评论、附件、操作历史都挂在「任意业务对象」上（多态双列），
// 因此它们无法依赖某个具体 service 来判断「这个对象这个人能不能看」。
// Gate 集中承接这件事：把 (objectType, objectID) 解析成 Location，
// 再按最贴近的那一层做 ACL 判定。
//
// 为什么不让这些接口无脑放行：注释和附件往往会带出业务内容
// （「这个需求的验收标准还得再改」已经泄露了需求存在与主题），
// 对一个看不到该对象的用户，连「存在这条记录」都不该知道。
type Gate struct {
	access     visibility
	products   *repository.ProductRepo
	stories    *repository.StoryRepo
	plans      *repository.PlanRepo
	modules    *repository.ModuleRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	tasks      *repository.TaskRepo
	bugs       *repository.BugRepo
}

// NewGate 构造访问网关。
func NewGate(
	access visibility,
	products *repository.ProductRepo,
	stories *repository.StoryRepo,
	plans *repository.PlanRepo,
	modules *repository.ModuleRepo,
	projects *repository.ProjectRepo,
	executions *repository.ExecutionRepo,
	tasks *repository.TaskRepo,
	bugs *repository.BugRepo,
) *Gate {
	return &Gate{
		access: access, products: products, stories: stories, plans: plans,
		modules: modules, projects: projects, executions: executions, tasks: tasks,
		bugs: bugs,
	}
}

// Locate 解析对象在数据层级中的位置。
func (g *Gate) Locate(ctx context.Context, objectType string, objectID uint) (Location, error) {
	switch objectType {
	case model.ObjectTypeStory:
		story, err := g.stories.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if story == nil {
			return Location{}, apperr.New(apperr.StoryNotFound)
		}
		return Location{ProductID: story.ProductID}, nil

	case model.ObjectTypeProduct:
		p, err := g.products.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if p == nil {
			return Location{}, apperr.New(apperr.ProductNotFound)
		}
		return Location{ProductID: p.ID}, nil

	case model.ObjectTypePlan:
		plan, err := g.plans.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if plan == nil {
			return Location{}, apperr.New(apperr.PlanNotFound)
		}
		return Location{ProductID: plan.ProductID}, nil

	case model.ObjectTypeModule:
		m, err := g.modules.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if m == nil {
			return Location{}, apperr.New(apperr.ModuleNotFound)
		}
		return Location{ProductID: m.Root}, nil

	case model.ObjectTypeProject:
		p, err := g.projects.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if p == nil {
			return Location{}, apperr.New(apperr.ProjectNotFound)
		}
		// 项目可以关联多个产品，所以这里没有单一的产品归属；
		// 可见性由项目自身那一层判定。
		return Location{ProjectID: p.ID}, nil

	case model.ObjectTypeExecution:
		e, err := g.executions.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if e == nil {
			return Location{}, apperr.New(apperr.ExecutionNotFound)
		}
		return Location{ProjectID: e.ProjectID, ExecutionID: e.ID}, nil

	case model.ObjectTypeTask:
		task, err := g.tasks.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if task == nil {
			return Location{}, apperr.New(apperr.TaskNotFound)
		}
		return taskLocation(task), nil

	case model.ObjectTypeBug:
		bug, err := g.bugs.FindByID(ctx, objectID)
		if err != nil {
			return Location{}, err
		}
		if bug == nil {
			return Location{}, apperr.New(apperr.BugNotFound)
		}
		// 缺陷可能只挂在产品上（测试同学直接在测试环境发现的问题），
		// 也可能落进了某个项目/迭代（在迭代里验出来的活）。三层都带上，
		// 让 assertLocation 去挑最贴近的那一层。
		return Location{
			ProductID:   bug.ProductID,
			ProjectID:   bug.ProjectID,
			ExecutionID: bug.ExecutionID,
		}, nil

	default:
		// 未知类型一律拒绝：与其「不认识就放行」，不如明确报错 ——
		// 后者能被发现并补上，前者是一个没人会注意到的越权口子。
		return Location{}, apperr.Newf(apperr.InvalidParam, "暂不支持对该类型对象操作：%s", objectType)
	}
}

// AssertVisible 校验当前用户能看到该对象。
func (g *Gate) AssertVisible(ctx context.Context, objectType string, objectID uint) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}

	loc, err := g.Locate(ctx, objectType, objectID)
	if err != nil {
		return err
	}
	return g.assertLocation(ctx, userID, loc)
}

// assertLocation 判定对象可见性。
//
// 两种口径（见 Location.Conjunctive）：
//
//   - 默认「最贴近的那一层说了算」，顺序是 迭代 → 项目 → 产品。
//     acl.Enforcer.CanAccessExecution 内部已经把「看不到项目就看不到迭代」
//     这条包含关系算进去了，所以按迭代判就等于连项目一起判过。
//   - 任务的每一层都要过：它同时挂在项目、产品（可能还有迭代）下，
//     而没有哪一层天然包含另外两层 —— 公开项目不等于公开它承载的私有产品。
func (g *Gate) assertLocation(ctx context.Context, userID uint, loc Location) error {
	if loc.Conjunctive {
		layers := []struct {
			id   uint
			can  func(context.Context, uint, uint) (bool, error)
			code apperr.Code
		}{
			{loc.ProductID, g.access.CanAccessProduct, apperr.ProductAccessDenied},
			{loc.ProjectID, g.access.CanAccessProject, apperr.ProjectAccessDenied},
			{loc.ExecutionID, g.access.CanAccessExecution, apperr.ProjectAccessDenied},
		}
		for _, layer := range layers {
			if layer.id == 0 {
				// 0 表示该对象不属于这一层：没有产品归属的任务（内部事务）
				// 就该只按项目判，而不是被一条不存在的产品挡住。
				continue
			}
			if err := g.assertAccess(layer.can, ctx, userID, layer.id, layer.code); err != nil {
				return err
			}
		}
		return nil
	}

	switch {
	case loc.ExecutionID > 0:
		return g.assertAccess(g.access.CanAccessExecution, ctx, userID, loc.ExecutionID,
			apperr.ProjectAccessDenied)
	case loc.ProjectID > 0:
		return g.assertAccess(g.access.CanAccessProject, ctx, userID, loc.ProjectID,
			apperr.ProjectAccessDenied)
	case loc.ProductID > 0:
		return g.assertAccess(g.access.CanAccessProduct, ctx, userID, loc.ProductID,
			apperr.ProductAccessDenied)
	default:
		// 不属于任何产品/项目的对象（如产品线）：没有可判定的层级。
		return nil
	}
}

// assertAccess 调用一个可见性判定函数并把 false 翻成指定错误码。
func (g *Gate) assertAccess(
	can func(context.Context, uint, uint) (bool, error),
	ctx context.Context, userID, objectID uint, code apperr.Code,
) error {
	ok, err := can(ctx, userID, objectID)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(code)
	}
	return nil
}

// VisibleProducts 返回当前用户可见的产品 id。
func (g *Gate) VisibleProducts(ctx context.Context) ([]uint, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}
	return g.access.VisibleProductIDs(ctx, userID)
}

// VisibleProjects 返回当前用户可见的项目 id。
//
// 过滤条件用的是「可见项目」，而项目可能关联私有产品：
// 一个能看项目却看不到其产品的用户，在项目页里只该看到
// 自己有权看到的那部分内容，这个交叉过滤由各自的查询负责。
func (g *Gate) VisibleProjects(ctx context.Context) ([]uint, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}
	return g.access.VisibleProjectIDs(ctx, userID)
}

// AssertProductVisible 校验当前用户能看到指定产品。
//
// 与 AssertVisible 的区别：这里已知产品 id，省掉一次「对象→产品」的解析。
func (g *Gate) AssertProductVisible(ctx context.Context, productID uint) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	return g.assertAccess(g.access.CanAccessProduct, ctx, userID, productID,
		apperr.ProductAccessDenied)
}

// taskLocation 给出任务在数据层级中的位置。
//
// 单独抽一个函数，是为了让「评论/附件按 objectType=task 走网关」与
// 「任务自己的详情页」用的是同一份判定 —— 两处各写一遍的话，
// 迟早会出现「详情打不开、它的评论却读得到」这类只在一侧生效的口子。
func taskLocation(task *model.Task) Location {
	return Location{
		ProductID:   task.ProductID,
		ProjectID:   task.ProjectID,
		ExecutionID: task.ExecutionID,
		Conjunctive: true,
	}
}

// AssertTaskVisible 判定当前用户能否看到某条任务。
//
// 任务的可见性是三层**与**的关系：项目可见 + 产品可见（未挂产品的不受
// 产品层约束）+ 迭代可见。这与 TaskFilter 里的过滤条件一一对应 ——
// 项目公开并不构成「它承载的私有产品也公开」的理由。
func (g *Gate) AssertTaskVisible(ctx context.Context, task *model.Task) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	return g.assertLocation(ctx, userID, taskLocation(task))
}

// AssertProjectVisible 校验当前用户能看到指定项目。
func (g *Gate) AssertProjectVisible(ctx context.Context, projectID uint) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	return g.assertAccess(g.access.CanAccessProject, ctx, userID, projectID,
		apperr.ProjectAccessDenied)
}

// AssertExecutionVisible 校验当前用户能看到指定迭代。
func (g *Gate) AssertExecutionVisible(ctx context.Context, executionID uint) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	return g.assertAccess(g.access.CanAccessExecution, ctx, userID, executionID,
		apperr.ProjectAccessDenied)
}
