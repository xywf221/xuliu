package db

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// TestSQLiteDSNKeepsUserSettings 固定住 DSN 改写的边界。
//
// 这里最怕的不是漏加 PRAGMA（性能问题，量一下就发现），
// 而是把调用方自己写的参数弄坏或重复叠加 —— 那会静默改变连接行为。
func TestSQLiteDSNKeepsUserSettings(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "普通相对路径转成 URI 并带上 PRAGMA",
			in:   "data/xuliu.db",
			want: "file:data/xuliu.db?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "Windows 反斜杠换成正斜杠",
			in:   `C:\tmp\xuliu.db`,
			want: "file:C:/tmp/xuliu.db?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		},
		{
			name: "内存库原样保留（没有可改写的路径，也没有 fsync 问题）",
			in:   ":memory:",
			want: ":memory:",
		},
		{
			name: "调用方自带 _pragma 时完全不插手",
			in:   "file:data/x.db?_pragma=synchronous(FULL)",
			want: "file:data/x.db?_pragma=synchronous(FULL)",
		},
		{
			name: "已有查询串但没有 _pragma 时用 & 续上",
			in:   "file:data/x.db?cache=shared",
			want: "file:data/x.db?cache=shared&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		},
	}

	for _, tc := range tests {
		if got := sqliteDSN(tc.in); got != tc.want {
			t.Errorf("%s：\n  期望 %s\n  实际 %s", tc.name, tc.want, got)
		}
	}
}

// TestSQLitePragmasApply 断言 PRAGMA 真的在连接上生效了。
//
// 为什么值得钉一条测试：这些参数带来的不是「跑不通」而是「跑得慢」
// —— 一次误改（比如把 DSN 改写绕过去）会让首次启动从几百毫秒回到半分钟、
// 让服务层测试从十秒回到两分钟，而且所有功能依旧全绿，没人会怀疑到它身上。
func TestSQLitePragmasApply(t *testing.T) {
	gdb, err := Open(Config{
		Driver: SQLite, DSN: filepath.Join(t.TempDir(), "pragma.db"), LogLevel: "silent",
	})
	if err != nil {
		t.Fatalf("开库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := AutoMigrate(context.Background(), gdb, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	// journal_mode 返回值是小写文本；WAL 会持久写进库文件。
	if got := pragmaText(t, gdb, "PRAGMA journal_mode"); got != "wal" {
		t.Errorf("journal_mode 应为 wal，实际 %q", got)
	}
	// synchronous 是数字：0=OFF 1=NORMAL 2=FULL 3=EXTRA。
	if got := pragmaNumber(t, gdb, "PRAGMA synchronous"); got != 1 {
		t.Errorf("synchronous 应为 1(NORMAL)，实际 %d", got)
	}
	// 不断言 busy_timeout：驱动自带的默认值（实测 60000）比我们想设的更大方，
	// 所以我们没有覆盖它。留一条断言反而会把「别画蛇添足」这个结论锁成
	// 「必须等于某个数」，下次改动时会被误当成目标。
}

func pragmaText(t *testing.T, gdb *gorm.DB, pragma string) string {
	t.Helper()
	var out string
	if err := gdb.Raw(pragma).Scan(&out).Error; err != nil {
		t.Fatalf("%s 查询失败: %v", pragma, err)
	}
	return strings.ToLower(out)
}

func pragmaNumber(t *testing.T, gdb *gorm.DB, pragma string) int {
	t.Helper()
	var out int
	if err := gdb.Raw(pragma).Scan(&out).Error; err != nil {
		t.Fatalf("%s 查询失败: %v", pragma, err)
	}
	return out
}
