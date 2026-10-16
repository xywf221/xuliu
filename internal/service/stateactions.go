package service

import (
	"context"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/workflow"
)

// availableStateActions 生成某状态下可用的动作列表。
//
// 无权限的动作也一并下发（Enabled=false）：前端的做法是把按钮置灰并
// 提示「需要某某权限」，比直接消失更容易让人明白为什么点不了。
//
// 这套数据与后端的状态判定同源（都来自 workflow.Machine），
// 所以不会出现「前端显示能点、后端却拒绝」这种前后端规则漂移。
func availableStateActions(ctx context.Context, m *workflow.Machine, status string) []dto.TransitionResp {
	user, ok := identity.CurrentUser(ctx)
	if !ok {
		return nil
	}

	transitions := m.Available(status)
	out := make([]dto.TransitionResp, 0, len(transitions))
	for _, a := range transitions {
		// create 不是「对已存在对象」的动作，不该出现在详情页按钮里；
		// edit/assign 属于表单而非状态流转，前端从别处渲染。
		if a.Kind == string(workflow.KindCreate) {
			continue
		}
		out = append(out, dto.TransitionResp{
			Action:  a.Action,
			Label:   a.Label,
			Perm:    a.Perm,
			To:      a.To,
			Kind:    a.Kind,
			Danger:  a.Danger,
			Enabled: user.Has(a.Perm),
		})
	}
	return out
}
