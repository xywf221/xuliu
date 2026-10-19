package repository_test

// 按 order 列排序的回归。
//
// order 在 SQLite / MySQL / PostgreSQL 里都是保留字，所以 ORDER BY 里出现
// 没加引号的 pm_bug.order 就是语法错误 —— 而 `?order=order_desc` 是白名单里
// 公开对外的排序项，七个列表接口都挂着它。用 clause.Column 让 GORM 按方言
// 自己加引号，才是三库通用的写法。

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

func TestSortByOrderColumn(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(db.Config{
		Driver: db.SQLite, DSN: filepath.Join(t.TempDir(), "sort.db"), LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := gdb.AutoMigrate(&model.Bug{}, &model.TestCase{}, &model.TestCaseStep{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	seedOrderRows(t, gdb)

	bugs := repository.NewBugRepo(gdb)
	result, err := bugs.List(ctx, repository.BugFilter{
		// 缺陷列表带可见性条件：ProductIDs 为空等于「一个都看不到」，
		// 那会让这条测试变成一条永远通过的测试。
		ProductIDs: []uint{1},
		Pagination: pagination.Params{Page: 1, Size: 10, Order: "order_desc"},
	})
	if err != nil {
		t.Fatalf("按 order_desc 查缺陷列表失败: %v", err)
	}
	if len(result.List) != 3 {
		t.Fatalf("应查出 3 条，实际 %d", len(result.List))
	}
	// 种进去的 order 是 10 / 30 / 20，倒序应拿到 30、20、10。
	if got := []int{result.List[0].Order, result.List[1].Order, result.List[2].Order}; got[0] != 30 || got[2] != 10 {
		t.Errorf("order_desc 排序结果不对：%v", got)
	}

	// 同一套写法也要成立在子表的默认排序上（步骤按 order 升序）。
	steps := repository.NewTestCaseStepRepo(gdb)
	list, err := steps.ListByCase(ctx, 1)
	if err != nil {
		t.Fatalf("按 order 查步骤失败: %v", err)
	}
	if len(list) != 2 || list[0].Order != 1 {
		t.Errorf("步骤应按 order 升序返回，实际 %+v", list)
	}

	// 升序同样不能崩。
	if _, err := bugs.List(ctx, repository.BugFilter{
		ProductIDs: []uint{1},
		Pagination: pagination.Params{Page: 1, Size: 10, Order: "order_asc"},
	}); err != nil {
		t.Fatalf("按 order_asc 查缺陷列表失败: %v", err)
	}
}

// seedOrderRows 写入 order 值各不相同的缺陷与步骤，使排序结果可判定。
func seedOrderRows(t *testing.T, gdb *gorm.DB) {
	t.Helper()
	for i, order := range []int{10, 30, 20} {
		bug := model.Bug{
			ProductID: 1, Title: "缺陷" + string(rune('A'+i)),
			Status: model.BugStatusActive, OpenedBy: 1, Order: order,
		}
		if err := gdb.Create(&bug).Error; err != nil {
			t.Fatalf("写入缺陷失败: %v", err)
		}
	}
	for _, order := range []int{2, 1} {
		step := model.TestCaseStep{CaseID: 1, Order: order, Step: "步骤"}
		if err := gdb.Create(&step).Error; err != nil {
			t.Fatalf("写入步骤失败: %v", err)
		}
	}
}
