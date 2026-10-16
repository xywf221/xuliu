package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

// ActionService 负责操作历史的读取与展示。
//
// 写入由各业务 service 在事务里直接调 history.Recorder 完成（历史必须与
// 业务数据同生共死，所以不能等到事务提交后再补写）。本服务只做读侧：
// 把存储的原始数据补全成前端能直接渲染的时间线。
type ActionService struct {
	actions *repository.ActionRepo
	users   *repository.UserRepo
	files   *repository.FileRepo
	gate    *Gate
	db      *gorm.DB
}

// NewActionService 构造操作历史服务。
func NewActionService(
	db *gorm.DB,
	actions *repository.ActionRepo,
	users *repository.UserRepo,
	files *repository.FileRepo,
	gate *Gate,
) *ActionService {
	return &ActionService{db: db, actions: actions, users: users, files: files, gate: gate}
}

// List 分页查询操作历史。
//
// 三点约束：
//
//  1. 必须给出一个查询范围，不允许无条件下全表扫；
//  2. 按对象查时先验该对象的可见性（动态里带着标题、备注这类业务内容，
//     对一个看不到该对象的人，连「存在这条动态」都不该知道）；
//  3. 其余的按「可见产品 / 可见项目」过滤。少了第三点，任何人都能用
//     ?actorId=<别人> 把别人的操作记录（连同备注与字段变更）翻出来 ——
//     这是我在 M5 做时间线时补上的一道口子。
func (s *ActionService) List(ctx context.Context, req dto.ActionFilterReq) (*pagination.Result[dto.ActionResp], error) {
	if req.ObjectType != "" && req.ObjectID > 0 {
		if err := s.gate.AssertVisible(ctx, req.ObjectType, req.ObjectID); err != nil {
			return nil, err
		}
	}
	if req.ObjectType == "" && req.ObjectID == 0 &&
		req.ProductID == 0 && req.ProjectID == 0 && req.ExecutionID == 0 && req.ActorID == 0 {
		return nil, apperr.Newf(apperr.InvalidParam,
			"请至少指定一个查询范围（objectType+objectID / productId / projectId / executionId / actorId）")
	}

	visibleProducts, err := s.gate.VisibleProducts(ctx)
	if err != nil {
		return nil, err
	}
	visibleProjects, err := s.gate.VisibleProjects(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.actions.List(ctx, repository.ActionFilter{
		ObjectType:        req.ObjectType,
		ObjectID:          req.ObjectID,
		ProductID:         req.ProductID,
		ProjectID:         req.ProjectID,
		ExecutionID:       req.ExecutionID,
		ActorID:           req.ActorID,
		VisibleProductIDs: visibleProducts,
		VisibleProjectIDs: visibleProjects,
		Pagination:        pagination.Params{Page: req.Page, Size: req.Size, Order: req.Order},
	})
	if err != nil {
		return nil, err
	}

	// 一次取回全部操作者与全部附件，避免逐条查库。
	userIDs := make([]uint, 0, len(result.List))
	fileIDs := make([]uint, 0)
	for i := range result.List {
		userIDs = append(userIDs, result.List[i].ActorID)
		if ids, _ := history.DecodeIDs(result.List[i].Files); len(ids) > 0 {
			fileIDs = append(fileIDs, ids...)
		}
	}
	users, err := s.users.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	userMap := make(map[uint]model.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	fileMap := map[uint]model.File{}
	if len(fileIDs) > 0 {
		files, err := s.files.ListByIDs(ctx, fileIDs)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			fileMap[f.ID] = f
		}
	}

	out := make([]dto.ActionResp, 0, len(result.List))
	for i := range result.List {
		a := &result.List[i]
		resp := dto.ActionResp{
			ID: a.ID, ObjectType: a.ObjectType, ObjectID: a.ObjectID,
			ActorID: a.ActorID,
			Action:  a.Action, ActionText: actionVerbLabels[a.Action],
			Date:    a.Date.UTC().Format(time.RFC3339),
			Comment: a.Comment, Extra: a.Extra,
		}
		if u, ok := userMap[a.ActorID]; ok {
			resp.ActorName = u.DisplayName()
			resp.Avatar = u.Avatar
		}
		if resp.ActionText == "" {
			// 没登记的动词（如将来新增的动作）至少显示原始值，
			// 让时间线不至于出现空白的一行。
			resp.ActionText = a.Action
		}

		if changes, err := history.DecodeChanges(a.Changes); err == nil && len(changes) > 0 {
			resp.Changes = make([]dto.ActionChangeResp, 0, len(changes))
			for _, c := range changes {
				resp.Changes = append(resp.Changes, dto.ActionChangeResp{
					Field: c.Field, Label: c.Label,
					Old: c.Old, New: c.New,
					OldText: c.OldText, NewText: c.NewText,
					Text: history.DescribeChange(c),
				})
			}
		}

		if ids, _ := history.DecodeIDs(a.Files); len(ids) > 0 {
			for _, fid := range ids {
				if f, ok := fileMap[fid]; ok {
					resp.Files = append(resp.Files, toFileRespList([]model.File{f})[0])
				}
			}
		}
		out = append(out, resp)
	}

	return &pagination.Result[dto.ActionResp]{
		List: out, Total: result.Total, Page: result.Page, Size: result.Size,
	}, nil
}

// actionVerbLabels 是操作动词的中文名。
//
// 对照 model.ActionXxx 常量。放在这里而不是 model 包：
// 它是展示文案，模型包里放的是数据契约。
var actionVerbLabels = map[string]string{
	model.ActionOpened:       "创建",
	model.ActionEdited:       "修改",
	model.ActionAssigned:     "指派",
	model.ActionClosed:       "关闭",
	model.ActionActivated:    "激活",
	model.ActionDeleted:      "删除",
	model.ActionUndeleted:    "还原",
	model.ActionCommented:    "备注",
	model.ActionResolved:     "解决",
	model.ActionReviewed:     "评审",
	model.ActionSubmitReview: "提交评审",
	model.ActionRecalled:     "撤销评审",
	model.ActionFinished:     "完成",
	model.ActionStarted:      "开始",
	model.ActionPaused:       "暂停",
	model.ActionRestarted:    "继续",
	model.ActionCanceled:     "取消",
	model.ActionConfirmed:    "确认",
	model.ActionVerified:     "验收",
	model.ActionLinked:       "关联",
	model.ActionUnlinked:     "取消关联",
	model.ActionRecordWork:   "登记工时",
	model.ActionRestored:     "还原",
	model.ActionMoved:        "调整模块",
}
