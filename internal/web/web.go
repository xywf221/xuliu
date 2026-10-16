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
	"bytes"
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
	// 可以放心长期缓存；index.html 本身则必须每次都校验。
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	seeker, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		http.NotFound(w, r)
		return
	}
	// 带上 ModTime 才能响应 If-Modified-Since，让重复访问走 304。
	http.ServeContent(w, r, info.Name(), a.modTime, seeker)
}

// ServeIndex 返回 SPA 的入口页面。
//
// 所有未命中静态资源的前端路由都走这里 —— 这是单页应用在
// 「用户直接访问 /projects/3 并刷新」时仍能正常工作的关键。
func (a *Assets) ServeIndex(w http.ResponseWriter, r *http.Request) {
	if a == nil || len(a.index) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html 引用的资源带内容哈希，所以它自己不能缓存，
	// 否则发版后用户会一直加载旧版的资源引用。
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, indexFile, a.modTime, bytes.NewReader(a.index))
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

// buildTime 返回一个用于 Last-Modified 的稳定时间。
//
// 用编译期不可得，所以取一个固定的近期时间而非 time.Now()：
// 后者会让每次进程重启都产生不同的 Last-Modified，
// 使客户端缓存在重启后全部失效。料想发布频率不高，固定值足够。
func buildTime() time.Time {
	return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
}
