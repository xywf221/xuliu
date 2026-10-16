package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
)

// TestOpenCreatesMissingSQLiteDir 锁死一个会让「首次启动」直接失败的问题：
//
// 默认 DSN 是 "data/xuliu.db"，而刚 clone 下来的仓库里没有 data/ 目录，
// SQLite 不会替我们创建它。Open 必须先补上目录，否则用户拿到的第一条
// 报错就是「unable to open database file」—— 在README 承诺「一条命令起服务」
// 的前提下，这是最不该失败的路径。
func TestOpenCreatesMissingSQLiteDir(t *testing.T) {
	// 刻意指向两层都不存在的目录。
	dsn := filepath.Join(t.TempDir(), "data", "nested", "xuliu.db")

	gdb, err := db.Open(db.Config{Driver: db.SQLite, DSN: dsn, LogLevel: "silent"})
	if err != nil {
		t.Fatalf("目录不存在时应自动创建并可连接，实际报错: %v", err)
	}
	t.Cleanup(func() { closeDB(t, gdb) })

	if _, err := os.Stat(dsn); err != nil {
		t.Fatalf("库文件应已创建: %v", err)
	}
}

// TestOpenMemoryDSNUntouched 验证内存库不受目录创建逻辑影响。
func TestOpenMemoryDSNUntouched(t *testing.T) {
	gdb, err := db.Open(db.Config{Driver: db.SQLite, DSN: ":memory:", LogLevel: "silent"})
	if err != nil {
		t.Fatalf("内存库应可打开: %v", err)
	}
	t.Cleanup(func() { closeDB(t, gdb) })

	var one int
	if err := gdb.Raw("SELECT 1").Scan(&one).Error; err != nil || one != 1 {
		t.Fatalf("内存库应可用: %v %d", err, one)
	}
}

// TestOpenRejectsUnknownDriver 验证驱动名写错时给出可操作的报错。
func TestOpenRejectsUnknownDriver(t *testing.T) {
	if _, err := db.Open(db.Config{Driver: db.Driver("oracle"), DSN: "x"}); err == nil {
		t.Fatal("未知驱动应被拒绝")
	}
}

func closeDB(t *testing.T, gdb *gorm.DB) {
	t.Helper()
	if sqlDB, err := gdb.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
