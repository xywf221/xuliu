package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xywf221/xuliu/internal/storage"
)

// newLocal 在临时目录里建一个本地存储。
func newLocal(t *testing.T) *storage.Local {
	t.Helper()
	s, err := storage.NewLocal(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatalf("创建本地存储失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestLocalRoundTrip 覆盖写入-读取-存在性-删除的完整流程。
func TestLocalRoundTrip(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()

	key := storage.NewKey("bug", "截图.png")
	content := []byte("这是一个测试附件的内容")

	obj, err := s.Put(ctx, key, bytes.NewReader(content), int64(len(content)), "image/png")
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if obj.Size != int64(len(content)) {
		t.Errorf("返回大小应为 %d，实际 %d", len(content), obj.Size)
	}
	if len(obj.Hash) != 64 {
		t.Errorf("应返回 sha256 十六进制哈希（64 字符），实际 %q", obj.Hash)
	}

	// 读回的内容必须一致。
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("读取内容失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("读回内容不一致：got %q want %q", got, content)
	}

	// 存在性。
	ok, err := s.Exists(ctx, key)
	if err != nil || !ok {
		t.Errorf("对象应存在，实际 ok=%v err=%v", ok, err)
	}

	// 删除后应查不到，且重复删除不报错（幂等）。
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	ok, err = s.Exists(ctx, key)
	if err != nil || ok {
		t.Errorf("删除后对象不应存在，实际 ok=%v err=%v", ok, err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Errorf("重复删除应幂等成功，实际: %v", err)
	}
}

// TestGetMissingReturnsNotFound 验证「对象不存在」是明确的
// ErrNotFound，而不是某种底层 IO 错误 —— 上层要靠它区分 404 与 500。
func TestGetMissingReturnsNotFound(t *testing.T) {
	s := newLocal(t)
	_, err := s.Get(context.Background(), "bug/202601/does-not-exist.txt")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("应返回 ErrNotFound，实际: %v", err)
	}
}

// TestKeyRejectsTraversal 验证即便传入恶意对象键，也无法读写根目录之外的文件。
//
// 这是纵深防御的第二层：正常路径下 NewKey 已保证键的安全，
// 但若将来有人绕过它直接传键（比如实现「按用户提供的路径下载」），
// 这一层必须仍然拦得住。
func TestKeyRejectsTraversal(t *testing.T) {
	s := newLocal(t)
	ctx := context.Background()

	// 先在存储根目录外放一个「机密文件」。
	secret := filepath.Join(filepath.Dir(s.Dir()), "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret"), 0o600); err != nil {
		t.Fatalf("准备测试文件失败: %v", err)
	}

	for _, evil := range []string{
		"../secret.txt",
		"../../secret.txt",
		"a/../../secret.txt",
		`..\secret.txt`,
		"/etc/passwd",
		"bug/../../../secret.txt",
	} {
		t.Run(evil, func(t *testing.T) {
			if _, err := s.Get(ctx, evil); err == nil {
				t.Errorf("恶意键 %q 不应被接受", evil)
			}
			if _, err := s.Put(ctx, evil, strings.NewReader("x"), 1, "text/plain"); err == nil {
				t.Errorf("恶意键 %q 不应允许写入", evil)
			}
			if err := s.Delete(ctx, evil); err == nil {
				t.Errorf("恶意键 %q 不应允许删除", evil)
			}
		})
	}

	// 确认机密文件确实没被读到或删除。
	if data, err := os.ReadFile(secret); err != nil || string(data) != "top-secret" {
		t.Errorf("根目录外的文件不应被影响：err=%v data=%q", err, data)
	}
}

// TestNewKeySanitizesInput 验证对象键的生成与用户输入完全解耦。
func TestNewKeySanitizesInput(t *testing.T) {
	cases := []struct {
		name         string
		objectType   string
		originalName string
		wantExt      string
	}{
		{"正常文件", "bug", "screenshot.png", ".png"},
		{"双扩展名取最后一个", "bug", "shell.php.jpg", ".jpg"},
		{"大写扩展名转小写", "bug", "PHOTO.PNG", ".png"},
		{"无扩展名", "bug", "Makefile", ""},
		{"扩展名含非法字符则丢弃", "bug", "evil.p!h@p", ""},
		{"超长扩展名丢弃", "bug", "a.superlongextension", ""},
		{"中文文件名", "bug", "截图.png", ".png"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := storage.NewKey(tc.objectType, tc.originalName)

			// 键必须以对象类型开头、按年月分目录。
			if !strings.HasPrefix(key, tc.objectType+"/") {
				t.Errorf("键应以 %q 开头，实际 %q", tc.objectType, key)
			}
			if !strings.HasSuffix(key, tc.wantExt) {
				t.Errorf("键应以 %q 结尾，实际 %q", tc.wantExt, key)
			}
			// 关键：用户提供的文件名绝不能出现在键里。
			if tc.originalName != "" && strings.Contains(key, tc.originalName) {
				t.Errorf("键不应包含原始文件名 %q，实际 %q", tc.originalName, key)
			}
			// 键中不应有任何回溯或分隔符逃逸。
			if strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
				t.Errorf("键包含可疑片段：%q", key)
			}
			// 两段目录：类型/年月。
			if parts := strings.Split(key, "/"); len(parts) != 3 {
				t.Errorf("键应为 类型/年月/文件 三段，实际 %q", key)
			}
		})
	}
}

// TestNewKeySanitizesObjectType 验证对象类型里的非法字符被清洗，
// 否则它可以被用来从参数注入路径片段。
func TestNewKeySanitizesObjectType(t *testing.T) {
	key := storage.NewKey("../../etc", "x.txt")
	if strings.Contains(key, "..") {
		t.Errorf("对象类型中的回溯片段应被清洗，实际 %q", key)
	}

	// 全非法字符时回退为 misc，而不是产生空片段（会变成 // 双层斜杠）。
	key = storage.NewKey("///", "x.txt")
	if !strings.HasPrefix(key, "misc/") {
		t.Errorf("非法对象类型应回退为 misc，实际 %q", key)
	}
}

// TestValidateUpload 覆盖上传前的校验规则。
func TestValidateUpload(t *testing.T) {
	cfg := storage.Config{
		MaxSize:           1024,
		AllowedExtensions: []string{".png", ".txt"},
	}

	// 应通过。
	for _, ok := range []struct {
		name string
		size int64
	}{
		{"a.png", 100},
		{"a.txt", 1024},   // 恰好等于上限
		{"Makefile", 100}, // 无扩展名不拦
		{"a.PNG", 100},    // 大小写不敏感
	} {
		if err := storage.ValidateUpload(ok.name, ok.size, cfg); err != nil {
			t.Errorf("%q(%d) 应通过校验，实际: %v", ok.name, ok.size, err)
		}
	}

	// 应被拒。
	for _, bad := range []struct {
		name string
		size int64
	}{
		{"a.png", 0},    // 空文件
		{"a.png", -1},   // 非法大小
		{"a.png", 1025}, // 超限
		{"a.exe", 100},  // 扩展名不允许
		{"a.sh", 100},   // 扩展名不允许
	} {
		if err := storage.ValidateUpload(bad.name, bad.size, cfg); err == nil {
			t.Errorf("%q(%d) 应被拒绝", bad.name, bad.size)
		}
	}
}

// TestHumanSize 验证大小格式化。
func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:              "512 B",
		1024:             "1.0 KB",
		1536:             "1.5 KB",
		1024 * 1024:      "1.0 MB",
		64 * 1024 * 1024: "64.0 MB",
	}
	for size, want := range cases {
		if got := storage.HumanSize(size); got != want {
			t.Errorf("HumanSize(%d) = %q，期望 %q", size, got, want)
		}
	}
}
