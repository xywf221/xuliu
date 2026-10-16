package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/repository"
)

// teamOps 是项目团队与迭代团队的公共实现。
//
// 两者结构完全一致（同一张 pm_team，靠 root_type 区分），
// 所以「校验归属对象 → 查/加/改/移成员」这一套只写一遍。
// 差异只有两处，都用 rootType 分支表达了：
//
//   - 迭代团队成员必须先属于其项目团队（见 add）；
//   - 「未完成的任务」按不同列统计（project_id vs execution_id）。
//
// 关于迭代团队为空：那表示「沿用项目团队」，而不是「没人可用」。
// 迭代团队只在需要更小范围时才填，这样可以避免每个迭代都重复维护
// 一份项目团队的副本。
type teamOps struct {
	teams      *repository.TeamRepo
	users      *repository.UserRepo
	projects   *repository.ProjectRepo
	executions *repository.ExecutionRepo
	tasks      *repository.TaskRepo
	gate       *Gate
}

// assertRoot 校验团队归属的对象存在且当前用户可见。
func (t *teamOps) assertRoot(ctx context.Context, rootType string, rootID uint) error {
	switch rootType {
	case model.TeamRootProject:
		if _, err := t.projects.GetByID(ctx, rootID); err != nil {
			return err
		}
		return t.gate.AssertProjectVisible(ctx, rootID)
	case model.TeamRootExecution:
		if _, err := t.executions.GetByID(ctx, rootID); err != nil {
			return err
		}
		return t.gate.AssertExecutionVisible(ctx, rootID)
	default:
		return apperr.Newf(apperr.Internal, "未知的团队归属类型：%s", rootType)
	}
}

// list 返回团队成员的出参列表。
func (t *teamOps) list(ctx context.Context, rootType string, rootID uint) ([]dto.TeamMemberResp, error) {
	if err := t.assertRoot(ctx, rootType, rootID); err != nil {
		return nil, err
	}
	members, err := t.teams.ListByRoot(ctx, rootType, rootID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []dto.TeamMemberResp{}, nil
	}

	userIDs := make([]uint, 0, len(members))
	for _, m := range members {
		userIDs = append(userIDs, m.UserID)
	}
	users, err := t.users.ListByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	info := make(map[uint]model.User, len(users))
	for _, u := range users {
		info[u.ID] = u
	}
	counts, err := t.taskCounts(ctx, rootType, rootID, userIDs)
	if err != nil {
		return nil, err
	}

	out := make([]dto.TeamMemberResp, 0, len(members))
	for _, m := range members {
		u := info[m.UserID]
		out = append(out, dto.TeamMemberResp{
			UserID: m.UserID, Account: u.Account, Realname: u.Realname,
			Role: m.Role, Position: m.Position,
			Days: m.Days, Hours: m.Hours,
			Estimate: m.Estimate, Consumed: m.Consumed, Left: m.Left,
			Join: formatTimeValue(m.Join), Order: m.Order,
			TaskCount: counts[m.UserID],
		})
	}
	return out, nil
}

// memberRows 批量取多个团队的全部成员行。
func (t *teamOps) memberRows(ctx context.Context, rootType string, rootIDs []uint) (map[uint][]model.Team, error) {
	rows, err := t.teams.ListByRoots(ctx, rootType, rootIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uint][]model.Team, len(rootIDs))
	for _, r := range rows {
		out[r.RootID] = append(out[r.RootID], r)
	}
	return out, nil
}

// taskCounts 统计每个成员在该项目/迭代下的任务数。
func (t *teamOps) taskCounts(ctx context.Context, rootType string, rootID uint, userIDs []uint) (map[uint]int64, error) {
	if len(userIDs) == 0 {
		return map[uint]int64{}, nil
	}
	q := t.tasks.DB.WithContext(ctx).Model(&model.Task{}).
		Select("assigned_to AS assigned_to, COUNT(*) AS total").
		Where("assigned_to IN ?", userIDs)
	if rootType == model.TeamRootExecution {
		q = q.Where("execution_id = ?", rootID)
	} else {
		q = q.Where("project_id = ?", rootID)
	}

	var rows []struct {
		AssignedTo uint
		Total      int64
	}
	if err := q.Group("assigned_to").Scan(&rows).Error; err != nil {
		return nil, apperr.Wrap(apperr.Internal, err)
	}
	out := make(map[uint]int64, len(rows))
	for _, row := range rows {
		out[row.AssignedTo] = row.Total
	}
	return out, nil
}

// add 往团队里加一个成员。
func (t *teamOps) add(ctx context.Context, rootType string, rootID uint, req dto.TeamMemberReq) error {
	if err := t.assertRoot(ctx, rootType, rootID); err != nil {
		return err
	}
	if err := t.assertUserExists(ctx, req.UserID); err != nil {
		return err
	}
	if err := t.assertParentMember(ctx, rootType, rootID, req.UserID); err != nil {
		return err
	}

	members, err := t.teams.MemberIDs(ctx, rootType, rootID)
	if err != nil {
		return err
	}
	for _, id := range members {
		if id == req.UserID {
			return apperr.New(apperr.TeamMemberExists)
		}
	}

	now := time.Now().UTC()
	return t.teams.Add(ctx, nil, rootType, rootID, []model.Team{{
		UserID: req.UserID, Role: req.Role, Position: req.Position,
		Days: req.Days, Hours: req.Hours, Estimate: req.Estimate,
		Join: &now, Order: req.Order,
	}})
}

// update 更新成员在团队里的信息。
func (t *teamOps) update(ctx context.Context, rootType string, rootID, userID uint, req dto.TeamMemberReq) error {
	if err := t.assertRoot(ctx, rootType, rootID); err != nil {
		return err
	}
	return t.teams.UpdateMember(ctx, nil, rootType, rootID, userID, map[string]any{
		"role": req.Role, "position": req.Position,
		"days": req.Days, "hours": req.Hours,
		"estimate": req.Estimate, "order": req.Order,
	})
}

// remove 把成员移出团队。
//
// 身上还挂着未完成任务的人不能直接移出：那些任务会失去负责人，
// 变成没人认领的悬空工作。要么先转派，要么先把任务关掉。
func (t *teamOps) remove(ctx context.Context, rootType string, rootID, userID uint) error {
	if err := t.assertRoot(ctx, rootType, rootID); err != nil {
		return err
	}

	q := t.tasks.DB.WithContext(ctx).Model(&model.Task{}).
		Where("assigned_to = ? AND status IN ?", userID, model.TaskUnfinishedStatuses)
	if rootType == model.TeamRootExecution {
		q = q.Where("execution_id = ?", rootID)
	} else {
		q = q.Where("project_id = ?", rootID)
	}
	var count int64
	if err := q.Count(&count).Error; err != nil {
		return apperr.Wrap(apperr.Internal, err)
	}
	if count > 0 {
		return apperr.Newf(apperr.Conflict,
			"该成员名下还有 %d 个未完成的任务，请先转派或关闭", count)
	}
	return t.teams.Remove(ctx, nil, rootType, rootID, userID)
}

// assertMembers 校验一批成员的用户都存在。
func (t *teamOps) assertMembers(ctx context.Context, members []dto.TeamMemberReq) error {
	for _, m := range members {
		if err := t.assertUserExists(ctx, m.UserID); err != nil {
			return err
		}
	}
	return nil
}

// addOwnersAndMembers 在创建/编辑时把成员批量写进团队。
//
// owners 传 nil 表示「只加 req 里明列的人」，用于编辑场景 ——
// 负责人在编辑时可能已经变了，那属于「设置负责人」而不是「加成员」，
// 由专门的入口处理，不在这里顺手塞进去。
func (t *teamOps) addOwnersAndMembers(
	ctx context.Context, tx *gorm.DB,
	rootType string, rootID uint,
	members []dto.TeamMemberReq, owners []model.Team,
) error {
	now := time.Now().UTC()
	rows := make([]model.Team, 0, len(members)+len(owners))
	for _, m := range members {
		rows = append(rows, model.Team{
			UserID: m.UserID, Role: m.Role, Position: m.Position,
			Days: m.Days, Hours: m.Hours, Estimate: m.Estimate,
			Join: &now, Order: m.Order,
		})
	}
	rows = append(rows, owners...)
	return t.teams.Add(ctx, tx, rootType, rootID, rows)
}

// assertParentMember 校验迭代团队成员属于其项目团队。
func (t *teamOps) assertParentMember(ctx context.Context, rootType string, rootID, userID uint) error {
	if rootType != model.TeamRootExecution {
		return nil
	}
	execution, err := t.executions.FindByID(ctx, rootID)
	if err != nil {
		return err
	}
	if execution == nil {
		return apperr.New(apperr.ExecutionNotFound)
	}
	ok, err := t.teams.IsMember(ctx, model.TeamRootProject, execution.ProjectID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Newf(apperr.TeamMemberNotInProject, "该成员不是项目团队成员，请先加入项目团队")
	}
	return nil
}

// assertUserExists 校验用户存在。
func (t *teamOps) assertUserExists(ctx context.Context, userID uint) error {
	if userID == 0 {
		return apperr.Newf(apperr.InvalidParam, "请选择成员")
	}
	user, err := t.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperr.New(apperr.UserNotFound)
	}
	return nil
}

// EffectiveMemberIDs 返回某个迭代实际可用的团队成员。
//
// 迭代团队为空时回落到项目团队 —— 见 teamOps 的说明。
func (t *teamOps) EffectiveMemberIDs(ctx context.Context, projectID, executionID uint) ([]uint, error) {
	if executionID > 0 {
		members, err := t.teams.MemberIDs(ctx, model.TeamRootExecution, executionID)
		if err != nil {
			return nil, err
		}
		if len(members) > 0 {
			return members, nil
		}
	}
	return t.teams.MemberIDs(ctx, model.TeamRootProject, projectID)
}
