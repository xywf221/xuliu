// Package web 负责把前端构建产物嵌进二进制并提供访问。
//
// 产物放在本包的 dist/ 下（Vite 的 outDir 直接指向这里），
// 因为 go:embed 不能引用包目录之外的文件。
//
// 开发期 dist 里可能只有一个 .gitkeep（前端跑在 Vite dev server 上，还没构建产物），
// 所以本包对「没有产物」是显式支持的：Handler 返回 nil，由上层给出提示。
//
// 注意别把这里的容错理解大了：.gitkeep 必须存在，因为 //go:embed 在目录为空时
// 是**编译错误**，运行时兜不住。真正的容错是「编译过了但没有 index.html」这一档。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// dist 是前端构建产物。
//
// 用 all: 前缀是必须的：默认模式下 go:embed 会跳过以 . 或 _ 开头的文件，
// 而 Vite 产物里可能存在这类文件，漏掉会导致资源 404。
//
// all: 在这里还有一层实际作用：dist/.gitkeep 是个点文件，没有 all: 前缀它会被跳过，
// 于是「尚未构建、只有一个占位文件」的仓库反而编不过（空目录是编译错误）。
// 也就是说占位文件与 all: 前缀是一组配套设计，删任一个都会让无前端构建的环境失败。
//
//go:embed all:dist
var dist embed.FS

// indexFile 是 SPA 的入口文件。
const indexFile = "index.html"

// Assets 是对已构建前端的访问封装。
type Assets struct {
	// fsys 是去掉 dist 前缀后的文件系统。
	fsys fs.FS
	// index 是预读的 index.html 内容。
	//
	// 预读的理由：SPA 回退会把每一个前端路由都指到这里，
	// 每次请求都读一遍文件没有意义，且 embed 的内容本就不可变。
	index []byte
	// modTime 用作静态资源的 Last-Modified。
	//
	// embed 出来的文件 ModTime 为零值，若直接交给 http.ServeContent，
	// 它算出的 Last-Modified 会早于任何 If-Modified-Since，
	// 导致每次都返回完整内容而不是 304。这里统一给一个固定时间。
	modTime time.Time
}

// Handler 返回已构建前端的访问封装；若前端尚未构建则返回 nil。
func Handler() *Assets {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}

	// 探测 index.html 是否存在 —— 它不存在就说明还没构建过。
	raw, err := fs.ReadFile(sub, indexFile)
	if err != nil {
		return nil
	}

	// 只保留 assets/ 目录下的内容交给静态文件服务；
	// 其余路径（如 favicon）也一并由 fsys 提供。
	return &Assets{
		fsys:    sub,
		index:   raw,
		modTime: buildTime(),
	}
}

// ServeHTTP 按静态文件语义处理请求。
//
// 对不存在的文件返回 404，交由上层的 SPA 回退逻辑决定是否改送 index.html。
func (a *Assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a == nil {
		http.NotFound(w, r)
		return
	}

	name := cleanPath(r.URL.Path)
	if name == "" {
		a.ServeIndex(w, r)
		return
	}

	f, err := a.fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// 带哈希的产物文件名（如 index-a1b2c3.js）内容不会变，
	// 可以放心长期缓存；其余文件（favicon 等）没有内容哈希，
	// 必须 no-store —— 理由与 ServeIndex 相同。
	modTime := a.modTime
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
		// 零值 ModTime 让 ServeContent 不输出 Last-Modified，
		// 从根上避免「内容变了却回 304」。
		modTime = time.Time{}
	}

	seeker, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 资源文件带 ModTime 才能响应 If-Modified-Since；但它们是 immutable，
	// 正常情况下浏览器不会来问，这里只是给不支持 immutable 的客户端兜底。
	http.ServeContent(w, r, info.Name(), modTime, seeker)
}

// ServeIndex 返回 SPA 的入口页面。
//
// 所有未命中静态资源的前端路由都走这里 —— 这是单页应用在
// 「用户直接访问 /projects/3 并刷新」时仍能正常工作的关键。
//
// index.html 用 no-store 而不是 no-cache：no-cache 只是「使用前必须校验」，
// 而校验要依赖 Last-Modified / ETag。之前这里配的是 no-cache + 固定的
// 2020 年 Last-Modified，结果是浏览器带着 If-Modified-Since 来问，
// 服务端每次都回 304 —— 发版后老用户永远拿不到新的 index.html，
// 也就永远加载旧的模块图（真实踩过：新前端怎么改都不生效）。
// no-store 让它根本不进缓存，代价只是每次多传 1KB 左右的 HTML。
func (a *Assets) ServeIndex(w http.ResponseWriter, r *http.Request) {
	if a == nil || len(a.index) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// 必须显式写 200：SPA 回退是挂在 gin 的 NoRoute 上的，那里已经预置了
	// 404 状态；不覆盖的话，直接访问 / 或刷新子页面会拿到「200 的页面内容
	// 配 404 的状态码」。ServeContent 内部会写 200，所以早先没暴露这个问题。
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.index)
}

// cleanPath 把 URL 路径清洗为文件系统内的相对路径。
//
// 拒绝绝对路径与回溯段：虽然 embed.FS 本身不接受越界路径（会返回错误），
// 但显式清洗能让「非法路径」与「文件不存在」在上层可区分。
func cleanPath(urlPath string) string {
	trimmed := strings.TrimPrefix(urlPath, "/")
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "" {
		return ""
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

// buildTime 是 **内容哈希资源** 的 Last-Modified。
//
// 只给 assets/ 下的文件用：它们的文件名带内容哈希，内容永远不会在同一个
// 名字下变化，所以「固定的 2020 年」不会造成陈旧内容，只会让条件请求
// 一律命中 304（这正是期望行为）。
//
// 不要把它用到没有内容哈希的文件上：固定时间 + 条件请求 =
// 「内容更新了但客户端永远拿到旧版」。index.html 与根级文件走的都是 no-store。
func buildTime() time.Time {
	return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
}
