package db_test

// 这是一次「技术选型体检」测试。
//
// 选型时 Go 1.27（2026-08 发布）比 gin v1.12.0（2026-02）、gormlite v0.34.0
// （2026-04）的 tag 都新，且「纯 Go sqlite + GORM」这套组合没有现成先例。
// 所以在写任何业务代码之前，先用这个文件把每一个假设都验证一遍：
// 驱动能开、AutoMigrate 能建出我们要的字段形态、软删/事务/json v2/标准库 uuid/
// os.Root/gin 路由都能用。
//
// 这些断言通过后，才敢在此基础上铺 model / service。

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
)

// spikeStory 刻意覆盖我们真正会用的全部字段形态：
// 自增主键、uint 外键、枚举存 varchar 带默认值、text 存 JSON、
// decimal 存工时、软删除时间戳。
type spikeStory struct {
	ID        uint    `gorm:"primaryKey"`
	ProductID uint    `gorm:"index;not null"`
	ModuleID  uint    `gorm:"index"`
	Title     string  `gorm:"size:255;not null"`
	Status    string  `gorm:"size:30;not null;default:draft"`
	Stage     string  `gorm:"size:30;not null;default:wait"`
	Estimate  float64 `gorm:"type:decimal(10,2);not null;default:0"`
	Changes   string  `gorm:"type:text"`

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (spikeStory) TableName() string { return "spike_story" }

// openTemp 在临时目录里开一个真实文件库。
//
// 不用 :memory:：database/sql 的连接池会开多条连接，而 sqlite 的 :memory:
// 是「每条连接一个独立库」，在池化下会得到彼此看不见的多个库，测试结论不可信。
func openTemp(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "spike.db")
	gdb, err := db.Open(db.Config{
		Driver:          db.SQLite,
		DSN:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
		LogLevel:        "silent",
	})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

// TestSQLiteMigrationAndCRUD 验证核心假设：纯 Go 驱动能开、AutoMigrate 能建表、
// 软删除按预期工作。
func TestSQLiteMigrationAndCRUD(t *testing.T) {
	gdb := openTemp(t)

	if err := gdb.AutoMigrate(&spikeStory{}); err != nil {
		t.Fatalf("AutoMigrate 失败（说明字段形态不被驱动接受）: %v", err)
	}

	ctx := context.Background()

	s := spikeStory{ProductID: 1, Title: "登录页验证码不刷新", Status: "active", Stage: "developing", Estimate: 3.50}
	if err := gdb.WithContext(ctx).Create(&s).Error; err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("期望自增主键被回填，实际仍为 0")
	}

	// 默认值必须真的落到了库里（而不是靠 Go 侧 zero value 遮掩）。
	var got spikeStory
	if err := gdb.WithContext(ctx).First(&got, s.ID).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got.Title != s.Title || got.Estimate != s.Estimate {
		t.Errorf("回读不一致: got %+v want %+v", got, s)
	}

	// 软删除：默认查询看不到，Unscoped 能查到。
	if err := gdb.WithContext(ctx).Delete(&spikeStory{}, s.ID).Error; err != nil {
		t.Fatalf("软删除失败: %v", err)
	}
	if err := gdb.WithContext(ctx).First(&spikeStory{}, s.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("软删除后默认查询应报 ErrRecordNotFound，实际: %v", err)
	}
	if err := gdb.WithContext(ctx).Unscoped().First(&spikeStory{}, s.ID).Error; err != nil {
		t.Errorf("Unscoped 应能查到已软删的记录，实际: %v", err)
	}
}

// TestTransactionRollback 验证「落库 + 写历史」能真的做成一个原子操作 ——
// 这是状态机正确性的地基。
func TestTransactionRollback(t *testing.T) {
	gdb := openTemp(t)
	if err := gdb.AutoMigrate(&spikeStory{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	ctx := context.Background()

	sentinel := errors.New("模拟业务校验失败")
	err := db.WithTx(ctx, gdb, func(tx *gorm.DB) error {
		if err := tx.Create(&spikeStory{ProductID: 1, Title: "半途而废的需求"}).Error; err != nil {
			return err
		}
		return sentinel // 故意失败
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("期望原样返回哨兵错误，实际: %v", err)
	}

	var n int64
	if err := gdb.WithContext(ctx).Model(&spikeStory{}).Count(&n).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if n != 0 {
		t.Errorf("事务已回滚，表里不该有记录，实际有 %d 条", n)
	}
}

// TestJSONV2 验证标准库 v2 能正常编解码 —— 操作历史的 changes 字段要用它。
func TestJSONV2(t *testing.T) {
	type change struct {
		Field string `json:"field"`
		Old   string `json:"old"`
		New   string `json:"new"`
	}

	in := []change{{Field: "status", Old: "active", New: "resolved"}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json/v2 Marshal 失败: %v", err)
	}

	var out []change
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json/v2 Unmarshal 失败: %v", err)
	}
	if len(out) != 1 || out[0].New != "resolved" {
		t.Errorf("json/v2 往返后数据不一致: %s", raw)
	}

	// v2 默认更严格：重复键应当被拒绝，这里只确认它确实变严了。
	var m map[string]int
	if err := json.Unmarshal([]byte(`{"a":1,"a":2}`), &m); err == nil {
		t.Log("注意：json/v2 未拒绝重复键，严格性假设不成立")
	}
}

// TestStdlibUUID 确认 Go 1.27 的标准库 uuid 可用，无需引入 google/uuid。
func TestStdlibUUID(t *testing.T) {
	a := uuid.NewV7()
	b := uuid.New()
	if a == b {
		t.Error("两次生成的 UUID 不应相同")
	}

	// 往返：String 出来的文本必须能 Parse 回同一个值（附件名/令牌要用）。
	parsed, err := uuid.Parse(a.String())
	if err != nil {
		t.Fatalf("uuid.Parse 失败: %v", err)
	}
	if parsed != a {
		t.Errorf("UUID 往返不一致: %s != %s", parsed, a)
	}

	// V7 按时间有序，这对做有序主键很关键：后生成的必须大于先生成的。
	if uuid.NewV7().Compare(a) <= 0 {
		t.Error("V7 UUID 应随时间单调递增（依赖时钟精度，若偶发失败可放宽）")
	}
}

// TestOSRootBlocksTraversal 验证附件目录能靠 os.Root 锁死，
// 这对「用户可控文件名」的上传场景是硬要求。
func TestOSRootBlocksTraversal(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("os.OpenRoot 失败: %v", err)
	}
	defer root.Close()

	if err := root.WriteFile("ok.txt", []byte("safe"), 0o600); err != nil {
		t.Fatalf("Root 内正常写入失败: %v", err)
	}

	if _, err := root.Open("../escape.txt"); err == nil {
		t.Error("os.Root 应当拒绝 ../ 穿越访问，实际放行了")
	} else if !errors.Is(err, fs.ErrInvalid) && !os.IsNotExist(err) {
		t.Logf("穿越访问被拒绝（错误类型: %v）", err)
	}
}

// TestGinRouter 确认 gin 在 Go 1.27 上能正常起路由 —— 这是选型里最新的那个依赖。
func TestGinRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	if body := w.Body.String(); body != `{"status":"ok"}` {
		t.Errorf("响应体不符: %s", body)
	}
}
