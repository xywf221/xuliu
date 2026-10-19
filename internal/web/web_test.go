package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// newAssets 造一个不依赖真实构建产物的 Assets。
//
// 故意不读 internal/web/dist：那份目录在未构建前端的 clone 里只有 .gitkeep，
// 用它做测试会让「没装 Node 就没法 go test」。
func newAssets(index string) *Assets {
	const asset = "assets/app-abc123.js"
	return &Assets{
		fsys: fstest.MapFS{
			"index.html":  &fstest.MapFile{Data: []byte(index)},
			asset:         &fstest.MapFile{Data: []byte("console.log(1)")},
			"favicon.ico": &fstest.MapFile{Data: []byte("ico")},
		},
		index:   []byte(index),
		modTime: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestServeIndexNeverReturns304 是发版后「前端改了但不生效」的回归测试。
//
// 旧实现给 index.html 配 no-cache + 固定的 2020 年 Last-Modified，
// 浏览器带 If-Modified-Since 来校验时永远得到 304，于是永远加载旧版的
// 资源引用 —— 新的 JS 根本没机会被请求。这个 bug 不会报错、不会 404，
// 只会让「明明改了」变成玄学。
func TestServeIndexNeverReturns304(t *testing.T) {
	a := newAssets("<html>old</html>")

	// 第一次拿到 old，模拟浏览器把它连 Last-Modified 一起缓存下来。
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("首次访问 /: 期望 200，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("index.html 的 Cache-Control 应为 no-store，实际 %q", got)
	}

	// 模拟发版：同一个进程里换掉 index 内容，再带着条件请求来问。
	a.index = []byte("<html>new</html>")
	a.fsys = newAssets("<html>new</html>").fsys

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-Modified-Since", time.Now().UTC().Format(http.TimeFormat))
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("发版后访问 /: 期望 200（绝不能是 304），实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "new") {
		t.Errorf("发版后应返回新的 index.html，实际 %q", body)
	}
}

// TestServeIndexWrites200 钉住「SPA 回退必须显式写 200」。
//
// 它挂在 gin 的 NoRoute 上，而 gin 在调用 NoRoute 前已把状态预置成 404。
// 如果 ServeIndex 只 Write 不 WriteHeader，gin 会把这个 404 写出去 ——
// 页面内容是对的，状态码是 404，监控与爬虫看到的全是「不存在」。
// 这里用一个只记录状态码的 ResponseWriter，不依赖 gin 也能测出这一点。
func TestServeIndexWrites200(t *testing.T) {
	a := newAssets("<html>ok</html>")

	rec := &statusWriter{}
	a.ServeIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.code != http.StatusOK {
		t.Fatalf("ServeIndex 必须显式写 200，实际 %d", rec.code)
	}
	if rec.body != "<html>ok</html>" {
		t.Errorf("响应体应为 index.html，实际 %q", rec.body)
	}
}

// statusWriter 记录第一次 WriteHeader 的状态码与写入内容。
type statusWriter struct {
	code int
	body string
}

func (w *statusWriter) Header() http.Header { return http.Header{} }

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	w.body += string(p)
	return len(p), nil
}

// TestAssetsAreImmutable 固定住「带哈希的资源可以长缓存」。
func TestAssetsAreImmutable(t *testing.T) {
	a := newAssets("<html></html>")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app-abc123.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("资源访问: 期望 200，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("带哈希资源应为 immutable，实际 %q", got)
	}
}

// TestRootFilesAreNotCached 覆盖 favicon 这类没有内容哈希的文件：
// 它们同样不能用固定 Last-Modified 换 304。
func TestRootFilesAreNotCached(t *testing.T) {
	a := newAssets("<html></html>")
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	req.Header.Set("If-Modified-Since", time.Now().UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("根级文件: 期望 200，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("根级文件应为 no-store，实际 %q", got)
	}
}
