package service

import (
	"context"

	"gorm.io/gorm"
)

// 站内通知的触发点与文案。
//
// 全部集中在这一个文件里，是为了让「哪个动作会打扰谁」能被一眼看完并逐条评审。
// 散在三个 service 里各写一遍的结果是：没人说得清一共有几种通知，
// 也没人敢改文案 —— 因为不知道要改一处还是三处。
//
// 触发原则只有一条：**通知下一个要动手的人**。
// 指派给谁、提交评审等谁给结论、解决完由谁验证 —— 只有「下一步卡在谁手上」
// 才发通知。其余动作（编辑、暂停、自己关闭自己提的东西……）只进操作历史：
// 它们不改变「现在该谁动」这个事实，发出去只会把真正的待办淹掉。
//
// 另有一条已经写进 NoticeService 的规则：同一个对象上已有的未读通知会被
// 覆盖而不是追加。两者合起来才是完整的通知语义 ——
// 一条缺陷被来回指派五次，收件人看到的是「现在该我处理它了」这一条。

// noopNotifier 是不发任何通知的实现。
//
// 构造服务时传 nil 就会拿到它。留这个默认值是为了让单测可以只装配
// 关心的那几个依赖；代价是「忘了注入」不会报错，所以通知本身必须有
// 单独的测试覆盖（见 workbench_test.go 里对通知落库的断言）。
type noopNotifier struct{}

func (noopNotifier) Send(
	context.Context, *gorm.DB, uint, uint, string, uint, string, string,
) error {
	return nil
}

// orNoop 把可能为 nil 的通知器换成空实现。
func orNoop(n notifier) notifier {
	if n == nil {
		return noopNotifier{}
	}
	return n
}

// ---------------------------------------------------------------------------
// 任务
// ---------------------------------------------------------------------------

func noticeTaskAssigned(name string) (string, string) {
	return "任务指派给你", "「" + name + "」已指派给你，请及时处理。"
}

func noticeTaskFinished(name string) (string, string) {
	return "任务已完成", "「" + name + "」已完成，请验收或关闭。"
}

func noticeTaskActivated(name string) (string, string) {
	return "任务重新开始", "「" + name + "」被重新激活，需要你继续处理。"
}

// ---------------------------------------------------------------------------
// 缺陷
// ---------------------------------------------------------------------------

// bugCode 是缺陷的对外编号（与缺陷详情页显示的是同一个）。
func bugCode(id uint) string { return "BUG-" + itoa(id) }

func noticeBugAssigned(id uint, title string) (string, string) {
	return "缺陷指派给你", bugCode(id) + "「" + title + "」需要你处理。"
}

func noticeBugResolved(id uint) (string, string) {
	return "缺陷已解决", bugCode(id) + " 已解决，请验证后关闭。"
}

func noticeBugActivated(id uint) (string, string) {
	return "缺陷重新激活", bugCode(id) + " 被重新激活，请再次处理。"
}

// ---------------------------------------------------------------------------
// 需求
// ---------------------------------------------------------------------------

func noticeStoryAssigned(title string) (string, string) {
	return "需求指派给你", "「" + title + "」已指派给你。"
}

func noticeStoryReviewRequested(title string) (string, string) {
	return "需求待你评审", "「" + title + "」已提交评审，请给出结论。"
}

func noticeStoryReviewed(title, result string) (string, string) {
	return "需求评审完成", "「" + title + "」的评审结论：" + reviewResultLabel(result) + "。"
}

// reviewResultLabel 取评审结论的中文名，认不出的回退到原始值。
//
// 复用 labeler.go 里那份映射：通知与操作历史说的是同一个结论，
// 两处各存一份迟早会说成两样。
func reviewResultLabel(result string) string {
	if label, ok := reviewResultLabels[result]; ok {
		return label
	}
	return result
}
