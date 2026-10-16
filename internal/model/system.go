package model

import "time"

// 本文件是系统层实体：公司、部门、用户、权限组。

// Company 公司信息。单实例，整个系统只有一行。
//
// Admins 是超级管理员账号列表，对应禅道的 zt_company.admins。
// 超管绕过所有权限点校验，但**不绕过**数据级可见性
// （私有产品仍要看白名单），这一点在 acl 里体现。
type Company struct {
	BaseModel
	Name    string `gorm:"size:120;not null" json:"name"`
	Phone   string `gorm:"size:30" json:"phone"`
	Fax     string `gorm:"size:30" json:"fax"`
	Address string `gorm:"size:200" json:"address"`
	Zipcode string `gorm:"size:10" json:"zipcode"`
	Website string `gorm:"size:200" json:"website"`
	// Admins 用逗号分隔的账号列表。
	Admins string `gorm:"size:500" json:"admins"`
	// Guest 是访客账号。
	Guest string `gorm:"size:30" json:"guest"`
}

func (Company) TableName() string { return "sys_company" }

// Dept 部门，树形结构。
//
// Path 存祖先 id 的逗号串（如 "1,4,9"），Grade 是层级深度（从 1 开始）。
// 这种「物化路径」方案让「查某部门下所有子孙」只需一次 LIKE 前缀匹配，
// 比递归查询简单，且三种数据库行为一致。
type Dept struct {
	BaseModel
	Name     string `gorm:"size:60;not null;index" json:"name"`
	ParentID uint   `gorm:"index;not null;default:0" json:"parentId"`
	Path     string `gorm:"size:255;index" json:"path"`
	Grade    int    `gorm:"not null;default:1" json:"grade"`
	Order    int    `gorm:"not null;default:0" json:"order"`
	// Manager 是部门负责人（用户 id）。
	Manager uint `json:"manager"`
}

func (Dept) TableName() string { return "sys_dept" }

// User 用户。
//
// Password 存 bcrypt 哈希，不存明文也不存 md5（禅道用 md5，已不适用）。
// 注意 bcrypt 有 72 字节输入上限，超长口令在 hash 包内先做 sha256 预哈希。
type User struct {
	BaseModel
	CompanyID uint   `gorm:"index" json:"companyId"`
	DeptID    uint   `gorm:"index" json:"deptId"`
	Account   string `gorm:"size:30;uniqueIndex;not null" json:"account"`
	Password  string `gorm:"size:100;not null" json:"-"` // 永不出参

	Type     string `gorm:"size:10;not null;default:inside" json:"type"`
	Realname string `gorm:"size:60;not null;index" json:"realname"`
	Nickname string `gorm:"size:60" json:"nickname"`
	// JobRole 是职位（研/测/产品……），对应禅道的 roleList。
	JobRole string `gorm:"size:10" json:"jobRole"`
	// Superior 是直属上级（用户 id）。
	Superior uint   `gorm:"index" json:"superior"`
	Pinyin   string `gorm:"size:120" json:"pinyin"`

	Avatar   string     `gorm:"size:255" json:"avatar"`
	Birthday *time.Time `json:"birthday"`
	Gender   string     `gorm:"size:1" json:"gender"`

	Email    string `gorm:"size:120;index" json:"email"`
	Mobile   string `gorm:"size:30" json:"mobile"`
	Phone    string `gorm:"size:30" json:"phone"`
	Weixin   string `gorm:"size:60" json:"weixin"`
	DingTalk string `gorm:"size:60" json:"dingtalk"`
	Skype    string `gorm:"size:60" json:"skype"`
	QQ       string `gorm:"size:30" json:"qq"`

	Address string `gorm:"size:200" json:"address"`
	Zipcode string `gorm:"size:10" json:"zipcode"`

	// Join 是入职日期。
	Join *time.Time `json:"join"`
	// Visits 是登录次数；Last 是最后一次登录时间。
	Visits int        `gorm:"not null;default:0" json:"visits"`
	Last   *time.Time `json:"last"`

	// 登录失败计数与锁定时间，用于防暴力破解。
	Fails  int        `gorm:"not null;default:0" json:"-"`
	Locked *time.Time `json:"-"`

	// 口令重置令牌（sha256 后存储）与过期时间。
	ResetToken   string     `gorm:"size:64;index" json:"-"`
	ResetExpired *time.Time `json:"-"`

	// Disabled 表达「停用」。与软删除区分：停用保留全部历史关联，
	// 因为需求/任务的经办人外键仍指向该用户。
	Disabled bool `gorm:"not null;default:false;index" json:"disabled"`
}

func (User) TableName() string { return "sys_user" }

// IsActive 报告账号是否可用于登录。
func (u *User) IsActive() bool { return !u.Disabled && u.DeletedAt.Time.IsZero() }

// IsLockedAt 报告账号在给定时刻是否处于锁定中。
//
// 锁定是自动的、临时的（连续登录失败触发）；Disabled 是管理员主动的、
// 持续的。两者要分开判断，否则「锁定期已过」的账号会被永久拒绝。
func (u *User) IsLockedAt(now time.Time, lockDuration time.Duration) bool {
	if u.Locked == nil {
		return false
	}
	return now.Before(u.Locked.Add(lockDuration))
}

// DisplayName 返回用于界面展示的名字。
//
// 优先真实姓名，缺失时回退账号 —— 界面上任何位置都不应出现空白，
// 否则「指派给谁」「谁操作的」这类信息就断了。
func (u *User) DisplayName() string {
	if u.Realname != "" {
		return u.Realname
	}
	return u.Account
}

// Group 权限组。
//
// 用户 → 组（多对多）→ 权限点集合。权限点是 "module-method" 字符串，
// 如 "bug-resolve"，沿用禅道的命名以便对照。
type Group struct {
	BaseModel
	Name string `gorm:"size:60;not null;index" json:"name"`
	// JobRole 是组的默认职位，新建用户时的便捷默认值。
	JobRole string `gorm:"size:10" json:"jobRole"`
	Desc    string `gorm:"size:255" json:"desc"`
	// ProjectID 让组可以只在某个项目内生效，0 表示全局。
	ProjectID uint `gorm:"index;not null;default:0" json:"projectId"`
}

func (Group) TableName() string { return "sys_group" }

// GroupPriv 组的权限点。(GroupID, Perm) 唯一。
//
// 拆成独立表而不是在 Group 上存一个 CSV 字段：
// 前者能用「有哪些组拥有 bug-resolve」这种反查来排查权限问题，
// 后者只能全表扫。
type GroupPriv struct {
	ID      uint `gorm:"primaryKey" json:"id"`
	GroupID uint `gorm:"index;not null;uniqueIndex:uk_group_perm" json:"groupId"`
	// Perm 形如 "bug-resolve"，用 module-method 命名。
	Perm string `gorm:"size:60;not null;uniqueIndex:uk_group_perm" json:"perm"`
}

func (GroupPriv) TableName() string { return "sys_group_priv" }

// UserGroup 用户与组的关联。
type UserGroup struct {
	ID      uint `gorm:"primaryKey" json:"id"`
	UserID  uint `gorm:"index;not null;uniqueIndex:uk_user_group" json:"userId"`
	GroupID uint `gorm:"index;not null;uniqueIndex:uk_user_group" json:"groupId"`
	// ProjectID 让同一个组在不同项目里可有不同归属，0 表示全局。
	ProjectID uint `gorm:"index;not null;default:0" json:"projectId"`
}

func (UserGroup) TableName() string { return "sys_user_group" }

// Action 操作历史。
//
// 相比禅道的 zt_action + zt_history 两张表，这里合成一张，
// 字段级 diff 以 JSON 存在 Changes 里。理由：diff 只用于渲染时间线，
// 从不作为查询条件，拆表只会带来无谓的 join。
//
// Extra 的语义按动作类型约定：
//   - 指派动作：存目标用户 id
//   - 解决/关闭：存原因
//   - 状态变更：存 "old|new"，需求的「重新激活」靠它恢复前一个状态
type Action struct {
	ID uint `gorm:"primaryKey" json:"id"`

	ObjectType string `gorm:"size:30;not null;index:idx_action_object,priority:1" json:"objectType"`
	ObjectID   uint   `gorm:"not null;index:idx_action_object,priority:2" json:"objectID"`

	// 冗余的产品/项目/迭代 id，便于「某项目下所有动态」这类查询免 join。
	ProductID   uint `gorm:"index" json:"productId"`
	ProjectID   uint `gorm:"index" json:"projectId"`
	ExecutionID uint `gorm:"index" json:"executionId"`

	// ActorID 是操作者。
	ActorID uint `gorm:"index;not null" json:"actorId"`
	// Action 动词，取值见 ActionTypes。
	Action string    `gorm:"size:40;not null;index" json:"action"`
	Date   time.Time `gorm:"index;not null" json:"date"`

	Comment string `gorm:"type:text" json:"comment"`
	Extra   string `gorm:"size:255" json:"extra"`

	// Changes 是字段级变更列表，JSON 数组：{"field","old","new"}。
	// 用 text 存而非 jsonb，保持三库一致。
	Changes string `gorm:"type:text" json:"changes"`
	// Files 是本次操作附带的附件 id 列表（JSON 数组）。
	Files string `gorm:"type:text" json:"files"`

	Read     bool `gorm:"not null;default:false;index" json:"read"`
	Efforted bool `gorm:"not null;default:false" json:"efforted"`
}

func (Action) TableName() string { return "sys_action" }

// Action 动词取值。对照禅道 module/action/lang/zh-cn.php 的 label 定义。
const (
	ActionOpened       = "opened"         // 创建
	ActionEdited       = "edited"         // 修改
	ActionAssigned     = "assigned"       // 指派
	ActionClosed       = "closed"         // 关闭
	ActionActivated    = "activated"      // 激活
	ActionDeleted      = "deleted"        // 删除
	ActionUndeleted    = "undeleted"      // 还原
	ActionCommented    = "commented"      // 备注
	ActionResolved     = "resolved"       // 解决
	ActionReviewed     = "reviewed"       // 评审
	ActionSubmitReview = "submitReview"   // 提交评审
	ActionRecalled     = "recalled"       // 撤销评审
	ActionFinished     = "finished"       // 完成
	ActionStarted      = "started"        // 开始
	ActionPaused       = "paused"         // 暂停
	ActionRestarted    = "restarted"      // 继续
	ActionCanceled     = "canceled"       // 取消
	ActionConfirmed    = "confirmed"      // 确认
	ActionVerified     = "verified"       // 验收
	ActionLinked       = "linked"         // 关联
	ActionUnlinked     = "unlinked"       // 取消关联
	ActionRecordWork   = "RecordWorkhour" // 记录工时
	ActionRestored     = "restored"       // 从回收站还原
	ActionMoved        = "moved"          // 模块调整
)

// 操作历史的对象类型。
const (
	ObjectTypeStory     = "story"
	ObjectTypeTask      = "task"
	ObjectTypeBug       = "bug"
	ObjectTypeProject   = "project"
	ObjectTypeExecution = "execution"
	ObjectTypeProduct   = "product"
	ObjectTypePlan      = "productplan"
	ObjectTypeModule    = "module"
	ObjectTypeUser      = "user"
	ObjectTypeCase      = "case"
)

// Notice 站内通知。
type Notice struct {
	BaseModel
	// ToUser 是接收者。
	ToUser uint `gorm:"index;not null" json:"toUser"`
	// FromUser 是触发者，0 表示系统。
	FromUser uint `gorm:"index" json:"fromUser"`

	ObjectType string `gorm:"size:30;not null" json:"objectType"`
	ObjectID   uint   `gorm:"not null" json:"objectID"`

	// Title 是简述，Content 是详情。
	Title   string `gorm:"size:255;not null" json:"title"`
	Content string `gorm:"type:text" json:"content"`
	// Link 是前端跳转路径。
	Link string `gorm:"size:255" json:"link"`

	Read   bool       `gorm:"not null;default:false;index" json:"read"`
	ReadAt *time.Time `json:"readAt"`
}

func (Notice) TableName() string { return "sys_notice" }

// Comment 备注/评论。任意对象都可挂评论，故用多态双列。
type Comment struct {
	BaseModel
	ObjectType string `gorm:"size:30;not null;index:idx_comment_object,priority:1" json:"objectType"`
	ObjectID   uint   `gorm:"not null;index:idx_comment_object,priority:2" json:"objectID"`

	UserID  uint   `gorm:"index;not null" json:"userId"`
	Content string `gorm:"type:text;not null" json:"content"`
	// Files 是附带的附件 id 列表（JSON 数组）。
	Files string `gorm:"type:text" json:"files"`
}

func (Comment) TableName() string { return "sys_comment" }

// File 附件元信息。实际内容由 storage 包按 Backend 落盘/上云。
type File struct {
	BaseModel
	// ObjectType/ObjectID 指向附件所属的业务对象；为空的附件是「已上传但未挂载」。
	ObjectType string `gorm:"size:30;index:idx_file_object,priority:1" json:"objectType"`
	ObjectID   uint   `gorm:"index:idx_file_object,priority:2" json:"objectID"`

	// Name 是用户可见的原始文件名。
	Name string `gorm:"size:255;not null" json:"name"`
	// Path 是存储后端内的相对路径（本地）或对象键（S3）。
	// 绝不使用用户提供的文件名作为路径，见 storage 包。
	Path string `gorm:"size:500;not null" json:"-"`
	// Backend 记录写入时的后端，便于切换存储后仍能读回老附件。
	Backend string `gorm:"size:20;not null;default:local" json:"backend"`

	Size        int64  `gorm:"not null;default:0" json:"size"`
	ContentType string `gorm:"size:150" json:"contentType"`
	// Hash 是内容 sha256，用于秒传与去重。
	Hash string `gorm:"size:64;index" json:"hash"`

	UserID uint `gorm:"index;not null" json:"userId"`

	// Downloads 统计下载次数。
	Downloads int `gorm:"not null;default:0" json:"downloads"`
	// Extra 供各存储后端记录特有信息（如 S3 的 ETag）。
	Extra string `gorm:"size:500" json:"-"`
}

func (File) TableName() string { return "sys_file" }

// Config 系统配置键值对。给「不需要改表结构就能调的行为开关」用。
type Config struct {
	ID    uint   `gorm:"primaryKey" json:"id"`
	Key   string `gorm:"size:100;uniqueIndex;not null" json:"key"`
	Value string `gorm:"type:text" json:"value"`
	Desc  string `gorm:"size:255" json:"desc"`
}

func (Config) TableName() string { return "sys_config" }
