package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// notifier 是「给某个人发一条站内通知」这件事的最小依赖。
//
// 定义成接口而不是直接依赖 *NoticeService：任务、需求、缺陷三个服务
// 只需要这一个能力，而窄接口让「谁在什么时候通知了谁」在签名上一眼可见，
// 单测里也能用几行假实现替换掉。
type notifier interface {
	Send(ctx context.Context, tx *gorm.DB, to, from uint, objectType string, objectID uint,
		title, content string) error
}

// NoticeService 负责站内通知的读写。
//
// 通知的语义是**待办**而不是流水：同一个对象上已有的未读通知会被
// 新的覆盖（见 NoticeRepo.UpsertUnread）。理由是一条缺陷被来回指派
// 五次时，收件人需要知道的是「现在该我处理它了」，而不是收到五条
// 一模一样的提醒；要看完整的来龙去脉有操作历史那条线。
//
// 触发规则也据此定：**通知下一个要动手的人**。
// 谁被指派给谁、谁解决完等谁验证、谁提交评审等谁给结论 ——
// 只有"下一步卡在谁手上"才发通知，其余的动作留给时间线。
type NoticeService struct {
	db      *gorm.DB
	notices *repository.NoticeRepo
	users   *repository.UserRepo
}

// NewNoticeService 构造通知服务。
func NewNoticeService(db *gorm.DB, notices *repository.NoticeRepo, users *repository.UserRepo) *NoticeService {
	return &NoticeService{db: db, notices: notices, users: users}
}

// Send 写一条通知。
//
// 几个刻意的判断都集中在这里，业务侧就不必各写一遍：
//   - 收件人为 0（没人可通知）或就是操作者本人：直接不发；
//   - 同一个对象上已有的未读会被覆盖，不追加。
func (s *NoticeService) Send(
	ctx context.Context, tx *gorm.DB, to, from uint,
	objectType string, objectID uint, title, content string,
) error {
	if to == 0 || to == from {
		return nil
	}
	notice := &model.Notice{
		ToUser: to, FromUser: from,
		ObjectType: objectType, ObjectID: objectID,
		Title: title, Content: content,
		Link:      objectPath(objectType, objectID),
		CreatedAt: time.Now().UTC(),
	}
	return s.notices.UpsertUnread(ctx, tx, notice)
}

// objectPathSegments 是对象类型到前端路径段的映射。
//
// 用显式映射而不是给类型名加个 s：story 的复数是 stories，
// 拼出来的是 storys —— 一个必然 404 的链接，而且不会有任何测试发现它。
var objectPathSegments = map[string]string{
	model.ObjectTypeStory:     "stories",
	model.ObjectTypeTask:      "tasks",
	model.ObjectTypeBug:       "bugs",
	model.ObjectTypeProject:   "projects",
	model.ObjectTypeExecution: "executions",
	model.ObjectTypeProduct:   "products",
	model.ObjectTypePlan:      "plans",
	model.ObjectTypeModule:    "modules",
}

// objectPath 生成前端跳转路径。
//
// 路径规则由后端给定、前端照着实现：把「点通知该去哪」写进通知里，
// 前端就不必为每种对象类型再维护一份映射表。认不出的类型不给链接，
// 好过给一个一定打不开的地址。
func objectPath(objectType string, objectID uint) string {
	segment, ok := objectPathSegments[objectType]
	if !ok || objectID == 0 {
		return ""
	}
	return "/" + segment + "/" + itoa(objectID)
}

// ---------------------------------------------------------------------------
// 读侧
// ---------------------------------------------------------------------------

// List 查询我的通知。
func (s *NoticeService) List(ctx context.Context, req dto.NoticeFilterReq) (*pagination.Result[dto.NoticeResp], error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}

	result, err := s.notices.List(ctx, repository.NoticeFilter{
		ToUser:     userID,
		ObjectType: req.ObjectType,
		ObjectID:   req.ObjectID,
		Unread:     req.Unread,
		Keywords:   req.Keywords,
		Pagination: pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}
	return s.decorate(ctx, result)
}

// UnreadCount 返回我的未读通知数。
func (s *NoticeService) UnreadCount(ctx context.Context) (*dto.NoticeCountResp, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}
	count, err := s.notices.CountUnread(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &dto.NoticeCountResp{Unread: count}, nil
}

// MarkRead 标记已读（指定若干条，或全部）。
func (s *NoticeService) MarkRead(ctx context.Context, req dto.NoticeReadReq) (*dto.NoticeCountResp, error) {
	userID := currentUserID(ctx)
	if userID == 0 {
		return nil, apperr.New(apperr.Unauthorized)
	}
	now := time.Now().UTC()

	if !req.All && len(req.IDs) == 0 {
		return nil, apperr.Newf(apperr.InvalidParam, "请指定要标记的通知，或传 all=true 全部标记")
	}
	if req.All {
		if _, err := s.notices.MarkAllRead(ctx, nil, userID, now); err != nil {
			return nil, err
		}
	} else if _, err := s.notices.MarkRead(ctx, nil, userID, req.IDs, now); err != nil {
		return nil, err
	}
	return s.UnreadCount(ctx)
}

// Delete 删除我的一条通知。
func (s *NoticeService) Delete(ctx context.Context, id uint) error {
	userID := currentUserID(ctx)
	if userID == 0 {
		return apperr.New(apperr.Unauthorized)
	}
	notice, err := s.notices.FindByID(ctx, id)
	if err != nil {
		return err
	}
	// 不区分「不存在」与「不是你的」：报 404 已经足够说明问题，
	// 而区分开等于告诉别人「这条通知存在，只是不属于你」。
	if notice == nil || notice.ToUser != userID {
		return apperr.New(apperr.NotFound)
	}
	return s.notices.Delete(ctx, nil, id)
}

// decorate 补上触发者姓名与被通知对象的标题。
func (s *NoticeService) decorate(
	ctx context.Context, result *pagination.Result[model.Notice],
) (*pagination.Result[dto.NoticeResp], error) {
	if len(result.List) == 0 {
		return &pagination.Result[dto.NoticeResp]{
			List: []dto.NoticeResp{}, Total: result.Total, Page: result.Page, Size: result.Size,
		}, nil
	}

	fromIDs := make([]uint, 0, len(result.List))
	for i := range result.List {
		fromIDs = append(fromIDs, result.List[i].FromUser)
	}
	names, err := s.users.NameMap(ctx, fromIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.NoticeResp, 0, len(result.List))
	for i := range result.List {
		n := &result.List[i]
		resp := dto.NoticeResp{
			ID: n.ID, FromUser: n.FromUser, FromName: names[n.FromUser],
			ObjectType: n.ObjectType, ObjectID: n.ObjectID,
			Title: n.Title, Content: n.Content, Link: n.Link,
			Read: n.Read, CreatedAt: n.CreatedAt.UTC().Format(time.RFC3339),
		}
		if n.ReadAt != nil {
			resp.ReadAt = n.ReadAt.UTC().Format(time.RFC3339)
		}
		out = append(out, resp)
	}
	return &pagination.Result[dto.NoticeResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}
