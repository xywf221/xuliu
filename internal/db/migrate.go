package db

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
)

// Migrate 建表并补齐缺失的列/索引。
//
// 用 GORM 的 AutoMigrate 而不是手写 DDL 或迁移框架，理由是它天然跨库：
// 同一份模型定义能在 sqlite / mysql / postgres 上各自建出等价的表，
// 这正是「换库只改配置」这条要求在迁移层面的落点。
//
// 它只做增量（加表、加列、加索引），不会删列 —— 这对生产环境是恰当的
// 保守行为：模型的删字段不该自动变成线上数据的删除。
type migration struct {
	// table 是表名，仅用于日志。
	table string
	// value 是模型指针。
	value any
}

// migrations 是全部业务表。顺序有讲究：被引用的表放在前面，
// 这样外键约束（在支持它的库上）能顺利建立。
var migrations = []migration{
	// ---- 系统 ----
	{model.Company{}.TableName(), &model.Company{}},
	{model.Dept{}.TableName(), &model.Dept{}},
	{model.User{}.TableName(), &model.User{}},
	{model.Group{}.TableName(), &model.Group{}},
	{model.GroupPriv{}.TableName(), &model.GroupPriv{}},
	{model.UserGroup{}.TableName(), &model.UserGroup{}},
	{model.Config{}.TableName(), &model.Config{}},

	// ---- 产品域 ----
	{model.Productline{}.TableName(), &model.Productline{}},
	{model.Product{}.TableName(), &model.Product{}},
	{model.Module{}.TableName(), &model.Module{}},
	{model.ProductPlan{}.TableName(), &model.ProductPlan{}},
	{model.Release{}.TableName(), &model.Release{}},
	{model.Build{}.TableName(), &model.Build{}},

	// ---- 需求 ----
	{model.Story{}.TableName(), &model.Story{}},
	{model.StorySpec{}.TableName(), &model.StorySpec{}},
	{model.StoryReview{}.TableName(), &model.StoryReview{}},

	// ---- 项目域 ----
	{model.Project{}.TableName(), &model.Project{}},
	{model.Execution{}.TableName(), &model.Execution{}},
	{model.Team{}.TableName(), &model.Team{}},
	{model.ProjectProduct{}.TableName(), &model.ProjectProduct{}},
	{model.ProjectStory{}.TableName(), &model.ProjectStory{}},

	// ---- 任务与工时 ----
	{model.Task{}.TableName(), &model.Task{}},
	{model.TaskTeam{}.TableName(), &model.TaskTeam{}},
	{model.TaskRelation{}.TableName(), &model.TaskRelation{}},
	{model.Effort{}.TableName(), &model.Effort{}},

	// ---- 缺陷 ----
	{model.Bug{}.TableName(), &model.Bug{}},

	// ---- 通用（多态） ----
	{model.Action{}.TableName(), &model.Action{}},
	{model.Comment{}.TableName(), &model.Comment{}},
	{model.Notice{}.TableName(), &model.Notice{}},
	{model.File{}.TableName(), &model.File{}},
}

// AutoMigrate 执行全部建表迁移。
func AutoMigrate(ctx context.Context, gdb *gorm.DB, logger *slog.Logger) error {
	logger.Info("开始数据库迁移", slog.Int("表数量", len(migrations)))

	for _, m := range migrations {
		// AutoMigrate 不接收 context，用 Session 设置上下文以便日志串联。
		if err := gdb.WithContext(ctx).AutoMigrate(m.value); err != nil {
			return fmt.Errorf("db: 迁移表 %s 失败: %w", m.table, err)
		}
	}

	logger.Info("数据库迁移完成")
	return nil
}

// Tables 返回全部业务表名，供诊断与测试使用。
func Tables() []string {
	out := make([]string, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, m.table)
	}
	return out
}

// Models 返回全部待迁移的模型指针，顺序与 AutoMigrate 一致。
//
// 导出它是为了让「同一套模型能否在三种方言下各自生成合法 DDL」这件事
// 可以被测试覆盖 —— 没有真实 MySQL/PG 实例时，这是能验证到的最接近
// 换库的一环（见 migrate_dialect_test.go）。
func Models() []any {
	out := make([]any, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, m.value)
	}
	return out
}
