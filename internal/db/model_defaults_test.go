package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xywf221/xuliu/internal/db"
	"github.com/xywf221/xuliu/internal/model"
)

// TestBoolFieldsWithoutDefaultTagRoundTrip 锁死一个曾让数据静默出错的
// GORM 行为。
//
// GORM 对带 `default` 标签的字段会**跳过零值**，让数据库默认值生效。
// 这对「让 DB 兜底」是好事，但在「用户显式关掉某个开关」时就是数据错误：
//
//	Product{ReviewStory: false}  →  库里存成 true
//
// 用户看到开关是关的，实际行为却是开着。修法是把这类 bool 的默认值
// 收回 Go 层（模型上不加 default 标签，由 service 显式赋值），
// 于是「忘了赋值」会直接报 not null，而不是静默变成默认值。
//
// 这个测试把结论固定在数据库层：模型一旦被加回 default 标签，它会失败。
func TestBoolFieldsWithoutDefaultTagRoundTrip(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(db.Config{
		Driver:   db.SQLite,
		DSN:      filepath.Join(t.TempDir(), "defaults.db"),
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
	if err := gdb.AutoMigrate(&model.Product{}, &model.Project{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// false 必须原样存进去。
	off := model.Product{Name: "关掉评审的产品", Code: "OFF", ReviewStory: false}
	if err := gdb.WithContext(ctx).Create(&off).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var gotOff model.Product
	if err := gdb.WithContext(ctx).First(&gotOff, off.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOff.ReviewStory {
		t.Error("显式写入 review_story=false 后读回为 true，" +
			"说明模型又被加回了 default 标签，GORM 跳过了零值")
	}

	// true 同样要原样存进去。
	on := model.Product{Name: "开启评审的产品", Code: "ON", ReviewStory: true}
	if err := gdb.WithContext(ctx).Create(&on).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	var gotOn model.Product
	if err := gdb.WithContext(ctx).First(&gotOn, on.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !gotOn.ReviewStory {
		t.Error("显式写入 review_story=true 后读回为 false")
	}

	// 项目上的 Multiple 是同一类字段，一并钉住。
	noMulti := model.Project{Name: "单迭代项目", Multiple: false}
	if err := gdb.WithContext(ctx).Create(&noMulti).Error; err != nil {
		t.Fatalf("写入项目失败: %v", err)
	}
	var gotProject model.Project
	if err := gdb.WithContext(ctx).First(&gotProject, noMulti.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotProject.Multiple {
		t.Error("显式写入 multiple=false 后读回为 true")
	}
}
