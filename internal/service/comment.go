package service

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// CommentService 负责业务对象上的备注/评论。
//
// 评论挂在任意对象上（多态双列），所以它不能依赖某个具体 service 来
// 判断可见性 —— 统一走 Gate。
type CommentService struct {
	db       *gorm.DB
	comments *repository.CommentRepo
	files    *repository.FileRepo
	users    *repository.UserRepo
	gate     *Gate
	recorder *history.Recorder
}

// NewCommentService 构造评论服务。
func NewCommentService(
	db *gorm.DB,
	comments *repository.CommentRepo,
	files *repository.FileRepo,
	users *repository.UserRepo,
	gate *Gate,
) *CommentService {
	return &CommentService{
		db: db, comments: comments, files: files, users: users, gate: gate,
		recorder: history.New(nil),
	}
}

// List 分页查询某对象下的评论。
func (s *CommentService) List(ctx context.Context, req dto.CommentFilterReq) (*pagination.Result[dto.CommentResp], error) {
	if err := s.gate.AssertVisible(ctx, req.ObjectType, req.ObjectID); err != nil {
		return nil, err
	}

	result, err := s.comments.ListByObject(ctx, req.ObjectType, req.ObjectID,
		pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order})
	if err != nil {
		return nil, err
	}

	// 一次取回全部作者，避免逐条查库。
	userIDs := make([]uint, 0, len(result.List))
	for i := range result.List {
		userIDs = append(userIDs, result.List[i].UserID)
	}
	users, err := s.users.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	userMap := make(map[uint]model.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}

	out := make([]dto.CommentResp, 0, len(result.List))
	for i := range result.List {
		c := &result.List[i]
		resp := dto.CommentResp{
			ID: c.ID, ObjectType: c.ObjectType, ObjectID: c.ObjectID,
			UserID: c.UserID, Content: c.Content,
			CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		}
		if u, ok := userMap[c.UserID]; ok {
			resp.UserName = u.DisplayName()
			resp.Avatar = u.Avatar
		}
		if ids, _ := decodeIDs(c.Files); len(ids) > 0 {
			if files, err := s.files.ListByIDs(ctx, ids); err == nil {
				resp.Files = toFileRespList(files)
			}
		}
		out = append(out, resp)
	}

	return &pagination.Result[dto.CommentResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// Create 发表评论。
//
// 评论一律要求「能看到被评论的对象」：一则留言里往往带出业务信息
// （「验收标准还得再改」已经说明了需求的主题与状态），对看不到该产品的
// 用户，连这条记录存在都不该让他知道。
func (s *CommentService) Create(ctx context.Context, req dto.CommentReq) (*dto.CommentResp, error) {
	if err := s.gate.AssertVisible(ctx, req.ObjectType, req.ObjectID); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, apperr.New(apperr.CommentEmpty)
	}

	actor := currentUserID(ctx)
	encodedFiles, err := encodeIDs(req.Files)
	if err != nil {
		return nil, err
	}

	comment := &model.Comment{
		ObjectType: req.ObjectType, ObjectID: req.ObjectID,
		UserID: actor, Content: content, Files: encodedFiles,
	}

	// 评论记录冗余一份归属信息（产品/项目/迭代），让「某项目下的
	// 全部评论」这类查询免去 join。定位失败说明对象不存在，直接报错。
	loc, err := s.gate.Locate(ctx, req.ObjectType, req.ObjectID)
	if err != nil {
		return nil, err
	}
	productID := loc.ProductID

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.comments.Create(ctx, tx, comment); err != nil {
			return err
		}
		// 评论附件挂在被评论的对象上，而不是评论自己：
		// 这样需求详情页的附件列表与评论里看到的是一致的，
		// 不会出现「评论里有的文件在附件区找不到」。
		if len(req.Files) > 0 {
			if err := s.files.BindToObject(ctx, tx, req.Files, req.ObjectType, req.ObjectID); err != nil {
				return err
			}
		}
		// 评论也进操作历史：动态流里「谁说了什么」与「谁改了什么」同样重要。
		_, err := s.recorder.Record(ctx, tx, history.Entry{
			ObjectType: req.ObjectType,
			ObjectID:   req.ObjectID,
			ProductID:  productID,
			ActorID:    actor,
			Action:     model.ActionCommented,
			Comment:    content,
			Files:      req.Files,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	// 回填作者信息，让前端不必再发一次请求就能把新评论渲染出来。
	user, err := s.users.FindByID(ctx, actor)
	resp := &dto.CommentResp{
		ID: comment.ID, ObjectType: comment.ObjectType, ObjectID: comment.ObjectID,
		UserID: actor, Content: comment.Content,
		CreatedAt: comment.CreatedAt.UTC().Format(time.RFC3339),
	}
	if err == nil && user != nil {
		resp.UserName = user.DisplayName()
		resp.Avatar = user.Avatar
	}
	if len(req.Files) > 0 {
		if files, err := s.files.ListByIDs(ctx, req.Files); err == nil {
			resp.Files = toFileRespList(files)
		}
	}
	return resp, nil
}

// Delete 删除评论。
//
// 只有作者本人或超级管理员能删：评论是「谁说了什么」的记录，
// 让旁人随意删除等于篡改讨论过程。
func (s *CommentService) Delete(ctx context.Context, id uint) error {
	comment, err := s.comments.GetByID(ctx, id)
	if err != nil {
		return err
	}

	current := currentUserID(ctx)
	if comment.UserID != current && !isSuperAdmin(ctx) {
		return apperr.Newf(apperr.Forbidden, "只能删除自己发表的评论")
	}
	if err := s.gate.AssertVisible(ctx, comment.ObjectType, comment.ObjectID); err != nil {
		return err
	}

	return s.comments.Delete(ctx, nil, id)
}

// toFileRespList 把附件模型批量转为出参（不含上传者姓名，调用方按需补）。
func toFileRespList(files []model.File) []dto.FileResp {
	out := make([]dto.FileResp, 0, len(files))
	for i := range files {
		f := &files[i]
		out = append(out, dto.FileResp{
			ID: f.ID, Name: f.Name, Size: f.Size,
			SizeText:    humanSize(f.Size),
			Ext:         extensionOfName(f.Name),
			ContentType: f.ContentType,
			Downloads:   f.Downloads,
			UserID:      f.UserID,
			CreatedAt:   f.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}
