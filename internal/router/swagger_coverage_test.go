package router

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSwaggerCoversEveryAPIRoute 保证「注册了的路由」与「文档里有的操作」是同一份清单。
//
// 为什么值得钉成测试而不是手工核对一遍：swagger 注释是**手写**在 handler 上的，
// 新增一个接口时忘记加注释不会有任何报错 —— 文档只是悄悄少了一项，
// 而少掉的那项正是别人接下来要对接的接口。同理，@Router 的路径写错时
// swag 一样生成成功，只是文档里多出一条指向空气的路径。
// 两个方向都在此断言，缺失与多余都能抓到。
//
// 这个测试**只**验证存在性与路径拼写，不验证注释内容是否属实
// （参数是否真被读取、状态码是否与错误码表一致 —— 那要读 handler 才看得出来）。
func TestSwaggerCoversEveryAPIRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	routes := registeredAPIRoutes(t)
	operations := documentedOperations(t)

	var missing []string
	for _, r := range routes {
		if _, ok := operations[r.key()]; !ok {
			missing = append(missing, r.String())
		}
	}

	var stale []string
	for key := range operations {
		if !containsRoute(routes, key) {
			stale = append(stale, key)
		}
	}

	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		var b strings.Builder
		if len(missing) > 0 {
			b.WriteString("\n以下路由没有 swagger 注释（给 handler 补 @Router 等注解，再重跑 swag init）：\n")
			for _, m := range missing {
				b.WriteString("  " + m + "\n")
			}
		}
		if len(stale) > 0 {
			b.WriteString("\n以下文档操作找不到对应路由（多半是 @Router 写错了，或接口已删除而注释留下）：\n")
			for _, s := range stale {
				b.WriteString("  " + strings.ReplaceAll(s, "|", " ") + "\n")
			}
		}
		t.Error(b.String())
	}

	// 数量本身也钉一下：文档大幅缩水通常意味着某次重构把注释整块删了。
	if len(operations) < len(routes) {
		t.Errorf("文档操作数 %d 少于路由数 %d", len(operations), len(routes))
	}
}

// route 是一条已注册的 API 路由。
type route struct {
	Method string
	Path   string // 已去掉 /api/v1 前缀、并把 :id 归一化为 {id}
}

func (r route) String() string { return r.Method + " " + r.Path }

// key 是路由与文档操作比对的键。
func (r route) key() string { return r.Method + "|" + r.Path }

func containsRoute(routes []route, key string) bool {
	for _, r := range routes {
		if r.key() == key {
			return true
		}
	}
	return false
}

// registeredAPIRoutes 用生产代码同一条路径挂上全部路由，再读出 gin 的路由表。
//
// Deps 传零值：注册阶段只是把 handler 存进树里，不会解引用任何服务，
// 因此不必为此装配数据库 —— 这点很重要，否则这个测试就成了集成测试。
func registeredAPIRoutes(t *testing.T) []route {
	t.Helper()

	engine := gin.New()
	api := engine.Group("/api/v1")
	registerSystem(api, Deps{})
	registerProduct(api, Deps{})
	registerStory(api, Deps{})
	registerProject(api, Deps{})
	registerTask(api, Deps{})
	registerBug(api, Deps{})
	registerTest(api, Deps{})
	registerCommon(api, Deps{})
	registerWorkbench(api, Deps{})

	const prefix = "/api/v1"
	var out []route
	for _, r := range engine.Routes() {
		if !strings.HasPrefix(r.Path, prefix) {
			continue
		}
		out = append(out, route{
			Method: strings.ToLower(r.Method),
			Path:   normalizeGinPath(strings.TrimPrefix(r.Path, prefix)),
		})
	}
	if len(out) == 0 {
		t.Fatal("一条 API 路由都没注册到，测试本身有问题")
	}
	return out
}

// normalizeGinPath 把 gin 的参数写法换成 swagger 的写法。
//
// gin 用 /users/:id，swagger 用 /users/{id}；不归一化就没法比对。
func normalizeGinPath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, ":") {
			segments[i] = "{" + strings.TrimPrefix(seg, ":") + "}"
		} else if strings.HasPrefix(seg, "*") {
			segments[i] = "{" + strings.TrimPrefix(seg, "*") + "}"
		}
	}
	return strings.Join(segments, "/")
}

// documentedOperations 读 swag 生成的 swagger.json，取出全部 (method, path)。
func documentedOperations(t *testing.T) map[string]struct{} {
	t.Helper()

	// 文档是 `go tool swag init` 的产物，放在仓库根的 docs/ 下。
	// 这里按相对路径读：测试跑在 internal/router 包里。
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "swagger.json"))
	if err != nil {
		t.Fatalf("读 docs/swagger.json 失败（先跑 go tool swag init -g cmd/xuliu/main.go -o docs）：%v", err)
	}

	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析 swagger.json 失败：%v", err)
	}

	out := make(map[string]struct{})
	for path, methods := range doc.Paths {
		for method := range methods {
			// definitions 之外，paths 下每个路径还可能有 parameters 等键，
			// 那些不是 HTTP 方法，跳过。
			if !isHTTPMethod(method) {
				continue
			}
			out[strings.ToLower(method)+"|"+path] = struct{}{}
		}
	}
	if len(out) == 0 {
		t.Fatal("文档里一个操作都没有，测试本身有问题")
	}
	return out
}

// httpMethods 是 swagger 的 paths 下算「一个操作」的键。
var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "head": true, "options": true,
}

// isHTTPMethod 排除 parameters、$ref 这类路径级键 —— 它们不是操作，
// 混进来的话会把「多余操作」的数量虚报出来。
func isHTTPMethod(s string) bool { return httpMethods[strings.ToLower(s)] }
