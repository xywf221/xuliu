package service

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/repository"
	"github.com/xywf221/xuliu/internal/storage"
)

// FileService 负责附件的上传、下载与删除。
//
// 两条铁律：
//
//  1. **存储路径永远由我们生成**（storage.NewKey），绝不使用用户提供的
//     文件名。这从根上消除了路径穿越、重名覆盖与 Windows 保留名问题。
//     用户文件名只作为元数据保存与展示。
//  2. **下载要鉴权**。附件常常比标题更能说明业务内容（测试报告、设计稿），
//     所以下载走带鉴权的接口，而不是把目录直接暴露成静态资源。
type FileService struct {
	db      *gorm.DB
	files   *repository.FileRepo
	users   *repository.UserRepo
	gate    *Gate
	backend storage.Storage
	cfg     storage.Config
}

// NewFileService 构造附件服务。
//
// backend 为 nil 表示附件功能未启用（配置里 storage.backend 为空），
// 此时所有附件接口都返回「存储未启用」，而不是 panic。
func NewFileService(
	db *gorm.DB,
	files *repository.FileRepo,
	users *repository.UserRepo,
	gate *Gate,
	backend storage.Storage,
	cfg storage.Config,
) *FileService {
	return &FileService{db: db, files: files, users: users, gate: gate, backend: backend, cfg: cfg}
}

// UploadInput 是一次上传的入参。
type UploadInput struct {
	// Name 是用户看到的原始文件名。
	Name string
	Size int64
	// ContentType 由请求头给出；为空时按扩展名推断。
	ContentType string
	Reader      io.Reader

	// ObjectType/ObjectID 指向要挂载的业务对象；为空表示先上传、稍后挂载。
	ObjectType string
	ObjectID   uint
}

// Upload 保存一个附件。
func (s *FileService) Upload(ctx context.Context, in UploadInput) (*dto.FileResp, error) {
	if s.backend == nil {
		return nil, apperr.New(apperr.StorageDisabled)
	}

	name := sanitizeFileName(in.Name)
	if name == "" {
		return nil, apperr.Newf(apperr.InvalidParam, "文件名不能为空")
	}

	// 大小与扩展名校验走 storage 包的统一实现：
	// 将来加「批量导入」时不会漏掉这套约束。
	if err := storage.ValidateUpload(name, in.Size, s.cfg); err != nil {
		return nil, uploadError(err)
	}

	// 有归属时先校验可见性，避免为无权对象上传文件。
	if in.ObjectID > 0 && in.ObjectType != "" {
		if err := s.gate.AssertVisible(ctx, in.ObjectType, in.ObjectID); err != nil {
			return nil, err
		}
	}

	contentType := in.ContentType
	if contentType == "" {
		contentType = guessContentType(name)
	}

	obj, err := s.backend.Put(ctx, storage.NewKey(in.ObjectType, name), in.Reader, in.Size, contentType)
	if err != nil {
		return nil, apperr.Wrap(apperr.FileStoreFailed, err)
	}

	file := &model.File{
		ObjectType:  in.ObjectType,
		ObjectID:    in.ObjectID,
		Name:        name,
		Path:        obj.Key,
		Backend:     s.backend.Backend(),
		Size:        obj.Size,
		ContentType: obj.ContentType,
		Hash:        obj.Hash,
		UserID:      currentUserID(ctx),
	}

	if err := s.files.Create(ctx, nil, file); err != nil {
		// 元信息写失败就把已经落盘的对象删掉：留一个无人引用的文件
		// 只是磁盘垃圾，而下一次上传同名文件会再写一份新的（键是 UUID）。
		_ = s.backend.Delete(ctx, obj.Key)
		return nil, err
	}

	resp := s.toFileResp(ctx, file)
	return &resp, nil
}

// Download 取附件内容。
//
// 返回闭包而不是 io.ReadCloser 之外的东西：读流的关闭由 handler 负责。
func (s *FileService) Download(ctx context.Context, id uint) (*dto.FileResp, io.ReadCloser, error) {
	if s.backend == nil {
		return nil, nil, apperr.New(apperr.StorageDisabled)
	}

	file, err := s.files.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if err := s.assertFileReadable(ctx, file); err != nil {
		return nil, nil, err
	}

	reader, err := s.backend.Get(ctx, file.Path)
	if err != nil {
		if err == storage.ErrNotFound {
			// 元信息在、对象不在：数据不一致，报内部错误并留下线索。
			return nil, nil, apperr.Wrap(apperr.FileNotFound,
				fmt.Errorf("service: 附件 %d 的存储对象缺失: %s", id, file.Path))
		}
		return nil, nil, apperr.Wrap(apperr.FileStoreFailed, err)
	}

	// 下载计数失败不该阻断下载。
	_ = s.files.IncrDownloads(ctx, id)

	file.Downloads++
	resp := s.toFileResp(ctx, file)
	return &resp, reader, nil
}

// List 返回某对象下的全部附件。
func (s *FileService) List(ctx context.Context, objectType string, objectID uint) ([]dto.FileResp, error) {
	if err := s.gate.AssertVisible(ctx, objectType, objectID); err != nil {
		return nil, err
	}
	files, err := s.files.ListByObject(ctx, objectType, objectID)
	if err != nil {
		return nil, err
	}
	return s.toFileResps(ctx, files), nil
}

// Delete 删除附件。
//
// 只有上传者本人或超级管理员能删：别人上传的证据（截图、日志）
// 不该被旁人抹掉。
func (s *FileService) Delete(ctx context.Context, id uint) error {
	file, err := s.files.GetByID(ctx, id)
	if err != nil {
		return err
	}

	current := currentUserID(ctx)
	if file.UserID != current && !isSuperAdmin(ctx) {
		return apperr.Newf(apperr.Forbidden, "只能删除自己上传的附件")
	}
	if err := s.assertFileReadable(ctx, file); err != nil {
		return err
	}

	// 先删元信息再删对象：顺序反过来的话，若元信息删除失败，
	// 就会留下一条指向已消失文件的记录，用户点下载才报错。
	if err := s.files.Delete(ctx, nil, id); err != nil {
		return err
	}
	if s.backend != nil {
		// 对象删不掉只记不报：元信息已经没了，用户视角的删除是成功的，
		// 残留对象由清理任务处理，不该把失败甩给用户。
		_ = s.backend.Delete(ctx, file.Path)
	}
	return nil
}

// assertFileReadable 校验当前用户能读这个附件。
//
// 规则分两种情况：
//   - 已挂载到业务对象：看对象所属产品的可见性（与评论同一套判定）；
//   - 尚未挂载（刚上传还没提交表单）：只有上传者本人能访问。
//
// 第二种是必要的：上传与提交是两次请求，中间那一小段时间里，
// 这个文件同样不能对所有人开放。
func (s *FileService) assertFileReadable(ctx context.Context, file *model.File) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	if file.ObjectType == "" || file.ObjectID == 0 {
		if file.UserID != userID && !isSuperAdmin(ctx) {
			return apperr.Newf(apperr.Forbidden, "该附件尚未归属任何对象，只有上传者可以访问")
		}
		return nil
	}
	return s.gate.AssertVisible(ctx, file.ObjectType, file.ObjectID)
}

// toFileResp 补全单个附件出参（含上传者姓名与直链）。
func (s *FileService) toFileResp(ctx context.Context, f *model.File) dto.FileResp {
	out := toFileRespList([]model.File{*f})[0]
	if name, err := s.users.NameMap(ctx, []uint{f.UserID}); err == nil {
		out.UserName = name[f.UserID]
	}
	// 支持直链的后端（如配了 CDN 的 S3）给个 URL；本地后端返回空串，
	// 前端据此回落到带鉴权的下载接口。
	if s.backend != nil {
		if url, err := s.backend.URL(ctx, f.Path); err == nil {
			out.URL = url
		}
	}
	return out
}

// toFileResps 补全一批附件的出参。
func (s *FileService) toFileResps(ctx context.Context, files []model.File) []dto.FileResp {
	out := toFileRespList(files)
	if len(files) == 0 {
		return out
	}
	ids := make([]uint, 0, len(files))
	for i := range files {
		ids = append(ids, files[i].UserID)
	}
	names, err := s.users.NameMap(ctx, ids)
	if err != nil {
		return out
	}
	for i := range out {
		out[i].UserName = names[out[i].UserID]
	}
	return out
}

// uploadError 把 storage 的校验错误映射成带错误码的应用错误。
//
// storage 包只返回人类可读的原因字符串（它不认识 apperr），
// 由这里补上错误码 —— 错误码是对外契约，文案只是给人看的。
func uploadError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "为空"):
		return apperr.Wrap(apperr.FileEmpty, err)
	case strings.Contains(msg, "超出限制"):
		return apperr.Wrapf(apperr.FileTooLarge, nil, "%s", msg)
	case strings.Contains(msg, "不支持的文件类型"):
		return apperr.Wrapf(apperr.FileTypeNotAllowed, nil, "%s", msg)
	default:
		return apperr.Wrapf(apperr.InvalidParam, err, "%s", msg)
	}
}

// sanitizeFileName 清洗用户提供的文件名，只保留纯文件名部分。
//
// 去掉目录成分（Windows 客户端可能带完整路径）与控制字符：
// 这个值会进数据库、可能回显到页面，所以在这一层就定死它的形状。
func sanitizeFileName(name string) string {
	// 统一分隔符后用 path.Base 取末段，跨平台一致。
	cleaned := strings.ReplaceAll(name, "\\", "/")
	cleaned = path.Base(cleaned)
	if cleaned == "." || cleaned == "/" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(cleaned))
	for _, r := range cleaned {
		// 丢弃控制字符：它们在文件名里没有正当用途，
		// 却能让日志与页面显示变得难以预料。
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 255 {
		// 与模型的 size:255 对齐，避免超长名字在入库时才报错。
		out = out[:255]
	}
	return out
}

// extensionOfName 取小写扩展名（含点）；没有扩展名返回空串。
func extensionOfName(name string) string {
	idx := strings.LastIndexByte(name, '.')
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx:])
}

// guessContentType 按扩展名推断 MIME 类型。
//
// 只覆盖常见的几类：前端附件大多是文档、图片与压缩包，
// 引入完整的 mime 表只会增加维护面。推断不出来时给通用二进制类型，
// 浏览器会按下载处理。
func guessContentType(name string) string {
	switch extensionOfName(name) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md", ".log", ".csv":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".zip":
		return "application/zip"
	case ".gz", ".tar":
		return "application/gzip"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	default:
		return "application/octet-stream"
	}
}

// humanSize 把字节数格式化为人读大小。
func humanSize(size int64) string { return storage.HumanSize(size) }

// isSuperAdmin 报告当前请求的用户是否为超级管理员。
func isSuperAdmin(ctx context.Context) bool {
	user, ok := identity.CurrentUser(ctx)
	return ok && user.IsSuper
}
