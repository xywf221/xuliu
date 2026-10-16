package repository_test

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/pagination"
	"github.com/xywf221/xuliu/internal/repository"
)

func openRepo(t *testing.T) (*gorm.DB, *repository.UserRepo) {
	t.Helper()
	gdb, err := db.Open(db.Config{
		Driver:   db.SQLite,
		DSN:      filepath.Join(t.TempDir(), "repo.db"),
		LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := gdb.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return gdb, repository.NewUserRepo(gdb)
}

// TestUpdateFieldsAcceptsUnchangedValue 锁死一个跨库行为差异：
//
// MySQL 默认只统计「真正发生变化」的行数（clientFoundRows=false），
// 而 sqlite / postgres 统计的是「匹配到的行数」。若把 RowsAffected==0
// 一律当作「记录不存在」，那么在 MySQL 上「提交了但某个字段没改动」
// 就会莫名其妙报 404 —— 而前端整表单提交时，总有不改动的字段。
func TestUpdateFieldsAcceptsUnchangedValue(t *testing.T) {
	ctx := context.Background()
	_, repo := openRepo(t)

	u := &model.User{Account: "zhangsan", Realname: "张三", Password: "x"}
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	// 把 realname 更新成与当前相同的值：应当成功，而不是 404。
	if err := repo.UpdateFields(ctx, nil, u.ID, map[string]any{"realname": "张三"}); err != nil {
		t.Fatalf("更新为相同值不应失败，实际: %v", err)
	}
}

// TestUpdateFieldsNotFoundStillReported 验证修复没有把「真的不存在」也放过去。
func TestUpdateFieldsNotFoundStillReported(t *testing.T) {
	ctx := context.Background()
	_, repo := openRepo(t)

	err := repo.UpdateFields(ctx, nil, 99999, map[string]any{"realname": "李四"})
	if err == nil {
		t.Fatal("更新不存在的记录应报错")
	}
	if got := apperr.As(err).Code(); got != apperr.NotFound {
		t.Errorf("应为 not_found，实际 %q", got)
	}
}

// TestUpdateFieldsCanClearToZero 验证用 map 而非结构体更新的理由：
// 结构体更新会跳过零值，导致「把 assignedTo 清成 0」静默失效。
func TestUpdateFieldsCanClearToZero(t *testing.T) {
	ctx := context.Background()
	_, repo := openRepo(t)

	u := &model.User{Account: "lisi", Realname: "李四", Password: "x", DeptID: 7, Disabled: true}
	if err := repo.Create(ctx, nil, u); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	if err := repo.UpdateFields(ctx, nil, u.ID, map[string]any{"dept_id": 0, "disabled": false}); err != nil {
		t.Fatalf("清空字段失败: %v", err)
	}

	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeptID != 0 {
		t.Errorf("dept_id 应被清成 0，实际 %d", got.DeptID)
	}
	if got.Disabled {
		t.Error("disabled 应被清成 false")
	}
}

// TestDeleteMissingReportsNotFound 验证删除不存在的记录会报错而不是静默成功。
func TestDeleteMissingReportsNotFound(t *testing.T) {
	ctx := context.Background()
	_, repo := openRepo(t)

	if err := repo.Delete(ctx, nil, 4242); err == nil {
		t.Fatal("删除不存在的记录应报错")
	}
}

// TestDuplicateDetectionIsUniform 验证唯一冲突被翻译成统一错误，
// 三库行为一致（调用方据此给出「账号已被占用」这类具体提示）。
func TestDuplicateDetectionIsUniform(t *testing.T) {
	ctx := context.Background()
	_, repo := openRepo(t)

	if err := repo.Create(ctx, nil, &model.User{Account: "dup", Realname: "甲", Password: "x"}); err != nil {
		t.Fatal(err)
	}
	err := repo.Create(ctx, nil, &model.User{Account: "dup", Realname: "乙", Password: "x"})
	if err == nil {
		t.Fatal("重复账号应被拒绝")
	}
	if !repository.IsDuplicated(err) {
		t.Errorf("IsDuplicated 应识别唯一冲突，实际 err=%v", err)
	}
}

// TestPaginateReturnsEmptyListNotNull 验证空结果的 list 是 [] 而不是 null，
// 免得前端每处都要判空。
func TestPaginateReturnsEmptyListNotNull(t *testing.T) {
	ctx := context.Background()
	gdb, _ := openRepo(t)
	repo := repository.NewUserRepo(gdb)

	res, err := repo.List(ctx, repository.UserFilter{Keywords: "绝对不存在的关键字"})
	if err != nil {
		t.Fatal(err)
	}
	if res.List == nil {
		t.Fatal("list 不应为 nil")
	}
	if len(res.List) != 0 {
		t.Errorf("应为空列表，实际 %d 条", len(res.List))
	}
}

// TestPaginateNormalizesParams 验证越界的 page/size 被收敛到合法范围。
func TestPaginateNormalizesParams(t *testing.T) {
	ctx := context.Background()
	gdb, _ := openRepo(t)
	repo := repository.NewUserRepo(gdb)

	res, err := repo.List(ctx, repository.UserFilter{
		Pagination: pagination.Params{Page: -5, Size: 100000, Order: "not-a-column_desc"},
	})
	if err != nil {
		t.Fatalf("非法分页参数不应导致失败: %v", err)
	}
	if res.Page != 1 {
		t.Errorf("page 应被收敛为 1，实际 %d", res.Page)
	}
	if res.Size > 200 {
		t.Errorf("size 应被限制在 200 以内，实际 %d", res.Size)
	}
}
