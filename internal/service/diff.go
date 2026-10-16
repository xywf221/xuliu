package service

import (
	"fmt"
	"strconv"
	"time"

	"github.com/xywf221/xuliu/internal/history"
	"github.com/xywf221/xuliu/internal/model"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// 本文件是项目域与任务域用到的「字段变更比对」与若干纯函数辅助。
//
// 与需求那套（story.go 里的 diffFields）思路一致：把「准备写入的字段」
// 与模型当前值逐一比对，只把登记过的字段写进操作历史 ——
// 未登记的字段（如 updated_at）不该在时间线里变成一条用户看不懂的动态。

// projectDiff 比对项目字段。
func projectDiff(p *model.Project, fields map[string]any) []history.Change {
	out := make([]history.Change, 0, len(fields))
	for field, value := range fields {
		if _, ok := projectFieldLabels[field]; !ok {
			continue
		}
		old, new := valueString(oldProjectValue(p, field)), valueString(value)
		if old == new {
			continue
		}
		out = append(out, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(out)
	return out
}

// executionDiff 比对迭代字段。
func executionDiff(e *model.Execution, fields map[string]any) []history.Change {
	out := make([]history.Change, 0, len(fields))
	for field, value := range fields {
		if _, ok := executionFieldLabels[field]; !ok {
			continue
		}
		old, new := valueString(oldExecutionValue(e, field)), valueString(value)
		if old == new {
			continue
		}
		out = append(out, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(out)
	return out
}

// taskDiff 比对任务字段。
func taskDiff(t *model.Task, fields map[string]any) []history.Change {
	out := make([]history.Change, 0, len(fields))
	for field, value := range fields {
		if _, ok := taskFieldLabels[field]; !ok {
			continue
		}
		old, new := valueString(oldTaskValue(t, field)), valueString(value)
		if old == new {
			continue
		}
		out = append(out, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(out)
	return out
}

// bugDiff 比对缺陷字段。
func bugDiff(b *model.Bug, fields map[string]any) []history.Change {
	out := make([]history.Change, 0, len(fields))
	for field, value := range fields {
		if _, ok := bugFieldLabels[field]; !ok {
			continue
		}
		old, new := valueString(oldBugValue(b, field)), valueString(value)
		if old == new {
			continue
		}
		out = append(out, history.Change{Field: field, Old: old, New: new})
	}
	sortChanges(out)
	return out
}

// oldProjectValue 取项目某字段的当前值。
func oldProjectValue(p *model.Project, field string) any {
	switch field {
	case "name":
		return p.Name
	case "code":
		return p.Code
	case "model":
		return p.Model
	case "status":
		return p.Status
	case "begin":
		return p.Begin
	case "end":
		return p.End
	case "days":
		return p.Days
	case "desc":
		return p.Desc
	case "po":
		return p.PO
	case "pm":
		return p.PM
	case "qd":
		return p.QD
	case "rd":
		return p.RD
	case "acl":
		return p.ACL
	case "whitelist":
		// 白名单是 JSON 文本，比对时按「已解码的 id 串」比较，
		// 否则 '[1,2]' 与 '[1, 2]' 会被当成两次不同的修改。
		return idListString(mustDecodeIDs(p.Whitelist))
	case "multiple":
		return p.Multiple
	case "progress":
		return p.Progress
	case "estimate":
		return p.Estimate
	case "consumed":
		return p.Consumed
	case "left":
		return p.Left
	case "order":
		return p.Order
	}
	return nil
}

// oldExecutionValue 取迭代某字段的当前值。
func oldExecutionValue(e *model.Execution, field string) any {
	switch field {
	case "name":
		return e.Name
	case "code":
		return e.Code
	case "type":
		return e.Type
	case "status":
		return e.Status
	case "begin":
		return e.Begin
	case "end":
		return e.End
	case "days":
		return e.Days
	case "desc":
		return e.Desc
	case "po":
		return e.PO
	case "pm":
		return e.PM
	case "qd":
		return e.QD
	case "rd":
		return e.RD
	case "acl":
		return e.ACL
	case "whitelist":
		return idListString(mustDecodeIDs(e.Whitelist))
	case "progress":
		return e.Progress
	case "estimate":
		return e.Estimate
	case "consumed":
		return e.Consumed
	case "left":
		return e.Left
	case "order":
		return e.Order
	}
	return nil
}

// oldTaskValue 取任务某字段的当前值。
func oldTaskValue(t *model.Task, field string) any {
	switch field {
	case "name":
		return t.Name
	case "keywords":
		return t.Keywords
	case "desc":
		return t.Desc
	case "type":
		return t.Type
	case "mode":
		return t.Mode
	case "pri":
		return t.Pri
	case "estimate":
		return t.Estimate
	case "consumed":
		return t.Consumed
	case "left":
		return t.Left
	case "deadline":
		return t.Deadline
	case "est_started":
		return t.EstStarted
	case "status":
		return t.Status
	case "version":
		return t.Version
	case "assigned_to":
		return t.AssignedTo
	case "module_id":
		return t.ModuleID
	case "story_id":
		return t.StoryID
	case "execution_id":
		return t.ExecutionID
	case "product_id":
		return t.ProductID
	case "parent_id":
		return t.ParentID
	case "closed_reason":
		return t.ClosedReason
	case "mailto":
		return t.Mailto
	case "order":
		return t.Order
	}
	return nil
}

// oldBugValue 取缺陷某字段的当前值。
func oldBugValue(b *model.Bug, field string) any {
	switch field {
	case "title":
		return b.Title
	case "keywords":
		return b.Keywords
	case "steps":
		return b.Steps
	case "type":
		return b.Type
	case "severity":
		return b.Severity
	case "pri":
		return b.Pri
	case "os":
		return b.OS
	case "browser":
		return b.Browser
	case "hardware":
		return b.Hardware
	case "opened_build":
		return b.OpenedBuild
	case "resolved_build":
		return b.ResolvedBuild
	case "module_id":
		return b.ModuleID
	case "plan_id":
		return b.PlanID
	case "story_id":
		return b.StoryID
	case "story_version":
		return b.StoryVersion
	case "task_id":
		return b.TaskID
	case "project_id":
		return b.ProjectID
	case "execution_id":
		return b.ExecutionID
	case "deadline":
		return b.Deadline
	case "assigned_to":
		return b.AssignedTo
	case "status":
		return b.Status
	case "resolution":
		return b.Resolution
	case "confirmed":
		return b.Confirmed
	case "to_story_id":
		return b.ToStoryID
	case "to_task_id":
		return b.ToTaskID
	case "duplicate_bug_id":
		return b.DuplicateBugID
	case "related_bug_ids":
		// 与项目白名单同理：JSON 文本按「已解码的 id 串」比较，
		// 否则 '[1,2]' 与 '[1, 2]' 会被当成两次不同的修改。
		return idListString(mustDecodeIDs(b.RelatedBugIDs))
	case "closed_reason":
		return b.ClosedReason
	case "order":
		return b.Order
	}
	return nil
}

// valueString 把字段值转成可比对、可展示的字符串。
//
// 空值统一成空串：nil 指针、零值时间、空字符串在「时间线」这个语境下
// 都该显示为「（空）」，而不是各自的字面量。
func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "1"
		}
		return "0"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case float64:
		// -1 精度去掉浮点尾随零：3.50 与 3.5 是同一次修改。
		return strconv.FormatFloat(t, 'f', -1, 64)
	case *time.Time:
		if t == nil || t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02")
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02")
	default:
		return fmt.Sprint(v)
	}
}

// idListString 把 id 列表拼成 "1,2,3"，用于比对与展示。
func idListString(ids []uint) string {
	if len(ids) == 0 {
		return ""
	}
	out := make([]byte, 0, len(ids)*3)
	for i, id := range ids {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, strconv.FormatUint(uint64(id), 10)...)
	}
	return string(out)
}

// hoursProgress 按工时算进度百分比。
//
// 公式：
//
//	剩余 > 0：progress = round(已消耗 / (已消耗 + 剩余) * 100)
//	剩余 == 0：已消耗 == 0 → 0；否则 100（活干完了但没记工时，
//	          按「完成」处理而不是「0%」—— 否则已完成的任务会显示成没动）
//
// 迭代与项目的进度也用它，只不过分子分母换成聚合后的工时。
func hoursProgress(consumed, left float64) int {
	if left > 0 {
		total := consumed + left
		if total <= 0 {
			return 0
		}
		return int(roundTo(consumed/total*100, 0))
	}
	if consumed == 0 {
		return 0
	}
	return 100
}

// roundTo 四舍五入到指定小数位。
func roundTo(v float64, decimals int) float64 {
	scale := 1.0
	for i := 0; i < decimals; i++ {
		scale *= 10
	}
	if v >= 0 {
		return float64(int64(v*scale+0.5)) / scale
	}
	return float64(int64(v*scale-0.5)) / scale
}

// itoa 把 id 转成字符串（拼业务编号用）。
func itoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// mustDecodeIDs 解码 id 列表，失败时返回空列表。
//
// 只用于读出参：库里存的是我们自己写进去的 JSON，解析失败说明数据被
// 人为改过，此时给空列表比让整个查询失败更合适（前端少显示一项，
// 而不是整页打不开）。
func mustDecodeIDs(raw string) []uint {
	ids, err := decodeIDs(raw)
	if err != nil {
		return nil
	}
	return ids
}

// translateDateErr 把 parseDateRange 的错误换成指定错误码。
//
// parseDateRange 是产品计划的实现，报的是 PlanDateInvalid；
// 项目与迭代各有自己的错误码，所以在这里换一下，同时保留原文案
// （「开始日期格式应为 YYYY-MM-DD」比「日期不合法」有用得多）。
func translateDateErr(err error, code apperr.Code) error {
	if appErr := apperr.As(err); appErr != nil {
		return apperr.Newf(code, "%s", appErr.Message())
	}
	return apperr.Wrap(code, err)
}
