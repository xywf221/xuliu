package workflow_test

// 状态机守卫条件是整个系统里最容易出错、影响面最大的一块逻辑：
// 一旦某个流转被错误放行，脏状态就会进库，之后所有依赖状态的判断都跟着错。
// 所以这里的用例是照着禅道 model.php 的守卫条件逐条写的，
// 每条「允许」和「拒绝」都显式断言。

import (
	"strings"
	"testing"

	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/workflow"
)

// TestBugLifecycle 覆盖缺陷的完整闭环，含「重新激活」。
func TestBugLifecycle(t *testing.T) {
	m := workflow.BugMachine

	// 正常闭环：active → resolved → closed
	mustAllow(t, m, "resolve", model.BugStatusActive)
	if got := m.Transitions["resolve"].To; got != model.BugStatusResolved {
		t.Errorf("resolve 目标状态应为 resolved，实际 %q", got)
	}
	mustAllow(t, m, "close", model.BugStatusResolved)
	// 重新激活是缺陷能被反复处理的关键路径。
	mustAllow(t, m, "activate", model.BugStatusClosed)
	mustAllow(t, m, "activate", model.BugStatusResolved)

	// 守卫边界：这些组合必须被拒。
	mustDeny(t, m, "resolve", model.BugStatusResolved, "已解决的缺陷不能再次解决")
	mustDeny(t, m, "resolve", model.BugStatusClosed, "已关闭的缺陷不能解决")
	mustDeny(t, m, "close", model.BugStatusActive, "未解决的缺陷不能直接关闭")
	mustDeny(t, m, "close", model.BugStatusClosed, "不能重复关闭")
	mustDeny(t, m, "activate", model.BugStatusActive, "已激活的缺陷无需再激活")
	mustDeny(t, m, "confirm", model.BugStatusResolved, "只能确认激活状态的缺陷")

	// 指派：已关闭不可指派，其余可以。
	mustAllow(t, m, "assign", model.BugStatusActive)
	mustAllow(t, m, "assign", model.BugStatusResolved)
	mustDeny(t, m, "assign", model.BugStatusClosed, "已关闭的缺陷不能指派")
}

// TestTaskLifecycle 覆盖任务的暂停/继续往返与完成/取消的边界。
func TestTaskLifecycle(t *testing.T) {
	m := workflow.TaskMachine

	mustAllow(t, m, "start", model.TaskStatusWait)
	mustAllow(t, m, "pause", model.TaskStatusDoing)
	mustAllow(t, m, "restart", model.TaskStatusPause)
	mustAllow(t, m, "finish", model.TaskStatusDoing)
	mustAllow(t, m, "finish", model.TaskStatusPause)

	// 完成与取消共享同一组允许来源：未开始/进行中/已暂停。
	for _, from := range []string{model.TaskStatusWait, model.TaskStatusDoing, model.TaskStatusPause} {
		mustAllow(t, m, "finish", from)
		mustAllow(t, m, "cancel", from)
	}

	// 已终结的任务不能再完成或取消。
	for _, from := range []string{model.TaskStatusDone, model.TaskStatusClosed, model.TaskStatusCancel} {
		mustDeny(t, m, "finish", from, "已终结的任务不能重复完成")
		mustDeny(t, m, "cancel", from, "已终结的任务不能取消")
	}

	// 只有 done/cancel 能关闭。
	mustAllow(t, m, "close", model.TaskStatusDone)
	mustAllow(t, m, "close", model.TaskStatusCancel)
	mustDeny(t, m, "close", model.TaskStatusDoing, "进行中的任务不能直接关闭")

	// 工时只能记在进行中的任务上。
	mustAllow(t, m, "recordeffort", model.TaskStatusDoing)
	mustDeny(t, m, "recordeffort", model.TaskStatusWait, "未开始的任务不能记工时")
	mustDeny(t, m, "recordeffort", model.TaskStatusPause, "已暂停的任务不能记工时")
}

// TestStoryLifecycle 覆盖需求评审的四种结论与变更版本化。
func TestStoryLifecycle(t *testing.T) {
	m := workflow.StoryMachine

	// 提交评审：草稿与变更中都可提交。
	mustAllow(t, m, "submitreview", model.StoryStatusDraft)
	mustAllow(t, m, "submitreview", model.StoryStatusChanging)
	mustDeny(t, m, "submitreview", model.StoryStatusActive, "已激活的需求无需评审")

	// 四种评审结论各自的目标状态。
	for action, want := range map[string]string{
		"pass":    model.StoryStatusActive,
		"revert":  model.StoryStatusActive,
		"reject":  model.StoryStatusClosed,
		"clarify": "", // clarify 的目标由 service 按是否已变更决定
	} {
		mustAllow(t, m, action, model.StoryStatusReviewing)
		if got := m.Transitions[action].To; got != want {
			t.Errorf("%s 的目标状态应为 %q，实际 %q", action, want, got)
		}
	}

	// 评审动作只能作用于评审中状态。
	mustDeny(t, m, "pass", model.StoryStatusDraft, "草稿状态没有评审可做")
	mustDeny(t, m, "reject", model.StoryStatusActive, "激活状态没有评审可做")

	// 变更：只有已激活的可变更；它是需求版本 +1 的入口。
	mustAllow(t, m, "change", model.StoryStatusActive)
	mustDeny(t, m, "change", model.StoryStatusDraft, "草稿直接编辑即可，无需变更")
	mustDeny(t, m, "change", model.StoryStatusReviewing, "评审中不能变更")
	if got := m.Transitions["change"].To; got != model.StoryStatusChanging {
		t.Errorf("change 目标状态应为 changing，实际 %q", got)
	}

	// 关闭：除已关闭外都可关闭。
	mustDeny(t, m, "close", model.StoryStatusClosed, "不能重复关闭")
	for _, from := range []string{
		model.StoryStatusDraft, model.StoryStatusActive,
		model.StoryStatusChanging, model.StoryStatusReviewing,
	} {
		mustAllow(t, m, "close", from)
	}

	// 激活：只能激活已关闭的。
	mustAllow(t, m, "activate", model.StoryStatusClosed)
	mustDeny(t, m, "activate", model.StoryStatusDraft, "未关闭的需求无需激活")

	// 编辑：已关闭不可编辑，评审中也不可（要先撤回）。
	mustDeny(t, m, "edit", model.StoryStatusClosed, "已关闭的需求不能编辑")
	mustDeny(t, m, "edit", model.StoryStatusReviewing, "评审中不能编辑")
}

// TestProjectAndExecutionLifecycle 覆盖项目与迭代的状态流转。
func TestProjectAndExecutionLifecycle(t *testing.T) {
	p := workflow.ProjectMachine
	mustAllow(t, p, "start", model.ProjectStatusWait)
	mustAllow(t, p, "start", model.ProjectStatusSuspended)
	mustDeny(t, p, "start", model.ProjectStatusClosed, "已关闭的项目要先激活")
	mustAllow(t, p, "suspend", model.ProjectStatusDoing)
	mustDeny(t, p, "suspend", model.ProjectStatusSuspended, "已挂起无需再挂起")
	mustAllow(t, p, "close", model.ProjectStatusDoing)
	mustDeny(t, p, "close", model.ProjectStatusClosed, "不能重复关闭")
	mustAllow(t, p, "activate", model.ProjectStatusClosed)

	e := workflow.ExecutionMachine
	mustAllow(t, e, "start", model.ExecutionStatusWait)
	mustAllow(t, e, "close", model.ExecutionStatusDoing)
	mustDeny(t, e, "activate", model.ExecutionStatusDoing, "进行中的迭代无需激活")
}

// TestUnknownActionIsDistinguished 验证「动作不存在」与「状态不允许」
// 能被区分开 —— 两者应给出不同的错误码，前者说明前端传错了参数，
// 后者说明用户点错了按钮。
func TestUnknownActionIsDistinguished(t *testing.T) {
	m := workflow.TaskMachine

	_, serr := m.Resolve("teleport", model.TaskStatusWait)
	if serr == nil {
		t.Fatal("不存在的动作应被拒绝")
	}
	if !serr.Unknown {
		t.Error("应标记为「动作不存在」而非「状态不允许」")
	}

	_, serr = m.Resolve("pause", model.TaskStatusWait)
	if serr == nil {
		t.Fatal("未开始的任务不能暂停")
	}
	if serr.Unknown {
		t.Error("pause 是存在的动作，应标记为状态不允许")
	}
	// 错误信息要能告诉调用方当前允许哪些来源状态。
	if len(serr.AllowedFrom) == 0 {
		t.Error("状态不允许时应给出允许的来源状态，供前端提示")
	}
}

// TestActionNameNormalization 验证动作名的宽松匹配。
// 前端不同地方的写法容易不一致，不能因为大小写或连字符就拒绝请求。
func TestActionNameNormalization(t *testing.T) {
	m := workflow.TaskMachine

	for _, variant := range []string{"recordeffort", "recordEffort", "RecordEffort", "record-effort", "record_effort"} {
		if _, err := m.Resolve(variant, model.TaskStatusDoing); err != nil {
			t.Errorf("动作名变体 %q 应被接受，实际: %v", variant, err)
		}
	}
}

// TestAvailableActionsConsistency 验证 Available 的结论与 Resolve 一致。
//
// 这两者一旦不一致，前端就会显示一个点不动的按钮（或反过来隐藏可用操作），
// 是最容易被忽略又最影响体验的一类 bug。
func TestAvailableActionsConsistency(t *testing.T) {
	for name, m := range workflow.Machines {
		for _, status := range m.Statuses {
			available := m.Available(status)
			seen := make(map[string]bool, len(available))
			for _, a := range available {
				seen[a.Action] = true
				if _, err := m.Resolve(a.Action, status); err != nil {
					t.Errorf("%s: Available 给出了 %q@%s，但 Resolve 拒绝：%v",
						name, a.Action, status, err)
				}
				// 每个可用动作都必须带权限点与中文名，否则前端没法渲染。
				if a.Perm == "" {
					t.Errorf("%s: 动作 %q 缺少权限点", name, a.Action)
				}
				if a.Label == "" {
					t.Errorf("%s: 动作 %q 缺少中文名", name, a.Action)
				}
			}

			// 反向：Resolve 通过的必须出现在 Available 里。
			for action := range m.Transitions {
				if _, err := m.Resolve(action, status); err == nil && !seen[action] {
					t.Errorf("%s: Resolve 允许 %q@%s，但 Available 未列出",
						name, action, status)
				}
			}
		}
	}
}

// TestMachinesAreWellFormed 是状态机的结构自检：
// 流转里的来源与目标状态必须是已声明的合法状态，否则会出现
// 「流转到一个不存在的状态」这种难以追查的问题。
func TestMachinesAreWellFormed(t *testing.T) {
	for name, m := range workflow.Machines {
		if m.ObjectType == "" {
			t.Errorf("%s: 缺少 ObjectType", name)
		}
		// 没配 ErrorCode 的话，状态不允许时会报成通用 500，
		// 前端无法区分「状态不对」与「服务器坏了」。
		if m.ErrorCode == "" {
			t.Errorf("%s: 缺少 ErrorCode", name)
		}

		valid := make(map[string]bool, len(m.Statuses))
		for _, s := range m.Statuses {
			valid[s] = true
			if m.StatusLabel(s) == s {
				t.Errorf("%s: 状态 %q 缺少中文名", name, s)
			}
		}

		for action, tr := range m.Transitions {
			if tr.Action != action {
				t.Errorf("%s: 动作 %q 的 Action 字段为 %q，两者应一致",
					name, action, tr.Action)
			}
			if tr.Label == "" {
				t.Errorf("%s: 动作 %q 缺少中文名", name, tr.Action)
			}
			if tr.Perm == "" {
				t.Errorf("%s: 动作 %q 缺少权限点", name, tr.Action)
			}
			for _, from := range tr.From {
				if !valid[from] {
					t.Errorf("%s: 动作 %q 的来源状态 %q 未在 Statuses 中声明",
						name, tr.Action, from)
				}
			}
			if tr.To != "" && !valid[tr.To] {
				t.Errorf("%s: 动作 %q 的目标状态 %q 未在 Statuses 中声明",
					name, tr.Action, tr.To)
			}
		}
	}
}

// TestStatusChangeExtraRoundTrip 验证 "old|new" 的编解码。
//
// 需求的「重新激活」依赖它恢复关闭前的状态，格式错了会导致
// 激活后状态丢失，所以这个往返必须有测试兜底。
func TestStatusChangeExtraRoundTrip(t *testing.T) {
	extra := workflow.StatusChangeExtra(model.StoryStatusActive, model.StoryStatusClosed)
	if extra != "active|closed" {
		t.Fatalf("Extra 格式应为 old|new，实际 %q", extra)
	}

	from, to, ok := workflow.ParseStatusChangeExtra(extra)
	if !ok {
		t.Fatal("应能解析出状态变更")
	}
	if from != model.StoryStatusActive || to != model.StoryStatusClosed {
		t.Errorf("解析结果不符：from=%q to=%q", from, to)
	}

	// 非法输入不应 panic，也不应返回看似合法的结果。
	for _, bad := range []string{"", "|", "active|", "|closed", "active"} {
		if _, _, ok := workflow.ParseStatusChangeExtra(bad); ok {
			t.Errorf("非法输入 %q 不应被解析成功", bad)
		}
	}
}

// ---- 断言辅助 ----

func mustAllow(t *testing.T, m *workflow.Machine, action, from string) {
	t.Helper()
	if _, err := m.Resolve(action, from); err != nil {
		t.Errorf("%s: %q 应当允许从 %q 出发，实际被拒：%v", m.ObjectType, action, from, err)
	}
}

func mustDeny(t *testing.T, m *workflow.Machine, action, from, why string) {
	t.Helper()
	if _, err := m.Resolve(action, from); err == nil {
		t.Errorf("%s: %q 不应允许从 %q 出发（%s）", m.ObjectType, action, from, why)
	}
}

// TestDescribeError 验证状态错误能被翻译成用户看得懂的应用错误，
// 且「动作不存在」与「状态不允许」给出不同的文案。
func TestDescribeError(t *testing.T) {
	// 状态不允许：文案里要带上「现在能处于什么状态」，否则用户只能瞎试。
	_, statusErr := workflow.StoryMachine.Resolve("pass", model.StoryStatusDraft)
	if statusErr == nil {
		t.Fatal("draft 状态不应允许 pass")
	}
	appErr := workflow.StoryMachine.DescribeError(statusErr)
	if appErr.Code() != apperr.StoryStatusInvalid {
		t.Errorf("错误码应为 story-status-invalid，实际 %q", appErr.Code())
	}
	if !strings.Contains(appErr.Message(), "评审中") {
		t.Errorf("文案应说明需处于「评审中」，实际 %q", appErr.Message())
	}
	if !strings.Contains(appErr.Message(), "评审通过") {
		t.Errorf("文案应说明被拒的是哪个动作，实际 %q", appErr.Message())
	}
	// 内部原因要保留，便于日志里追查，但不能泄漏给用户。
	if appErr.Error() == appErr.Message() {
		t.Error("应把内部细节放在 cause 里，而不是拼进用户文案")
	}

	// 动作本身不存在：文案应指出是动作名的问题。
	_, unknownErr := workflow.StoryMachine.Resolve("no-such-action", model.StoryStatusDraft)
	if unknownErr == nil || !unknownErr.Unknown {
		t.Fatal("不存在的动作应被识别为 Unknown")
	}
	unknownAppErr := workflow.StoryMachine.DescribeError(unknownErr)
	if unknownAppErr.Code() != apperr.StoryStatusInvalid {
		t.Errorf("错误码应为 story-status-invalid，实际 %q", unknownAppErr.Code())
	}
	if !strings.Contains(unknownAppErr.Message(), "不支持") {
		t.Errorf("文案应说明动作不存在，实际 %q", unknownAppErr.Message())
	}

	// 每个状态机都要有此能力，且各用各的错误码。
	for name, m := range workflow.Machines {
		_, e := m.Resolve("definitely-not-an-action", "")
		if e == nil {
			t.Fatalf("%s: 应当无法解析该动作", name)
		}
		if got := m.DescribeError(e).Code(); got == "" {
			t.Errorf("%s: DescribeError 应给出错误码", name)
		}
	}
}
