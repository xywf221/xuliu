package service_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/service"
)

// harness 是服务层测试的装配。
//
// 刻意用真实的 sqlite 文件库 + 真实的 AutoMigrate + 真实的 acl.Enforcer，
// 而不是各层假实现：服务层最容易出错的地方恰恰是「跨表读写的组合行为」
// （版本号、物化路径、ACL 过滤），把这些换掉之后测试就只剩自说自话。
// 唯一被替换的是「当前登录用户」—— 它由 context 注入，本来就该可替换。
type harness struct {
	t   *testing.T
	db  *gorm.DB
	ctx context.Context

	enforcer *acl.Enforcer
	gate     *service.Gate

	products   *service.ProductService
	lines      *service.ProductlineService
	modules    *service.ModuleService
	plans      *service.PlanService
	stories    *service.StoryService
	comments   *service.CommentService
	actions    *service.ActionService
	projects   *service.ProjectService
	executions *service.ExecutionService
	tasks      *service.TaskService
	bugs       *service.BugService

	notices   *service.NoticeService
	workbench *service.WorkbenchService

	noticeRepo *repository.NoticeRepo
	users      *repository.UserRepo
}

// actorIDs 是本测试里固定使用的三个用户。
const (
	poUser    = 1 // 产品经理，拥有产品
	reviewer  = 2 // 评审人 / 研发
	outsider  = 3 // 与私有产品无关的人
	storyUser = 9 // 通用
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	gdb, err := db.Open(db.Config{
		Driver:   db.SQLite,
		DSN:      filepath.Join(t.TempDir(), "service.db"),
		LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		// Windows 下不关连接池的话，t.TempDir 的清理会因文件被占用而失败。
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(ctx, gdb, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	userRepo := repository.NewUserRepo(gdb)
	productlineRepo := repository.NewProductlineRepo(gdb)
	productRepo := repository.NewProductRepo(gdb)
	moduleRepo := repository.NewModuleRepo(gdb)
	planRepo := repository.NewPlanRepo(gdb)
	storyRepo := repository.NewStoryRepo(gdb)
	specRepo := repository.NewStorySpecRepo(gdb)
	reviewRepo := repository.NewStoryReviewRepo(gdb)
	linkRepo := repository.NewProjectStoryRepo(gdb)
	commentRepo := repository.NewCommentRepo(gdb)
	fileRepo := repository.NewFileRepo(gdb)
	actionRepo := repository.NewActionRepo(gdb)

	projectRepo := repository.NewProjectRepo(gdb)
	executionRepo := repository.NewExecutionRepo(gdb)
	projectProductRepo := repository.NewProjectProductRepo(gdb)
	teamRepo := repository.NewTeamRepo(gdb)
	taskRepo := repository.NewTaskRepo(gdb)
	taskTeamRepo := repository.NewTaskTeamRepo(gdb)
	effortRepo := repository.NewEffortRepo(gdb)
	bugRepo := repository.NewBugRepo(gdb)
	noticeRepo := repository.NewNoticeRepo(gdb)

	enforcer := acl.New(gdb)
	gate := service.NewGate(enforcer, productRepo, storyRepo, planRepo, moduleRepo,
		projectRepo, executionRepo, taskRepo, bugRepo)

	// 通知用真实实现：通知是「谁该收到什么」的业务规则，
	// 换掉它就等于把 M5 最该测的那部分测没了。
	noticeSvc := service.NewNoticeService(gdb, noticeRepo, userRepo)

	projects := service.NewProjectService(gdb, projectRepo, executionRepo, taskRepo,
		productRepo, projectProductRepo, linkRepo, storyRepo, userRepo, teamRepo, gate)
	storySvc := service.NewStoryService(gdb, storyRepo, specRepo, reviewRepo, linkRepo,
		productRepo, moduleRepo, planRepo, projectRepo, executionRepo, taskRepo,
		bugRepo, userRepo, fileRepo, commentRepo, actionRepo, gate, noticeSvc)

	h := &harness{
		t: t, db: gdb, ctx: asUser(ctx, poUser, true),
		enforcer: enforcer, gate: gate,
		products: service.NewProductService(gdb, productRepo, productlineRepo,
			storyRepo, planRepo, userRepo, gate),
		lines: service.NewProductlineService(gdb, productlineRepo, productRepo, userRepo),
		modules: service.NewModuleService(gdb, moduleRepo, productRepo, storyRepo,
			taskRepo, bugRepo, gate),
		plans:    service.NewPlanService(gdb, planRepo, productRepo, storyRepo, gate),
		stories:  storySvc,
		comments: service.NewCommentService(gdb, commentRepo, fileRepo, userRepo, gate),
		actions:  service.NewActionService(gdb, actionRepo, userRepo, fileRepo, gate),
		projects: projects,
		executions: service.NewExecutionService(gdb, executionRepo, projectRepo, taskRepo,
			linkRepo, userRepo, teamRepo, gate),
		tasks: service.NewTaskService(gdb, taskRepo, taskTeamRepo, effortRepo,
			storyRepo, productRepo, moduleRepo, executionRepo, userRepo, fileRepo,
			commentRepo, actionRepo, teamRepo, projects, gate, noticeSvc),
		bugs: service.NewBugService(gdb, bugRepo, productRepo, moduleRepo, planRepo,
			projectRepo, executionRepo, taskRepo, storyRepo, userRepo,
			fileRepo, commentRepo, storySvc, gate, noticeSvc),
		notices: noticeSvc,
		workbench: service.NewWorkbenchService(taskRepo, bugRepo, storyRepo, reviewRepo,
			effortRepo, noticeRepo, userRepo, productRepo, projectRepo, executionRepo, gate),
		noticeRepo: noticeRepo,
		users:      userRepo,
	}

	h.seedUsers()
	return h
}

// asUser 把某个用户放进 context。
//
// isSuper=true 时跳过权限点校验（与生产一致），便于测试只关注业务逻辑；
// 真正要验证权限时用 isSuper=false 并给出具体权限点。
func asUser(ctx context.Context, id uint, isSuper bool, rights ...string) context.Context {
	set := make(map[string]bool, len(rights))
	for _, r := range rights {
		set[r] = true
	}
	return identity.WithUser(ctx, &identity.User{
		ID: id, Account: "u" + itoa(id), Realname: "用户" + itoa(id),
		IsSuper: isSuper, Rights: set,
	})
}

// seedUsers 写入测试用的固定用户。
func (h *harness) seedUsers() {
	h.t.Helper()
	for _, u := range []model.User{
		{Account: "po", Realname: "产品经理", Password: "x"},
		{Account: "dev", Realname: "研发", Password: "x"},
		{Account: "other", Realname: "路人", Password: "x"},
	} {
		user := u
		if err := h.db.Create(&user).Error; err != nil {
			h.t.Fatalf("写入测试用户失败: %v", err)
		}
	}
	if err := h.db.Model(&model.Company{}).Create(&model.Company{
		Name: "序流科技", Admins: "po",
	}).Error; err != nil {
		h.t.Fatalf("写入公司失败: %v", err)
	}
}

// createProduct 建一个测试产品。
func (h *harness) createProduct(name, code, aclMode string, reviewers []uint) uint {
	h.t.Helper()
	product, err := h.products.Create(h.ctx, productReq(name, code, aclMode, reviewers))
	if err != nil {
		h.t.Fatalf("建产品失败: %v", err)
	}
	return product.ID
}

// createStory 建一条测试需求。
func (h *harness) createStory(productID uint, title string, reviewers []uint) uint {
	h.t.Helper()
	story, err := h.stories.Create(h.ctx, storyReq(productID, title, reviewers, "描述 "+title, "验收 "+title))
	if err != nil {
		h.t.Fatalf("建需求失败: %v", err)
	}
	return story.ID
}

// specCount 返回某需求的版本快照条数。
func (h *harness) specCount(storyID uint) int64 {
	h.t.Helper()
	var count int64
	if err := h.db.Model(&model.StorySpec{}).Where("story_id = ?", storyID).
		Count(&count).Error; err != nil {
		h.t.Fatalf("统计版本快照失败: %v", err)
	}
	return count
}

// actionCount 返回某对象某动作在操作历史里的条数。
func (h *harness) actionCount(objectType string, objectID uint, action string) int64 {
	h.t.Helper()
	var count int64
	q := h.db.Model(&model.Action{}).Where("object_type = ? AND object_id = ?", objectType, objectID)
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if err := q.Count(&count).Error; err != nil {
		h.t.Fatalf("统计操作历史失败: %v", err)
	}
	return count
}

// itoa 是一个不引入 strconv 的整数转字符串（仅用于测试命名）。
func itoa(v uint) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
