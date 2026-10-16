package bootstrap_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xywf221/xuliu/internal/bootstrap"
	"github.com/xywf221/xuliu/internal/config"
)

// newTestApp 用临时库组装一个完整的应用。
//
// 这个测试的价值在于「装配本身」：路由注册、迁移、种子、中间件挂载
// 一旦出错，编译是通过的，只有真正把引擎建起来才会暴露
// （典型如 gin 的路由冲突，以及某个仓储没被注入）。
func newTestApp(t *testing.T) *bootstrap.App {
	t.Helper()

	// 走 config.Load 而不是 config.Default()：JWT 密钥这类「必须在
	// 加载阶段补齐」的字段只有在 Load 里才会被 finalize（空密钥在非
	// 开发环境下直接报错，在开发环境下临时生成）。
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("载入默认配置失败: %v", err)
	}
	cfg.Database.DSN = filepath.Join(t.TempDir(), "bootstrap.db")
	// 不启用附件存储：这里验的是装配，不是文件系统。
	cfg.Storage.Backend = ""

	app, err := bootstrap.New(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("组装应用失败: %v", err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Errorf("关闭应用出错: %v", err)
		}
	})
	return app
}

// TestRoutesRegistered 确认 M3 的路由都在路由表里。
//
// 断言到具体路径而不是「路由总数大于 N」：后者改一个数字就能骗过自己，
// 而路径清单正是前端依赖的契约。
func TestRoutesRegistered(t *testing.T) {
	app := newTestApp(t)

	registered := make(map[string]bool)
	for _, r := range app.Engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	want := []string{
		"GET /api/v1/projects",
		"GET /api/v1/projects/brief",
		"POST /api/v1/projects/:id/stories",
		"DELETE /api/v1/projects/:id/stories/:storyId",
		"POST /api/v1/projects/:id/close",
		"GET /api/v1/projects/:id/team",
		"POST /api/v1/executions",
		"POST /api/v1/executions/:id/suspend",
		"GET /api/v1/tasks",
		// kanban 与 /tasks/:id 是同级的路由，两者都要在 ——
		// 这是 gin 路由树里最容易撞车的一处。
		"GET /api/v1/tasks/kanban",
		"GET /api/v1/tasks/:id",
		"POST /api/v1/tasks/:id/finish",
		"POST /api/v1/tasks/:id/assign",
		"POST /api/v1/tasks/:id/efforts",
		"GET /api/v1/efforts",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("路由未注册: %s", route)
		}
	}
}

// TestHealthzAndAuth 确认健康检查开放、业务接口需要登录。
func TestHealthzAndAuth(t *testing.T) {
	app := newTestApp(t)

	// 健康检查不带令牌也要能通。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	app.Engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz 应返回 200，实际 %d", rec.Code)
	}

	// 未登录访问任务列表应被挡在鉴权中间件上。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	app.Engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api/v1/tasks 应返回 401，实际 %d（响应体 %s）",
			rec.Code, rec.Body.String())
	}
}

// TestSeedGrantsM3Privs 确认种子账号真的拿到了项目/任务域的权限点。
//
// 权限点常量在代码里，落到库里的是「哪个组有哪些点」。这一步没做对的话
// 界面能打开但每个按钮都 403，而编译与启动都看不出问题。
func TestSeedGrantsM3Privs(t *testing.T) {
	app := newTestApp(t)

	var adminGroupID uint
	if err := app.DB.Table("sys_group").Select("id").
		Where("name = ?", "项目经理").Scan(&adminGroupID).Error; err != nil {
		t.Fatalf("读内置角色组失败: %v", err)
	}
	if adminGroupID == 0 {
		t.Fatal("种子里应有「项目经理」组")
	}

	var codes []string
	if err := app.DB.Table("sys_group_priv").Select("perm").
		Where("group_id = ?", adminGroupID).Pluck("perm", &codes).Error; err != nil {
		t.Fatalf("读组权限失败: %v", err)
	}
	for _, want := range []string{"project-browse", "execution-create", "task-finish", "effort-record"} {
		if !slices.Contains(codes, want) {
			t.Errorf("项目经理组缺少权限点 %s", want)
		}
	}
}
