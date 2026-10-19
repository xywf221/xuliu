// Package bootstrap 负责把各层组装起来并返回一个可运行的 HTTP 服务。
//
// 组装集中在这里而不是分散在各包的 init 里：依赖关系只在启动时确定一次，
// 集中一处能看到完整的装配图，测试也更容易替换其中某个部件。
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/acl"
	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/handler"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/router"
	"github.com/xywf221/xuliu/internal/service"
	"github.com/xywf221/xuliu/internal/storage"
)

// App 是组装完成的应用。
type App struct {
	Config *config.Config
	DB     *gorm.DB
	Engine *gin.Engine

	// Storage 是附件存储后端。未启用附件时为 nil。
	Storage storage.Storage

	// closers 记录需要在退出时释放的资源。
	closers []func() error
}

// Close 释放应用持有的资源。
//
// 按注册的逆序关闭：后建立的依赖先释放。
func (a *App) Close() error {
	var firstErr error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// New 组装应用：建连接、跑迁移与种子、装配各层、注册路由。
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*App, error) {
	app := &App{Config: cfg}

	// ---- 数据库 ----
	gdb, err := db.Open(db.Config{
		Driver:          db.Driver(cfg.Database.Driver),
		DSN:             cfg.Database.DSN,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		ConnMaxIdleTime: cfg.Database.ConnMaxIdleTime,
		LogLevel:        cfg.Database.LogLevel,
	})
	if err != nil {
		return nil, err
	}
	app.DB = gdb
	app.closers = append(app.closers, func() error {
		sqlDB, err := gdb.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	})
	logger.Info("数据库已连接", slog.String("驱动", cfg.Database.Driver))

	// ---- 迁移与种子 ----
	if cfg.Database.AutoMigrate {
		if err := db.AutoMigrate(ctx, gdb, logger); err != nil {
			return nil, err
		}
	} else {
		logger.Info("已跳过自动迁移（database.autoMigrate=false）")
	}

	if cfg.Database.Seed {
		if err := Seed(ctx, gdb, cfg, logger); err != nil {
			return nil, err
		}
	}

	// ---- 附件存储 ----
	// 配置留一份给 FileService：上传时的「大小上限 + 扩展名白名单」校验
	// 要用它，而这份判断必须与真正落盘的后端同源，不能各写一份。
	storageCfg := storage.Config{
		Backend:           cfg.Storage.Backend,
		LocalDir:          cfg.Storage.Local.Dir,
		MaxSize:           cfg.Storage.Local.MaxSize,
		AllowedExtensions: cfg.Storage.Local.AllowedExtensions,
		S3: storage.S3Config{
			Endpoint:      cfg.Storage.S3.Endpoint,
			Region:        cfg.Storage.S3.Region,
			Bucket:        cfg.Storage.S3.Bucket,
			AccessKey:     cfg.Storage.S3.AccessKey,
			SecretKey:     cfg.Storage.S3.SecretKey,
			UseSSL:        cfg.Storage.S3.UseSSL,
			PublicBaseURL: cfg.Storage.S3.PublicBaseURL,
		},
	}
	if cfg.Storage.Backend != "" {
		st, err := storage.New(storageCfg)
		if err != nil {
			return nil, err
		}
		app.Storage = st
		if local, ok := st.(*storage.Local); ok {
			app.closers = append(app.closers, local.Close)
		}
		logger.Info("附件存储已就绪",
			slog.String("后端", st.Backend()),
			slog.String("目录", cfg.Storage.Local.Dir))
	}

	// ---- 仓储层 ----
	userRepo := repository.NewUserRepo(gdb)
	deptRepo := repository.NewDeptRepo(gdb)
	groupRepo := repository.NewGroupRepo(gdb)

	productlineRepo := repository.NewProductlineRepo(gdb)
	productRepo := repository.NewProductRepo(gdb)
	moduleRepo := repository.NewModuleRepo(gdb)
	planRepo := repository.NewPlanRepo(gdb)

	storyRepo := repository.NewStoryRepo(gdb)
	storySpecRepo := repository.NewStorySpecRepo(gdb)
	storyReviewRepo := repository.NewStoryReviewRepo(gdb)
	projectStoryRepo := repository.NewProjectStoryRepo(gdb)

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

	caseRepo := repository.NewTestCaseRepo(gdb)
	caseStepRepo := repository.NewTestCaseStepRepo(gdb)
	testTaskRepo := repository.NewTestTaskRepo(gdb)
	testTaskCaseRepo := repository.NewTestTaskCaseRepo(gdb)
	testRunRepo := repository.NewTestRunRepo(gdb)
	buildRepo := repository.NewBuildRepo(gdb)

	// ---- 权限判定 ----
	enforcer := acl.New(gdb)

	// 对象级访问网关：把「这条评论/这个附件/这个任务挂在谁身上」翻译成
	// 「它属于哪个产品/项目/私有项目的白名单」，再判定可见性。
	gate := service.NewGate(enforcer, productRepo, storyRepo, planRepo, moduleRepo,
		projectRepo, executionRepo, taskRepo, bugRepo, caseRepo, testTaskRepo)

	// ---- 服务层 ----
	// 通知服务先建：任务、需求、缺陷三个服务都要靠它「通知下一个动手的人」，
	// 而它自己只依赖通知仓储与用户仓储，不会形成环。
	noticeSvc := service.NewNoticeService(gdb, noticeRepo, userRepo)

	authSvc, err := service.NewAuthService(cfg, enforcer, userRepo, groupRepo, deptRepo)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: 构造认证服务失败: %w", err)
	}
	userSvc := service.NewUserService(cfg, gdb, userRepo, deptRepo, groupRepo)
	deptSvc := service.NewDeptService(gdb, deptRepo, userRepo, groupRepo)
	groupSvc := service.NewGroupService(gdb, groupRepo, userRepo)

	productlineSvc := service.NewProductlineService(gdb, productlineRepo, productRepo, userRepo)
	productSvc := service.NewProductService(gdb, productRepo, productlineRepo,
		storyRepo, planRepo, userRepo, gate)
	moduleSvc := service.NewModuleService(gdb, moduleRepo, productRepo, storyRepo,
		taskRepo, bugRepo, caseRepo, gate)
	planSvc := service.NewPlanService(gdb, planRepo, productRepo, storyRepo, gate)

	storySvc := service.NewStoryService(gdb, storyRepo, storySpecRepo, storyReviewRepo,
		projectStoryRepo, productRepo, moduleRepo, planRepo,
		projectRepo, executionRepo, taskRepo, bugRepo, userRepo,
		fileRepo, commentRepo, actionRepo, gate, noticeSvc)

	projectSvc := service.NewProjectService(gdb, projectRepo, executionRepo, taskRepo,
		productRepo, projectProductRepo, projectStoryRepo, storyRepo, userRepo, teamRepo, gate)
	executionSvc := service.NewExecutionService(gdb, executionRepo, projectRepo, taskRepo,
		projectStoryRepo, userRepo, teamRepo, gate)
	taskSvc := service.NewTaskService(gdb, taskRepo, taskTeamRepo, effortRepo,
		storyRepo, productRepo, moduleRepo, executionRepo, userRepo, fileRepo,
		commentRepo, actionRepo, teamRepo, projectSvc, gate, noticeSvc)

	// 缺陷服务依赖需求服务：缺陷「转需求」要真的建出一条需求，
	// 而需求的版本快照、评审人回落那套规则属于需求域，不能在这里重写。
	bugSvc := service.NewBugService(gdb, bugRepo, productRepo, moduleRepo, planRepo,
		projectRepo, executionRepo, taskRepo, storyRepo, userRepo,
		fileRepo, commentRepo, storySvc, gate, noticeSvc)

	// 用例服务：目前只有用例本身（含步骤与评审）。测试单与执行记录落地时，
	// 「用例被引用就不能删」这类约束要加在它身上，而不是散到测试单服务里。
	caseSvc := service.NewTestCaseService(gdb, caseRepo, caseStepRepo,
		productRepo, moduleRepo, storyRepo, userRepo, fileRepo, commentRepo, gate)

	// 测试单服务：结单与指派要发通知，所以它依赖通知服务（与任务/缺陷一样）。
	testTaskSvc := service.NewTestTaskService(gdb, testTaskRepo, testTaskCaseRepo,
		testRunRepo, caseRepo, caseStepRepo, productRepo, projectRepo, executionRepo,
		buildRepo, userRepo, gate, noticeSvc)

	commentSvc := service.NewCommentService(gdb, commentRepo, fileRepo, userRepo, gate)
	fileSvc := service.NewFileService(gdb, fileRepo, userRepo, gate, app.Storage, storageCfg)
	actionSvc := service.NewActionService(gdb, actionRepo, userRepo, fileRepo, gate)

	workbenchSvc := service.NewWorkbenchService(taskRepo, bugRepo, storyRepo, storyReviewRepo,
		effortRepo, noticeRepo, userRepo, productRepo, projectRepo, executionRepo, gate)

	// ---- HTTP 层 ----
	engine, err := router.New(router.Deps{
		Config:        cfg,
		Authenticator: authSvc,
		Enforcer:      enforcer,
		Auth:          handler.NewAuthHandler(authSvc),
		Users:         handler.NewUserHandler(userSvc, authSvc),
		Depts:         handler.NewDeptHandler(deptSvc),
		Groups:        handler.NewGroupHandler(groupSvc),

		Productlines: handler.NewProductlineHandler(productlineSvc),
		Products:     handler.NewProductHandler(productSvc),
		Modules:      handler.NewModuleHandler(moduleSvc),
		Plans:        handler.NewPlanHandler(planSvc),
		Stories:      handler.NewStoryHandler(storySvc),

		Projects:   handler.NewProjectHandler(projectSvc),
		Executions: handler.NewExecutionHandler(executionSvc),
		Tasks:      handler.NewTaskHandler(taskSvc),
		Bugs:       handler.NewBugHandler(bugSvc),

		TestCases: handler.NewTestCaseHandler(caseSvc),
		TestTasks: handler.NewTestTaskHandler(testTaskSvc),

		Comments: handler.NewCommentHandler(commentSvc),
		Files:    handler.NewFileHandler(fileSvc),
		Actions:  handler.NewActionHandler(actionSvc),

		Notices:   handler.NewNoticeHandler(noticeSvc),
		Workbench: handler.NewWorkbenchHandler(workbenchSvc),
	})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: 构建路由失败: %w", err)
	}
	app.Engine = engine

	return app, nil
}
