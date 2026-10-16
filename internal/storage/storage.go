// Package storage 抽象附件的实际存放位置。
//
// 两种实现：本地磁盘（默认，零依赖）与 S3 兼容对象存储。
// 上层只依赖 Storage 接口，切换后端只需改配置。
//
// 安全要点：**对象键（key）永远由我们生成，绝不使用用户提供的文件名**。
// 用户文件名只作为元数据保存与展示。这从根上消除了路径穿越、
// 重名覆盖、以及 Windows 保留名（CON、NUL）等问题。
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"uuid"
)

// ErrNotFound 表示对象不存在。
var ErrNotFound = errors.New("storage: 对象不存在")

// Object 是写入成功后的对象描述。
type Object struct {
	// Key 是后端内部的唯一标识（本地路径或 S3 对象键）。
	Key string
	// Size 是字节数。
	Size int64
	// ContentType 是探测到的 MIME 类型。
	ContentType string
	// Hash 是内容的 sha256，用于秒传与去重。
	Hash string
}

// Storage 是附件存储后端。
type Storage interface {
	// Put 写入一个对象，返回其标识。key 由调用方生成，必须是安全路径。
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (*Object, error)
	// Get 读取对象。对象不存在时返回 ErrNotFound。
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除对象。对象不存在不算错误（幂等）。
	Delete(ctx context.Context, key string) error
	// Exists 报告对象是否存在。
	Exists(ctx context.Context, key string) (bool, error)
	// URL 返回可直接访问的地址；不支持直链的后端返回空串
	// （此时由上层走带鉴权的下载接口）。
	URL(ctx context.Context, key string) (string, error)
	// Backend 返回后端名称，用于记录在附件元信息里。
	Backend() string
}

// NewKey 为附件生成对象键。
//
// 结构：<对象类型>/<年月>/<uuid>.<扩展名>
//
//   - 按对象类型与月份分目录，避免单目录堆积几十万文件
//     （本地后端下目录过大会显著拖慢文件系统操作，S3 上则影响列举性能）；
//   - 文件名用 UUID，与用户输入完全无关，因此不存在穿越与重名；
//   - 扩展名做白名单式清洗（只保留字母数字），防止把 "/" 或 ".." 带进来。
func NewKey(objectType, originalName string) string {
	objectType = sanitizeSegment(objectType)
	if objectType == "" {
		objectType = "misc"
	}

	return objectType + "/" + time.Now().UTC().Format("200601") + "/" +
		uuid.NewV7().String() + extensionOf(originalName)
}

// extensionOf 提取并清洗扩展名。
//
// 只保留最后一个点之后、且长度合理的字母数字串。
// 像 "shell.php.jpg" 这种双扩展名，取到的是 ".jpg"，符合预期；
// 而 "evil.php%00.jpg" 之类经过清洗后不会残留空字节。
func extensionOf(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	ext := name[idx:] // 含点
	if len(ext) > 12 {
		// 过长的「扩展名」多半不是真的扩展名，忽略掉更安全。
		return ""
	}
	for _, r := range ext[1:] {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
		default:
			return ""
		}
	}
	return strings.ToLower(ext)
}

// sanitizeSegment 清洗路径片段，只保留字母数字与下划线。
func sanitizeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Config 是存储层配置。
type Config struct {
	// Backend 取 local / s3。
	Backend string

	LocalDir string
	// MaxSize 单文件字节上限。
	MaxSize int64
	// AllowedExtensions 为空表示不限制。
	AllowedExtensions []string

	S3 S3Config
}

// S3Config 是 S3 兼容后端的连接参数。
type S3Config struct {
	Endpoint      string
	Region        string
	Bucket        string
	AccessKey     string
	SecretKey     string
	UseSSL        bool
	PublicBaseURL string
}

// New 按配置构造存储后端。
func New(cfg Config) (Storage, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "local", "":
		return NewLocal(cfg.LocalDir)
	case "s3":
		return NewS3(cfg.S3)
	default:
		return nil, fmt.Errorf("storage: 未知的后端 %q（可选 local / s3）", cfg.Backend)
	}
}

// ValidateUpload 在上传前做统一校验，返回面向用户的错误原因。
//
// 放在 storage 包而不是 handler 里，是为了保证「无论从哪个入口上传」
// 都受同一套约束 —— 将来加导入功能时不会漏掉。
func ValidateUpload(name string, size int64, cfg Config) error {
	if size <= 0 {
		return errors.New("上传的文件为空")
	}
	if cfg.MaxSize > 0 && size > cfg.MaxSize {
		return fmt.Errorf("附件大小超出限制（最大 %s）", HumanSize(cfg.MaxSize))
	}
	if len(cfg.AllowedExtensions) == 0 {
		return nil
	}

	ext := extensionOf(name)
	// 没有扩展名时按允许处理：很多合法的无扩展名文件（README、Makefile）
	// 会被误伤，而真正的风险来自于有可执行扩展名的文件。
	if ext == "" {
		return nil
	}
	for _, allowed := range cfg.AllowedExtensions {
		if strings.EqualFold(allowed, ext) {
			return nil
		}
	}
	return fmt.Errorf("不支持的文件类型 %s", ext)
}

// HumanSize 把字节数格式化为人读的大小。
func HumanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
