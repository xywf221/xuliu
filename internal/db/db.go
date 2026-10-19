// Package db 负责数据库连接的建立与驱动切换。
//
// sqlite / mysql / postgres 三种驱动共用同一套模型与迁移逻辑，
// 切换数据库只需要改配置里的 driver 与 dsn，业务代码零改动。
package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3/gormlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Driver 是受支持的数据库驱动名。
type Driver string

const (
	SQLite   Driver = "sqlite"
	MySQL    Driver = "mysql"
	Postgres Driver = "postgres"
)

// Config 描述一次数据库连接。
type Config struct {
	Driver          Driver
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	// LogLevel 取 silent / error / warn / info，空值按 warn 处理。
	LogLevel string
}

// dialector 按驱动名返回对应的 GORM dialector。
func (c Config) dialector() (gorm.Dialector, error) {
	switch c.Driver {
	case SQLite, "":
		return gormlite.Open(sqliteDSN(c.DSN)), nil
	case MySQL:
		return mysql.Open(c.DSN), nil
	case Postgres:
		return postgres.Open(c.DSN), nil
	default:
		return nil, fmt.Errorf("db: 不支持的驱动 %q（可选 %s / %s / %s）",
			c.Driver, SQLite, MySQL, Postgres)
	}
}

// sqlitePragmas 是每条新连接都要执行的 PRAGMA。
//
// 为什么非设不可 —— 实测数字：建这 35 张表，默认设置要 1.58 秒，
// 加上下面三条只要 63 毫秒（差 25 倍）。默认 journal_mode 是 rollback，
// 每条 DDL 都单独提交并 fsync，而 Windows 上 fsync 特别慢。
// 「首次启动要等半分钟」和「go test ./internal/service 跑两分钟」
// 都是这一件事：服务层 73 个测试各自建一遍库，光建表就吃掉约 110 秒。
//
//   - journal_mode=WAL：读不再被写挡住；这个设置会写进库文件，持久生效。
//   - synchronous=NORMAL：进程崩溃仍然安全，只在断电时可能丢掉最后一个事务。
//     这是**取舍**不是免费午餐 —— 需要断电零丢失的环境应改回 FULL
//     （代价就是回到上面那个 1.58 秒）。
//
// 没有设 busy_timeout：实测这个驱动的默认值是 60000 毫秒（不是 sqlite 本身的 0），
// 再加一条只会把它调小 —— 曾写过「默认 0、并发立刻抛 SQLITE_BUSY」，
// 那是对驱动的猜测而非实测，被 TestSQLitePragmasApply 打翻了。
//
// 没有加 foreign_keys(1)：模型里全是普通 uint 列而不是 GORM 关联字段，
// 生成的表根本没有外键约束（可对照建表语句看 pm_task），开了也无从生效。
var sqlitePragmas = []string{
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
}

// sqliteDSN 把 DSN 整理成能带上 PRAGMA 的 URI 形式。
//
// _pragma 只能走 URI 查询串（驱动在打开连接时解析它），而 URI 形式
// 必须带 file: 前缀；Windows 路径里的反斜杠还要换成正斜杠，
// 否则 "C:\Users\..." 会被当成普通字符而找不到文件。
func sqliteDSN(dsn string) string {
	trimmed := strings.TrimSpace(dsn)
	switch {
	case trimmed == "" || trimmed == ":memory:":
		// 内存库既没有 fsync 问题，也没有可以改写的路径形式。
		return trimmed
	case strings.HasPrefix(trimmed, "file:"):
		// 调用方自己带了 _pragma 就完全尊重他的设置，不叠加。
		if strings.Contains(trimmed, "_pragma=") {
			return trimmed
		}
		return appendSQLitePragmas(trimmed)
	default:
		return appendSQLitePragmas("file:" + filepath.ToSlash(trimmed))
	}
}

func appendSQLitePragmas(uri string) string {
	separator := "?"
	if strings.Contains(uri, "?") {
		separator = "&"
	}
	clauses := make([]string, 0, len(sqlitePragmas))
	for _, p := range sqlitePragmas {
		clauses = append(clauses, "_pragma="+p)
	}
	return uri + separator + strings.Join(clauses, "&")
}

// logLevel 把配置里的字符串映射为 gorm 的日志级别。
func (c Config) logLevel() logger.LogLevel {
	switch c.LogLevel {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}

// Open 建立数据库连接并配置连接池。
//
// 注意：这里不做 AutoMigrate，迁移由调用方在 bootstrp 阶段显式执行，
// 以免测试代码意外改动真实库结构。
func Open(cfg Config) (*gorm.DB, error) {
	// SQLite 的库文件不会自动建目录。默认 DSN 是 "data/xuliu.db"，
	// 而 clone 下来的仓库里并没有 data/ 目录 —— 不先补上就会在
	// 「首次启动」这个最该顺畅的路径上直接失败。
	if cfg.Driver == SQLite || cfg.Driver == "" {
		if err := ensureSQLiteDir(cfg.DSN); err != nil {
			return nil, err
		}
	}

	dialector, err := cfg.dialector()
	if err != nil {
		return nil, err
	}

	gdb, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(cfg.logLevel()),
		// 我们用软删除表达「已删除」，不做级联删除，避免误删关联数据。
		DisableForeignKeyConstraintWhenMigrating: false,
		// 把驱动层的唯一冲突归一为 gorm.ErrDuplicatedKey。
		//
		// 开关有意义：不开时 GORM 压根不会调用驱动的 Translate()，
		// 而 repository.IsDuplicated 在此之前只能靠错误文本猜。
		// 开了之后三库行为一致：都会得到同一个哨兵错误。
		// 不过 sqlite 驱动（ncruces/gormlite）是否实现了该接口
		// 因版本而异，所以 IsDuplicated 保留了文本兜底。
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("db: 打开 %s 连接失败: %w", cfg.Driver, err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: 获取底层连接池失败: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("db: %s 连接不可用: %w", cfg.Driver, err)
	}

	return gdb, nil
}

// ensureSQLiteDir 确保 SQLite 库文件所在的目录存在。
//
// 只处理「普通文件路径」这一种情况：
//   - :memory: 与 file:...?mode=memory 是内存库，无目录可建；
//   - 带 file: 前缀的 URI 形式留给高级用法，不提供目录操作。
func ensureSQLiteDir(dsn string) error {
	path := strings.TrimSpace(dsn)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	// 去掉 URI 参数（如 "data/xuliu.db?_pragma=..."）后取目录。
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("db: 创建 SQLite 库目录 %s 失败: %w", dir, err)
	}
	return nil
}

// WithTx 在一个事务里执行 fn；fn 返回错误则回滚，返回 nil 则提交。
//
// 所有会写多张表的业务操作（状态流转、拆任务、记工时）都应经由此函数，
// 保证「落库 + 写操作历史」要么全成要么全不成。
func WithTx(ctx context.Context, gdb *gorm.DB, fn func(tx *gorm.DB) error) error {
	return gdb.WithContext(ctx).Transaction(fn)
}
