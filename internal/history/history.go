// Package history 负责记录操作历史与字段级变更。
//
// 设计要点：
//
//   - **写入必须在业务事务内**。历史与数据必须同生共死：落了库却没有历史，
//     等于这段变更不可追溯；有历史却没有落库，则是凭空捏造的记录。
//     所以本包的接口都接收 *gorm.DB（可能是事务句柄），而不自己开事务。
//   - **字段级 diff 存 JSON**。照禅道是拆成独立的 zt_history 表，
//     但 diff 只用于渲染时间线、从不作为查询条件，拆表只会带来无谓的 join。
//   - **变更内容的可读化在写入时完成**。存进去的就是中文，
//     避免读取时还要反查用户表把 id 翻成人名（那会导致历史页 N+1 查询）。
package history

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/xywf221/xuliu/internal/model"
)

// Change 是一个字段的变更。
type Change struct {
	// Field 是字段名（数据库列名或 Go 字段名，由调用方保持一致）。
	Field string `json:"field"`
	// Label 是字段的中文名，供前端直接显示。
	Label string `json:"label"`
	// Old/New 是原始值，用于程序化处理。
	Old string `json:"old"`
	New string `json:"new"`
	// OldText/NewText 是已可读化的值（如把用户 id 换成姓名，
	// 把状态值换成「进行中」）。前端优先显示这两个。
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText,omitempty"`
}

// Labeler 由各 service 提供，把「字段 + 原始值」翻译成可读文本。
//
// 之所以要外部注入：只有业务层知道 user id 该查哪张表、
// 状态值该用哪个状态机的字典翻译。放在这里会让本包依赖所有领域。
type Labeler interface {
	// Label 返回字段的中文名；未知字段返回空串。
	FieldLabel(field string) string
	// Value 把原始值翻译为可读文本；未知字段原样返回。
	Value(field, raw string) string
}

// Entry 描述一次要记录的操作。
type Entry struct {
	ObjectType string
	ObjectID   uint

	// ProductID/ProjectID/ExecutionID 是冗余的归属信息，
	// 让「某项目下的全部动态」这类查询免去 join。
	ProductID   uint
	ProjectID   uint
	ExecutionID uint

	// ActorID 是操作者。系统自动触发的变更传 0。
	ActorID uint
	// Action 是动词，取值见 model.ActionXxx。
	Action string
	// Comment 是用户填写的备注。
	Comment string
	// Extra 是附加信息，按动作类型各有约定：
	// 指派存目标用户 id；解决/关闭存原因；状态变更存 "old|new"。
	Extra string
	// Changes 是字段级变更列表。
	Changes []Change
	// Files 是本次操作附带的附件 id。
	Files []uint
}

// Recorder 把操作写入 sys_action。
type Recorder struct {
	labeler Labeler
	// Clock 可注入，便于测试。
	Clock func() time.Time
}

// New 构造 Recorder。labeler 可为 nil（此时不做可读化，只存原始值）。
func New(labeler Labeler) *Recorder {
	return &Recorder{labeler: labeler, Clock: time.Now}
}

// Record 在给定的事务句柄上写入一条操作历史。
//
// db 必须是当前业务事务的句柄，不能是根 *gorm.DB —— 否则历史会在
// 业务回滚后仍然留在库里。
func (r *Recorder) Record(ctx context.Context, db *gorm.DB, e Entry) (*model.Action, error) {
	if e.ObjectType == "" || e.ObjectID == 0 {
		return nil, fmt.Errorf("history: 缺少对象类型或对象 ID")
	}
	if e.Action == "" {
		return nil, fmt.Errorf("history: 缺少动作名")
	}

	changes, err := r.encodeChanges(e.Changes)
	if err != nil {
		return nil, err
	}
	files, err := encodeIDs(e.Files)
	if err != nil {
		return nil, err
	}

	action := &model.Action{
		ObjectType:  e.ObjectType,
		ObjectID:    e.ObjectID,
		ProductID:   e.ProductID,
		ProjectID:   e.ProjectID,
		ExecutionID: e.ExecutionID,
		ActorID:     e.ActorID,
		Action:      e.Action,
		Date:        r.now(),
		Comment:     e.Comment,
		Extra:       e.Extra,
		Changes:     changes,
		Files:       files,
	}

	if err := db.WithContext(ctx).Create(action).Error; err != nil {
		return nil, fmt.Errorf("history: 写入操作历史失败: %w", err)
	}
	return action, nil
}

// now 返回当前时间（可注入）。
func (r *Recorder) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

// encodeChanges 把变更列表序列化为 JSON，并补全可读文本。
func (r *Recorder) encodeChanges(changes []Change) (string, error) {
	if len(changes) == 0 {
		return "", nil
	}

	// 过滤掉没有实际变化的项：调用方常直接对比两个结构体，
	// 其中大部分字段并未改动，存进去只会让时间线变成噪音。
	filtered := make([]Change, 0, len(changes))
	for _, c := range changes {
		if c.Old == c.New {
			continue
		}
		if r.labeler != nil {
			if c.Label == "" {
				c.Label = r.labeler.FieldLabel(c.Field)
			}
			if c.OldText == "" {
				c.OldText = r.labeler.Value(c.Field, c.Old)
			}
			if c.NewText == "" {
				c.NewText = r.labeler.Value(c.Field, c.New)
			}
		}
		// 可读文本与原始值相同时也留空，前端少渲染一层。
		if c.OldText == c.Old {
			c.OldText = ""
		}
		if c.NewText == c.New {
			c.NewText = ""
		}
		filtered = append(filtered, c)
	}

	if len(filtered) == 0 {
		return "", nil
	}

	out, err := json.Marshal(filtered)
	if err != nil {
		return "", fmt.Errorf("history: 序列化变更失败: %w", err)
	}
	return string(out), nil
}

// encodeIDs 把 id 列表序列化为 JSON 数组；空列表存空串。
func encodeIDs(ids []uint) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	out, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("history: 序列化 id 列表失败: %w", err)
	}
	return string(out), nil
}

// DecodeChanges 解析存储的变更 JSON。
func DecodeChanges(raw string) ([]Change, error) {
	if raw == "" {
		return nil, nil
	}
	var out []Change
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("history: 解析变更失败: %w", err)
	}
	return out, nil
}

// DecodeIDs 解析存储的 id 列表 JSON。
func DecodeIDs(raw string) ([]uint, error) {
	if raw == "" {
		return nil, nil
	}
	var out []uint
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("history: 解析 id 列表失败: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 变更比对辅助
// ---------------------------------------------------------------------------

// Diff 比对两个结构体的字段差异，返回变更列表。
//
// 只处理字符串、数字、布尔、时间与指针这类扁平字段，不递归嵌套结构 ——
// 业务实体里没有需要逐字段追溯的嵌套对象，递归反而会产出难以阅读的
// 路径式字段名（如 "Author.Name"）。
//
// fields 是「字段名 → 中文名」的映射；映射里没有的字段会被跳过，
// 这样调用方可以精确控制哪些字段值得追溯，而不必给整个结构体打标记。
func Diff(oldObj, newObj any, fields map[string]string) []Change {
	oldVal := reflect.Indirect(reflect.ValueOf(oldObj))
	newVal := reflect.Indirect(reflect.ValueOf(newObj))
	if !oldVal.IsValid() || !newVal.IsValid() {
		return nil
	}
	if oldVal.Type() != newVal.Type() {
		return nil
	}

	changes := make([]Change, 0, len(fields))
	for field, label := range fields {
		of := oldVal.FieldByName(field)
		nf := newVal.FieldByName(field)
		// 字段不存在或不可读时跳过，而不是 panic ——
		// 调用方给的映射可能包含已重命名的字段。
		if !of.IsValid() || !nf.IsValid() || !of.CanInterface() || !nf.CanInterface() {
			continue
		}

		oldStr := scalarString(of)
		newStr := scalarString(nf)
		if oldStr == newStr {
			continue
		}
		changes = append(changes, Change{Field: field, Label: label, Old: oldStr, New: newStr})
	}

	// 按字段名排序，保证同样的变更每次产出同样的顺序，
	// 前端渲染与测试断言才有确定性。
	sortChanges(changes)
	return changes
}

// scalarString 把反射值转成可比较的字符串表示。
//
// 对指针做了 nil 判断（nil 产出空串），因为业务实体里大量字段是
// *time.Time / *float64 这类可空类型。
func scalarString(v reflect.Value) string {
	// 解引用指针；nil 指针表示「无值」。
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		if v.Bool() {
			return "1"
		}
		return "0"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		// 用 -1 精度去掉浮点尾随零（3.50 与 3.5 视为相同）。
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case reflect.Struct:
		// 只处理时间类型，其他结构体跳过（返回空串会被当作「无变化」）。
		if t, ok := v.Interface().(time.Time); ok {
			if t.IsZero() {
				return ""
			}
			// RFC3339 便于阅读，且排序结果与时间顺序一致。
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// sortChanges 按字段名排序。
func sortChanges(changes []Change) {
	for i := 1; i < len(changes); i++ {
		for j := i; j > 0 && changes[j].Field < changes[j-1].Field; j-- {
			changes[j], changes[j-1] = changes[j-1], changes[j]
		}
	}
}

// DescribeChange 生成单个变更的可读描述，如「状态：草稿 → 评审中」。
func DescribeChange(c Change) string {
	oldText, newText := c.OldText, c.NewText
	if oldText == "" {
		oldText = c.Old
	}
	if newText == "" {
		newText = c.New
	}
	label := c.Label
	if label == "" {
		label = c.Field
	}

	switch {
	case oldText == "" && newText != "":
		return fmt.Sprintf("%s：设为 %s", label, newText)
	case oldText != "" && newText == "":
		return fmt.Sprintf("%s：清空（原为 %s）", label, oldText)
	default:
		return fmt.Sprintf("%s：%s → %s", label, oldText, newText)
	}
}

// Describe 把变更列表串成一句话，用于通知与日志。
func Describe(changes []Change) string {
	if len(changes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		parts = append(parts, DescribeChange(c))
	}
	return strings.Join(parts, "；")
}
