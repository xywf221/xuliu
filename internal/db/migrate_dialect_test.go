package db_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/db"
)

// TestColumnTypesAcrossDialects 是「换库只改两行配置」这条要求的可验证部分。
//
// 真正会在这里翻车的不是连接串，而是模型：某处手滑用了 PG 专属的 JSONB、
// 或者工时列写成 float，sqlite 上一切正常，换到另外两个库才炸 ——
// 而那已经是用户在生产上换库的时候了。
//
// 本机没有可用的 MySQL / PostgreSQL 实例，跑不了真实建表；但「每个字段在
// 每个方言下映射成什么列类型」这一步不需要服务器，直接问 Dialector 就行。
// 这正是 AutoMigrate 内部走的那条类型映射路径，所以它能覆盖到最容易出事的部分。
//
// 说清楚它验证不到什么：不连真库就无法验证排序规则、标识符大小写、
// 唯一索引在真实数据下的行为，以及查询层的方言差异。
// 那些要靠对着真实实例跑 scripts/e2e.sh（README「换数据库」一节有命令）。
func TestColumnTypesAcrossDialects(t *testing.T) {
	models := db.Models()
	if len(models) == 0 {
		t.Fatal("没有拿到任何模型，这个测试形同虚设")
	}

	dialects := []struct {
		name string
		open func(*testing.T) *gorm.DB
		// floatTypes 是该方言里「浮点列」的写法，工时列命中即为不合格。
		floatTypes []string
	}{
		{
			name: "mysql",
			open: func(t *testing.T) *gorm.DB {
				// SkipInitializeWithVersion：不连服务器就拿不到版本，
				// 跳过它才能在无连接状态下完成类型映射。
				g, err := gorm.Open(mysql.New(mysql.Config{
					DSN:                       "xuliu:xuliu@tcp(127.0.0.1:3306)/xuliu?charset=utf8mb4&parseTime=True&loc=UTC",
					SkipInitializeWithVersion: true,
				}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
				if err != nil {
					t.Fatalf("构造 mysql 句柄失败: %v", err)
				}
				return g
			},
			floatTypes: []string{"float", "double", "real"},
		},
		{
			name: "postgres",
			open: func(t *testing.T) *gorm.DB {
				// 这个 DSN 指向一个不存在的实例：postgres 驱动构造连接池是惰性的，
				// 配上 DryRun + DisableAutomaticPing 就只做类型映射、不碰网络。
				g, err := gorm.Open(postgres.New(postgres.Config{
					DSN: "host=127.0.0.1 user=xuliu password=xuliu dbname=xuliu port=5432 sslmode=disable TimeZone=UTC",
				}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
				if err != nil {
					t.Fatalf("构造 postgres 句柄失败: %v", err)
				}
				return g
			},
			floatTypes: []string{"float", "double precision", "real"},
		},
	}

	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			g := d.open(t)

			total := 0
			for _, m := range models {
				stmt := &gorm.Statement{DB: g}
				if err := stmt.Parse(m); err != nil {
					t.Fatalf("解析模型 %T 失败: %v", m, err)
				}
				table := stmt.Table
				if len(stmt.Schema.Fields) == 0 {
					t.Errorf("表 %s 没解析出任何字段", table)
					continue
				}

				// DBNames 是「这张表真的有这些列」的权威清单
				// （被 gorm:"-" 排除的字段不在里面），顺序即字段声明顺序。
				for _, colName := range stmt.Schema.DBNames {
					f := stmt.Schema.FieldsByDBName[colName]
					if f == nil {
						t.Errorf("表 %s 的列 %s 找不到对应字段", table, colName)
						continue
					}
					colType := strings.ToLower(g.Dialector.DataTypeOf(f))
					if colType == "" {
						t.Errorf("%s.%s 解析出空列类型，模型里可能写了该方言不认识的字段",
							table, f.DBName)
						continue
					}
					total++

					// 工时与金额的列必须是定点数。
					// 判定看列名而不是 Go 类型：float64 配 decimal(10,2) 是刻意的
					// —— 内存里做算术用 float，落库必须定点，否则 0.1+0.2 这类
					// 误差会顺着「已消耗 8 小时」变成看不见的对不上账。
					if isHoursColumn(f.DBName) {
						for _, ft := range d.floatTypes {
							if strings.HasPrefix(colType, ft) {
								t.Errorf("%s.%s 的工时列落成了 %s（应为 decimal）：%g 之类的值在三个库里精度不同，进度会对不上",
									table, f.DBName, colType, 0.1)
							}
						}
						if !strings.Contains(colType, "decimal") && !strings.Contains(colType, "numeric") {
							t.Errorf("%s.%s 的工时列类型是 %s，既非 decimal 也非 numeric",
								table, f.DBName, colType)
						}
					}

					// 三库通用类型是硬约束：JSONB 只有 PG 有，写进去就等于把
					// 「换库只改配置」这句话作废。
					if strings.Contains(colType, "jsonb") {
						t.Errorf("%s.%s 用了 PG 专属的 jsonb，换成 mysql 会建表失败",
							table, f.DBName)
					}
					// 同理，数组列也是 PG 专属。
					if strings.HasSuffix(colType, "[]") {
						t.Errorf("%s.%s 落成了数组列 %s，mysql 没有这种类型",
							table, f.DBName, colType)
					}
				}
			}
			if total == 0 {
				t.Fatalf("一个列都没检查到，测试没有生效")
			}
			t.Logf("%s：检查了 %d 个列", d.name, total)
		})
	}
}

// isHoursColumn 判断列名是否承载工时/金额语义。
//
// 这几张表里的工时口径必须一致（任务、迭代、项目、团队成员、effort 记录），
// 所以按名字认，不按结构体认。
func isHoursColumn(name string) bool {
	switch strings.ToLower(name) {
	case "estimate", "consumed", "left", "hours", "consumed_by":
		return true
	}
	return false
}

// TestAutoMigrateAllModelsOnSQLite 把全部模型在真实 sqlite 上建一遍表。
//
// 与上面的方言测试互补：那边只验证类型映射（不需要服务器），
// 这边验证「模型定义整体自洽」—— 索引重名、唯一键引用了不存在的列、
// 自引用外键之类的结构问题只有真建一次表才会暴露。
func TestAutoMigrateAllModelsOnSQLite(t *testing.T) {
	gdb, err := db.Open(db.Config{
		Driver:   db.SQLite,
		DSN:      filepath.Join(t.TempDir(), "allmodels.db"),
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

	for _, m := range db.Models() {
		if err := gdb.AutoMigrate(m); err != nil {
			t.Errorf("模型 %T 建表失败: %v", m, err)
		}
	}

	// 每个登记在迁移清单里的表都必须真的建出来 ——
	// 「加了模型忘了加进 migrations」是那种要到换库当天才会发现的漏建。
	seen := map[string]bool{}
	for _, tbl := range db.Tables() {
		if seen[tbl] {
			t.Errorf("表 %s 在迁移清单里出现两次", tbl)
			continue
		}
		seen[tbl] = true
		if !gdb.Migrator().HasTable(tbl) {
			t.Errorf("迁移清单里的表 %s 实际没建出来", tbl)
		}
	}
	if len(seen) != len(db.Models()) {
		t.Errorf("迁移清单有 %d 张表，模型却有 %d 个，两边对不上",
			len(seen), len(db.Models()))
	}
}
