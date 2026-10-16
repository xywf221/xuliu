package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Local 是本地磁盘存储。
//
// 所有文件操作都经 os.Root（Go 1.24+）进行，把访问范围锁死在根目录内。
// 这是纵深防御：即便 NewKey 的清洗逻辑将来被改错，内核层面仍会拒绝
// 任何逃出根目录的路径，而不是靠我们记得做检查。
type Local struct {
	dir  string
	root *os.Root
}

// NewLocal 打开（必要时创建）本地存储目录。
func NewLocal(dir string) (*Local, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("storage: 本地存储目录不能为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: 解析存储目录失败: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("storage: 创建存储目录 %s 失败: %w", abs, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("storage: 打开存储目录 %s 失败: %w", abs, err)
	}
	return &Local{dir: abs, root: root}, nil
}

// Close 释放目录句柄。
func (l *Local) Close() error { return l.root.Close() }

// Backend 返回后端名。
func (l *Local) Backend() string { return "local" }

// Dir 返回存储根目录的绝对路径（供运维排查）。
func (l *Local) Dir() string { return l.dir }

// Put 写入对象。
func (l *Local) Put(_ context.Context, key string, r io.Reader, size int64, contentType string) (*Object, error) {
	rel, err := l.relPath(key)
	if err != nil {
		return nil, err
	}

	// 先建子目录（key 里的类型/年月两级）。
	//
	// 注意 rel 已是平台分隔符形式（Windows 上是反斜杠），而 path.Dir
	// 只认正斜杠，直接对它用 path.Dir 会得到 "." 导致目录没被创建。
	// 所以先转回斜杠形式取目录，再转成平台形式交给 mkdirAll。
	if err := l.mkdirAll(filepath.FromSlash(path.Dir(filepath.ToSlash(rel)))); err != nil {
		return nil, err
	}

	f, err := l.root.Create(rel)
	if err != nil {
		return nil, fmt.Errorf("storage: 创建文件失败: %w", err)
	}

	// 边写边算 sha256，避免为了取哈希再读一遍文件。
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hasher), r)
	closeErr := f.Close()

	if copyErr != nil {
		// 写入失败时清掉半截文件，避免留下损坏的对象。
		_ = l.root.Remove(rel)
		return nil, fmt.Errorf("storage: 写入文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = l.root.Remove(rel)
		return nil, fmt.Errorf("storage: 关闭文件失败: %w", closeErr)
	}

	return &Object{
		Key:         key,
		Size:        written,
		ContentType: contentType,
		Hash:        hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

// Get 读取对象。
func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	rel, err := l.relPath(key)
	if err != nil {
		return nil, err
	}
	f, err := l.root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: 打开文件失败: %w", err)
	}
	return f, nil
}

// Delete 删除对象。不存在时静默成功（幂等）。
func (l *Local) Delete(_ context.Context, key string) error {
	rel, err := l.relPath(key)
	if err != nil {
		return err
	}
	if err := l.root.Remove(rel); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: 删除文件失败: %w", err)
	}
	return nil
}

// Exists 报告对象是否存在。
func (l *Local) Exists(_ context.Context, key string) (bool, error) {
	rel, err := l.relPath(key)
	if err != nil {
		return false, err
	}
	if _, err := l.root.Stat(rel); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("storage: 查询文件失败: %w", err)
	}
	return true, nil
}

// URL 本地后端不提供直链，返回空串让上层走带鉴权的下载接口。
//
// 这是有意的：本地文件若直接暴露为静态资源，附件就会绕过权限校验，
// 而附件里常含内部设计文档与截图。
func (l *Local) URL(context.Context, string) (string, error) { return "", nil }

// relPath 校验并归一化对象键，返回可交给 os.Root 的相对路径。
func (l *Local) relPath(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("storage: 对象键不能为空")
	}
	// 统一成斜杠形式，并拒绝绝对路径与回溯段。
	cleaned := path.Clean(strings.ReplaceAll(key, "\\", "/"))
	if path.IsAbs(cleaned) ||
		cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") ||
		strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("storage: 非法的对象键 %q", key)
	}
	if cleaned == "." {
		return "", fmt.Errorf("storage: 非法的对象键 %q", key)
	}
	return filepath.FromSlash(cleaned), nil
}

// mkdirAll 逐级创建目录。
//
// os.Root 没有 MkdirAll（root.Mkdir 只能建一级），而对象键是
// "bug/202610/<uuid>.png" 这样的多级路径，所以这里自己逐级建。
//
// 每一级都经根目录句柄创建而非 os.MkdirAll(绝对路径)：
// 这样目录创建同样受 os.Root 的越界保护约束，与写入路径保持一致的
// 安全边界，而不是在写入前先用一条不受保护的旁路。
func (l *Local) mkdirAll(rel string) error {
	if rel == "" || rel == "." {
		return nil
	}

	// 拆成逐级累进的相对路径，依次创建。
	segments := strings.Split(filepath.ToSlash(rel), "/")
	current := ""
	for _, seg := range segments {
		if seg == "" || seg == "." {
			continue
		}
		if current == "" {
			current = seg
		} else {
			current = current + "/" + seg
		}

		currentRel := filepath.FromSlash(current)
		if err := l.root.Mkdir(currentRel, 0o750); err != nil {
			// 已存在是正常情况（同一月份的第二份附件），
			// 但若同名路径是个文件，就必须报错而不是继续。
			if errors.Is(err, fs.ErrExist) {
				if info, statErr := l.root.Stat(currentRel); statErr == nil && info.IsDir() {
					continue
				}
				return fmt.Errorf("storage: 路径 %s 已被非目录占用", currentRel)
			}
			return fmt.Errorf("storage: 创建子目录 %s 失败: %w", currentRel, err)
		}
	}
	return nil
}
