package history_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/workflow"
)

// openTestDB 在临时目录里开一个真实文件库。
//
// 不用 :memory:：连接池会开多条连接，而 sqlite 的 :memory: 是
// 「每条连接一个独立库」，池化下会得到彼此看不见的多个库。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Open(db.Config{
		Driver:   db.SQLite,
		DSN:      filepath.Join(t.TempDir(), "history.db"),
		LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Action{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	// Windows 下文件被占用时 t.TempDir 的清理会失败，
	// 所以必须在测试结束前显式关掉连接池。
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

// testLabeler 模拟 service 层注入的翻译器。
type testLabeler struct{}

func (testLabeler) FieldLabel(field string) string {
	if field == "Status" {
		return "状态"
	}
	return ""
}

func (testLabeler) Value(field, raw string) string {
	if field != "Status" {
		return raw
	}
	switch raw {
	case "draft":
		return "草稿"
	case "active":
		return "激活"
	}
	return raw
}

// TestRecordRoundTrip 验证「写入 → 读回」的完整往返，以及可读化文本
// 是在写入时完成的（读取方不需要再查任何表）。
func TestRecordRoundTrip(t *testing.T) {
	ctx := context.Background()
	gdb := openTestDB(t)

	rec := history.New(testLabeler{})
	fixed := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	rec.Clock = func() time.Time { return fixed }

	action, err := rec.Record(ctx, gdb, history.Entry{
		ObjectType: model.ObjectTypeStory,
		ObjectID:   7,
		ProductID:  3,
		ActorID:    9,
		Action:     model.ActionEdited,
		Comment:    "补充验收标准",
		Extra:      "draft|active",
		Changes: []history.Change{
			{Field: "Status", Old: "draft", New: "active"},
		},
		Files: []uint{11, 12},
	})
	if err != nil {
		t.Fatalf("写入操作历史失败: %v", err)
	}

	if action.ID == 0 {
		t.Fatal("应回填自增 id")
	}
	if !action.Date.Equal(fixed) {
		t.Errorf("应使用注入的时钟，实际 %v", action.Date)
	}

	changes, err := history.DecodeChanges(action.Changes)
	if err != nil {
		t.Fatalf("解析变更失败: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("期望 1 条变更，实际 %d", len(changes))
	}
	// 可读化是关键：存进去的就是中文，历史页不必反查用户表。
	if changes[0].OldText != "草稿" || changes[0].NewText != "激活" {
		t.Errorf("应写入可读文本，实际 old=%q new=%q", changes[0].OldText, changes[0].NewText)
	}
	if changes[0].Label != "状态" {
		t.Errorf("应补全字段中文名，实际 %q", changes[0].Label)
	}

	files, err := history.DecodeIDs(action.Files)
	if err != nil {
		t.Fatalf("解析附件 id 失败: %v", err)
	}
	if len(files) != 2 || files[0] != 11 || files[1] != 12 {
		t.Errorf("附件 id 往返不一致: %v", files)
	}

	// Extra 的 "old|new" 语义要能还原 —— 需求的「激活」靠它恢复前状态。
	from, to, ok := workflow.ParseStatusChangeExtra(action.Extra)
	if !ok || from != "draft" || to != "active" {
		t.Errorf("状态变更 extra 解析失败: from=%q to=%q ok=%v", from, to, ok)
	}
}

// TestRecordInsideTransactionRollsBack 验证历史与业务数据同生共死：
// 事务回滚后不应留下孤儿历史记录。
func TestRecordInsideTransactionRollsBack(t *testing.T) {
	ctx := context.Background()
	gdb := openTestDB(t)
	rec := history.New(nil)

	sentinel := errSentinel{}
	err := gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := rec.Record(ctx, tx, history.Entry{
			ObjectType: model.ObjectTypeBug,
			ObjectID:   1,
			Action:     model.ActionOpened,
		}); err != nil {
			return err
		}
		// 模拟后续业务步骤失败。
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("期望事务返回哨兵错误，实际 %v", err)
	}

	var count int64
	if err := gdb.Model(&model.Action{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("事务回滚后不应留下历史记录，实际 %d 条", count)
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "业务步骤失败" }

// TestRecordRejectsIncompleteEntry 验证缺字段时尽早报错，
// 而不是写出一条无法归属的「幽灵动态」。
func TestRecordRejectsIncompleteEntry(t *testing.T) {
	ctx := context.Background()
	gdb := openTestDB(t)
	rec := history.New(nil)

	cases := map[string]history.Entry{
		"缺对象类型":  {ObjectID: 1, Action: model.ActionOpened},
		"缺对象 ID": {ObjectType: model.ObjectTypeStory, Action: model.ActionOpened},
		"缺动作名":   {ObjectType: model.ObjectTypeStory, ObjectID: 1},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := rec.Record(ctx, gdb, e); err == nil {
				t.Fatal("应拒绝不完整的操作记录")
			}
		})
	}
}

// TestEncodeChangesDropsNoopAndRedundantText 验证两个降噪行为：
// 无变化的字段不落库、可读文本与原始值相同时不重复存。
func TestEncodeChangesDropsNoopAndRedundantText(t *testing.T) {
	ctx := context.Background()
	gdb := openTestDB(t)
	rec := history.New(testLabeler{})

	action, err := rec.Record(ctx, gdb, history.Entry{
		ObjectType: model.ObjectTypeTask,
		ObjectID:   5,
		Action:     model.ActionEdited,
		Changes: []history.Change{
			// 没有变化 —— 应被丢掉。
			{Field: "Status", Old: "draft", New: "draft"},
			// 有变化但「标题」没有可读化文本，OldText == Old 时应留空。
			{Field: "Title", Old: "旧标题", New: "新标题"},
		},
	})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	changes, err := history.DecodeChanges(action.Changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("无变化的字段应被丢弃，实际留下 %d 条", len(changes))
	}
	if changes[0].Field != "Title" {
		t.Errorf("应只保留 Title，实际 %q", changes[0].Field)
	}
	// labeler 对未知字段返回空串，且 OldText 与 Old 相同 → 留空。
	if changes[0].OldText != "" {
		t.Errorf("可读文本与原始值相同时应留空，实际 %q", changes[0].OldText)
	}
}

// fakeStory 用于验证 Diff 的字段比对。
type fakeStory struct {
	Title      string
	Pri        int
	Estimate   *float64
	ClosedDate *time.Time
	Status     string
	Ignored    string
}

func TestDiff(t *testing.T) {
	estimate := 8.0
	closed := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	oldObj := fakeStory{Title: "登录", Pri: 3, Estimate: &estimate, Status: "draft", Ignored: "a"}
	newObj := fakeStory{Title: "登录优化", Pri: 3, Estimate: &estimate, ClosedDate: &closed, Status: "active", Ignored: "b"}

	changes := history.Diff(oldObj, newObj, map[string]string{
		"Title":      "标题",
		"Pri":        "优先级",
		"Estimate":   "预计工时",
		"ClosedDate": "关闭时间",
		// 注意：故意不给 Ignored，验证「映射里没有的字段会被跳过」。
	})

	got := map[string]history.Change{}
	for _, c := range changes {
		got[c.Field] = c
	}

	if _, ok := got["Pri"]; ok {
		t.Error("值未变化的字段不应产出变更")
	}
	if _, ok := got["Ignored"]; ok {
		t.Error("映射之外的字段不应产出变更（调用方精确控制追溯范围）")
	}
	if _, ok := got["Estimate"]; ok {
		t.Error("指针指向的值未变化，不应产出变更")
	}
	if c, ok := got["Title"]; !ok || c.Old != "登录" || c.New != "登录优化" {
		t.Errorf("标题变更不正确: %+v", got["Title"])
	}
	if c, ok := got["ClosedDate"]; !ok {
		t.Errorf("从无值变为有值应算作变化: %+v", changes)
	} else {
		if c.Old != "" {
			t.Errorf("nil 指针应转为空串，实际 %q", c.Old)
		}
		if c.New == "" {
			t.Errorf("新值不应为空，实际 %+v", c)
		}
	}

	// 输出顺序必须稳定，否则前端渲染与测试断言都会飘。
	for i := 1; i < len(changes); i++ {
		if changes[i-1].Field > changes[i].Field {
			t.Fatalf("变更列表应按字段名排序: %v", changes)
		}
	}
}

// TestDiffSkipsMissingFields 验证映射里写了不存在的字段时不 panic。
func TestDiffSkipsMissingFields(t *testing.T) {
	changes := history.Diff(
		fakeStory{Title: "a"},
		fakeStory{Title: "b"},
		map[string]string{"NoSuchField": "不存在"},
	)
	if len(changes) != 0 {
		t.Errorf("不存在的字段应被跳过，实际 %v", changes)
	}
}

func TestDescribeChange(t *testing.T) {
	cases := []struct {
		name string
		in   history.Change
		want string
	}{
		{
			name: "可读文本优先",
			in:   history.Change{Field: "Status", Label: "状态", Old: "draft", New: "active", OldText: "草稿", NewText: "激活"},
			want: "状态：草稿 → 激活",
		},
		{
			name: "无旧值视为新设为",
			in:   history.Change{Field: "AssignedTo", Label: "指派给", New: "2", NewText: "张三"},
			want: "指派给：设为 张三",
		},
		{
			name: "无新值视为清空",
			in:   history.Change{Field: "Deadline", Label: "截止日期", Old: "2026-01-01"},
			want: "截止日期：清空（原为 2026-01-01）",
		},
		{
			name: "缺中文名时回退字段名",
			in:   history.Change{Field: "Title", Old: "a", New: "b"},
			want: "Title：a → b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := history.DescribeChange(c.in); got != c.want {
				t.Errorf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

func TestDecodeEmpty(t *testing.T) {
	if changes, err := history.DecodeChanges(""); err != nil || changes != nil {
		t.Errorf("空串应解析为 nil 且不报错，实际 %v %v", changes, err)
	}
	if ids, err := history.DecodeIDs(""); err != nil || ids != nil {
		t.Errorf("空串应解析为 nil 且不报错，实际 %v %v", ids, err)
	}
}
