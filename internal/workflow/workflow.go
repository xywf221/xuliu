// Package workflow 用声明式转移表描述需求/任务/缺陷的生命周期。
//
// 为什么需要它：这三种实体各有 5~7 个状态和 6~8 个动作，如果把
// 「当前状态是否允许该动作」写成散落在 service 里的 switch，规则会迅速
// 失同步（前端显示能点，后端却拒绝），而且无法回答「这个状态下我还能做什么」。
//
// 本包负责三件确定性的事：
//
//  1. 合法的 (状态, 动作) → 目标状态 映射，非法组合返回明确的错误码；
//  2. 每个动作所需的权限点，供中间件与前端共用同一份事实；
//  3. 当前状态下可用的动作列表，前端据此渲染按钮，不会与后端判定漂移。
//
// **不负责**具体字段的写入。字段赋值与时间戳是各实体特有的、
// 且需要读业务数据的（如解决缺陷时要写 resolution），留在 service 里更直白。
// 本包提供「这个动作合法吗、要什么权限、会变成什么状态」，
// service 提供「变成那个状态具体要改哪些列」。
package workflow

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// 动作类型。决定 service 侧要写哪一组审计字段。
type Kind string

const (
	// KindCreate 创建。
	KindCreate Kind = "create"
	// KindEdit 编辑内容（不改状态）。
	KindEdit Kind = "edit"
	// KindStatus 改变状态，是本包的核心。
	KindStatus Kind = "status"
	// KindAssign 指派，改负责人但不改状态。
	KindAssign Kind = "assign"
	// KindComment 备注。
	KindComment Kind = "comment"
)

// Transition 描述一个合法流转。
type Transition struct {
	// Action 是动作名，如 "resolve"。对外 API 里的 action 参数即此值。
	Action string
	// From 是允许的来源状态。为空表示不限制来源。
	From []string
	// To 是目标状态。空表示不改状态（如「指派」）。
	To string
	// Perm 是所需权限点。
	Perm string
	// Label 是中文动作名，前端直接用作按钮文案。
	Label string
	// Kind 决定写入哪组审计字段。
	Kind Kind
	// LogAction 是记入操作历史的动词，取值见 model.ActionXxx。
	LogAction string
	// Danger 标记破坏性或不可逆动作，前端可用红色按钮区分。
	Danger bool
}

// allows 报告该流转是否允许从 from 状态出发。
func (t Transition) allows(from string) bool {
	if len(t.From) == 0 {
		return true
	}
	for _, s := range t.From {
		if s == from {
			return true
		}
	}
	return false
}

// Machine 是一种实体的状态机。
type Machine struct {
	// ObjectType 是对象类型，用于操作历史与错误文案。
	ObjectType string
	// ErrorCode 是状态不允许时报出的错误码（如 bug-status-invalid）。
	//
	// 放在这里而不是让每个 service 自己判断，是因为「该报哪个码」
	// 恰恰是状态机自己的知识；散到各处只会出现同一类误操作在不同接口
	// 报出不同码的情况。
	ErrorCode apperr.Code
	// Statuses 是全部合法状态，用于校验初始值与展示。
	Statuses []string
	// StatusLabels 是状态的中文名，供前端展示。
	StatusLabels map[string]string
	// Transitions 按动作名索引。用 map 而非 slice，因为查询始终按动作名。
	Transitions map[string]Transition
}

// Transition 返回指定动作的流转定义，不存在则 ok 为 false。
func (m *Machine) Transition(action string) (Transition, bool) {
	t, ok := m.Transitions[action]
	return t, ok
}

// Canonical 校验并归一化动作名。
//
// 允许大小写与连字符/下划线的差异（"submit-review"、"SubmitReview"
// 都归一为 "submitreview"），因为前端不同地方的写法容易不一致。
//
// 返回 (归一化后的动作名, 是否已登记)。归一化后仍未命中就是动作不存在——
// 这里不做拼写容错，否则前端把动作名拼错也会静默通过。
func (m *Machine) Canonical(action string) (string, bool) {
	normalized := normalizeAction(action)
	_, ok := m.Transitions[normalized]
	return normalized, ok
}

// normalizeAction 把动作名归一为「小写、去掉分隔符」。
func normalizeAction(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == '-' || r == '_' || r == ' ':
			// 丢弃分隔符
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// StatusError 描述一次非法的状态流转。
type StatusError struct {
	ObjectType string
	Action     string
	// Label 是动作的中文名，用于拼出可读的报错。
	Label string
	From  string
	// AllowedFrom 是当前动作允许的来源状态，供错误文案提示「现在能做什么」。
	AllowedFrom []string
	// Unknown 为 true 表示动作名本身不存在（而不是状态不允许）。
	Unknown bool
}

func (e *StatusError) Error() string {
	if e.Unknown {
		return fmt.Sprintf("%s: 不支持的动作 %q", e.ObjectType, e.Action)
	}
	return fmt.Sprintf("%s: 当前状态 %q 不允许执行 %q（允许的来源状态：%s）",
		e.ObjectType, e.From, e.Action, strings.Join(e.AllowedFrom, ", "))
}

// Resolve 校验 (from, action) 组合并返回对应的流转。
//
// 返回的 *StatusError 携带足够信息让上层翻译成面向用户的文案，
// 并区分「动作不存在」与「当前状态不允许」这两种不同的错误码。
func (m *Machine) Resolve(action, from string) (Transition, *StatusError) {
	canonical, ok := m.Canonical(action)
	if !ok {
		return Transition{}, &StatusError{
			ObjectType: m.ObjectType,
			Action:     action,
			From:       from,
			Unknown:    true,
		}
	}

	t := m.Transitions[canonical]
	if !t.allows(from) {
		return Transition{}, &StatusError{
			ObjectType:  m.ObjectType,
			Action:      canonical,
			Label:       t.Label,
			From:        from,
			AllowedFrom: t.From,
		}
	}
	return t, nil
}

// DescribeError 把状态错误翻译成面向用户的应用错误。
//
// 两种失败要分开报：
//   - 动作名本身不存在（多半是前端传错了参数）；
//   - 动作合法但当前状态不允许（业务上的正常拒绝）。
//
// 后者要告诉用户「现在能从哪些状态做」，否则只会让人反复试。
func (m *Machine) DescribeError(e *StatusError) *apperr.Error {
	if e == nil {
		return apperr.New(apperr.Internal)
	}
	if e.Unknown {
		return apperr.Wrapf(m.ErrorCode, fmt.Errorf("workflow: %s", e.Error()),
			"不支持的操作：%s", e.Action)
	}

	name := e.Label
	if name == "" {
		name = e.Action
	}

	if len(e.AllowedFrom) == 0 {
		// From 为空表示不限来源，能走到这里说明是别的校验抦住了。
		return apperr.Wrapf(m.ErrorCode, fmt.Errorf("workflow: %s", e.Error()),
			"当前状态不允许执行「%s」", name)
	}

	allowed := make([]string, 0, len(e.AllowedFrom))
	for _, s := range e.AllowedFrom {
		allowed = append(allowed, m.StatusLabel(s))
	}
	return apperr.Wrapf(m.ErrorCode, fmt.Errorf("workflow: %s", e.Error()),
		"当前状态不允许「%s」，需先处于：%s",
		name, strings.Join(allowed, " / "))
}

// Actions 描述一个可用动作，供前端渲染操作按钮。
type Actions struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	Perm   string `json:"perm"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Danger bool   `json:"danger"`
}

// Available 返回 from 状态下全部可用动作，按是否改状态、动作名排序，
// 保证同一次请求的输出稳定（前端做 diff 时不会因顺序抖动而重渲染）。
func (m *Machine) Available(from string) []Actions {
	out := make([]Actions, 0, len(m.Transitions))
	for _, t := range m.Transitions {
		if !t.allows(from) {
			continue
		}
		out = append(out, Actions{
			Action: t.Action,
			Label:  t.Label,
			Perm:   t.Perm,
			To:     t.To,
			Kind:   string(t.Kind),
			Danger: t.Danger,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Action < out[j].Action
	})
	return out
}

// StatusLabel 返回状态的中文名，未知状态回退为状态本身。
func (m *Machine) StatusLabel(status string) string {
	if l, ok := m.StatusLabels[status]; ok {
		return l
	}
	return status
}

// IsValidStatus 报告状态是否合法。
func (m *Machine) IsValidStatus(status string) bool {
	for _, s := range m.Statuses {
		if s == status {
			return true
		}
	}
	return false
}

// LogActionOf 返回动作对应的操作历史动词；动作不存在时回退为动作名本身。
func (m *Machine) LogActionOf(action string) string {
	canonical, ok := m.Canonical(action)
	if !ok {
		return action
	}
	if la := m.Transitions[canonical].LogAction; la != "" {
		return la
	}
	return canonical
}

// StatusChangeExtra 生成操作历史 Extra 字段里的状态变更标记。
//
// 格式取 "old|new"（与禅道一致，从那边导数据过来时能直接对上）：
// 需求的「重新激活」需要靠它恢复变更前的状态，所以这个字符串是有语义的，
// 不只是展示用。
func StatusChangeExtra(from, to string) string { return from + "|" + to }

// ParseStatusChangeExtra 解析 "old|new"。
func ParseStatusChangeExtra(extra string) (from, to string, ok bool) {
	i := strings.IndexByte(extra, '|')
	if i <= 0 || i == len(extra)-1 {
		return "", "", false
	}
	return extra[:i], extra[i+1:], true
}
