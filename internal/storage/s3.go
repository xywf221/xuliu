package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 是 S3 兼容的对象存储后端。
//
// 用 minio-go 而不是 aws-sdk-go-v2：前者是单模块、依赖少，
// 且对 MinIO / 阿里云 OSS / 腾讯云 COS / AWS S3 的兼容性都经过验证；
// 后者要拉 config、credentials、smithy 等多个模块。
//
// 签名算法（SigV4）由 SDK 负责 —— 这类密码学相关的协议实现不该手写。
type S3 struct {
	client        *minio.Client
	bucket        string
	publicBaseURL string
}

// NewS3 构造 S3 后端并确认桶可用。
//
// 这里会真实访问一次远端：配置写错（桶名拼错、密钥无效、网络不通）
// 应当在启动时就暴露，而不是等用户第一次上传附件才发现。
func NewS3(cfg S3Config) (*S3, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("storage: S3 桶名不能为空")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, errors.New("storage: S3 的 accessKey 与 secretKey 都不能为空")
	}

	// endpoint 只写主机名，不带协议 —— minio-go 的约定。
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	endpoint = strings.TrimSuffix(endpoint, "/")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: 初始化 S3 客户端失败: %w", err)
	}

	s := &S3{
		client:        client,
		bucket:        cfg.Bucket,
		publicBaseURL: strings.TrimSuffix(cfg.PublicBaseURL, "/"),
	}

	// 启动期连通性检查，带超时避免卡住启动。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: 访问 S3 桶 %s 失败: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("storage: S3 桶 %s 不存在（本服务不会自动创建桶）", cfg.Bucket)
	}

	return s, nil
}

// Backend 返回后端名。
func (s *S3) Backend() string { return "s3" }

// Put 上传对象。
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (*Object, error) {
	// 与本地后端一致地算内容哈希。S3 的 ETag 在分片上传时不是内容 MD5，
	// 无法用来做去重判断，所以自己算。
	hasher := sha256.New()
	reader := io.TeeReader(r, hasher)

	opts := minio.PutObjectOptions{}
	if contentType != "" {
		opts.ContentType = contentType
	}
	// size 未知时传 -1，SDK 会走分片上传并自动缓冲。
	if size <= 0 {
		size = -1
	}

	info, err := s.client.PutObject(ctx, s.bucket, key, reader, size, opts)
	if err != nil {
		return nil, fmt.Errorf("storage: 上传到 S3 失败: %w", err)
	}

	return &Object{
		Key:         key,
		Size:        info.Size,
		ContentType: contentType,
		Hash:        hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

// Get 下载对象。
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: 读取 S3 对象失败: %w", err)
	}
	// GetObject 是惰性的，错误要等 Stat 之后才暴露 —— 不 Stat 的话
	// 「对象不存在」会在第一次 Read 时以奇怪的 EOF 形式出现。
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: 查询 S3 对象失败: %w", err)
	}
	return obj, nil
}

// Delete 删除对象。不存在时静默成功。
func (s *S3) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("storage: 删除 S3 对象失败: %w", err)
	}
	return nil
}

// Exists 报告对象是否存在。
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage: 查询 S3 对象失败: %w", err)
	}
	return true, nil
}

// URL 返回可访问地址。
//
// 只在配置了 PublicBaseURL（如 CDN 域名）时返回，因为它意味着
// 该桶已对公网开放读取。否则返回空串，让上层走带鉴权的下载接口 ——
// 附件常含内部资料，默认不应匿名可读。
func (s *S3) URL(_ context.Context, key string) (string, error) {
	if s.publicBaseURL == "" {
		return "", nil
	}
	// 逐段转义，保留路径分隔符。
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return s.publicBaseURL + "/" + strings.Join(segments, "/"), nil
}

// isS3NotFound 判断错误是否为「对象不存在」。
//
// minio-go 用错误码而非哨兵错误表达，所以只能按码匹配。
func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		switch resp.Code {
		case "NoSuchKey", "NoSuchObject", "NotFound", "NoSuchBucket":
			return true
		}
	}
	return false
}

// 编译期断言：S3 满足 Storage 接口。
var _ Storage = (*S3)(nil)

// 编译期断言：Local 满足 Storage 接口。
var _ Storage = (*Local)(nil)
