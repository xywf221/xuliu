package pagination

// Sorter.orderBy 的回归。
//
// 这里盯的是一件「不出错就没人会发现」的事：ORDER BY 的列名要不要加引号。
// order 是 SQLite / MySQL / PostgreSQL 三家的保留字，而白名单里就有
// pm_bug.order、pm_product.order、sys_dept.order 这些列；
// GORM 的 Order(字符串) 会把整段标成 Raw 原样输出，于是 ?order=order_desc
// 会直接语法错误。默认序 defaultOrder 也可能是多列表达式
// （"pm_effort.date DESC, pm_effort.id DESC"），同样要逐段拆。

import (
	"testing"

	"gorm.io/gorm/clause"
)

func TestSorterOrderByQuotesColumns(t *testing.T) {
	sorter := Sorter{
		"id":    "pm_bug.id",
		"order": "pm_bug.order",
		"pri":   "pm_bug.pri",
	}

	got := sorter.orderBy("order_desc,id_asc", "pm_bug.id DESC")
	want := []clause.OrderByColumn{
		{Column: clause.Column{Table: "pm_bug", Name: "order"}, Desc: true},
		{Column: clause.Column{Table: "pm_bug", Name: "id"}, Desc: false},
	}
	assertColumns(t, got.Columns, want)

	// 白名单外的键丢弃，整体退回默认序 —— 默认序也要拆成表名/列名。
	got = sorter.orderBy("password_asc", "pm_bug.id DESC")
	assertColumns(t, got.Columns, []clause.OrderByColumn{
		{Column: clause.Column{Table: "pm_bug", Name: "id"}, Desc: true},
	})

	// 多列默认序：每一段都要分开，不能把整串当一个列名。
	// 这正是曾经踩过的坑：错误信息是 no such column: pm_effort.date DESC, ...
	got = sorter.orderBy("", "pm_effort.date DESC, pm_effort.id DESC")
	assertColumns(t, got.Columns, []clause.OrderByColumn{
		{Column: clause.Column{Table: "pm_effort", Name: "date"}, Desc: true},
		{Column: clause.Column{Table: "pm_effort", Name: "id"}, Desc: true},
	})

	// 不带方向的默认序。
	got = sorter.orderBy("", "pm_task.id ASC")
	assertColumns(t, got.Columns, []clause.OrderByColumn{
		{Column: clause.Column{Table: "pm_task", Name: "id"}, Desc: false},
	})

	// 没有表名前缀的表达式也要能用（列名单独出现时不填 Table）。
	got = sorter.orderBy("", "id DESC")
	assertColumns(t, got.Columns, []clause.OrderByColumn{
		{Column: clause.Column{Name: "id"}, Desc: true},
	})
}

func assertColumns(t *testing.T, got, want []clause.OrderByColumn) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("排序项数量应为 %d，实际 %d：%+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Column.Table != want[i].Column.Table ||
			got[i].Column.Name != want[i].Column.Name ||
			got[i].Desc != want[i].Desc {
			t.Errorf("第 %d 个排序项应为 %+v，实际 %+v", i, want[i], got[i])
		}
		if got[i].Column.Raw {
			t.Errorf("第 %d 个排序项被标成 Raw（原样输出不加引号），保留字列名会崩", i)
		}
	}
}
