// Package dto 定义 HTTP 层的请求与响应结构。
//
// 与 model 分开的理由：
//
//   - 请求字段的校验规则（validator tag）不属于领域模型；
//   - 响应要控制暴露面（如口令字段绝不能出参）；
//   - 二者演进节奏不同（加一个筛选参数不该动表结构）。
package dto

// LoginReq 是登录请求。
type LoginReq struct {
	Account  string `json:"account" form:"account" validate:"required,max=30"`
	Password string `json:"password" form:"password" validate:"required,max=128"`
	// Remember 为 true 时前端应保存刷新令牌以便长期免登录。
	Remember bool `json:"remember" form:"remember"`
}

// LoginResp 是登录成功的响应。
type LoginResp struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	TokenType    string `json:"tokenType"`
	// Profile 顺带返回用户信息与权限，省掉前端登录后再发一次请求。
	Profile *ProfileResp `json:"profile"`
}

// RefreshReq 是刷新令牌请求。
type RefreshReq struct {
	RefreshToken string `json:"refreshToken" validate:"required"`
}

// ChangePasswordReq 是修改本人口令的请求。
type ChangePasswordReq struct {
	OldPassword string `json:"oldPassword" validate:"required,max=128"`
	NewPassword string `json:"newPassword" validate:"required,min=8,max=128"`
}

// ProfileResp 是当前登录用户的信息与权限。
//
// Rights 是权限点列表，前端据此渲染菜单与按钮；是一份「当前用户能做什么」
// 的完整事实，与后端判定用的是同一份数据，不会漂移。
type ProfileResp struct {
	ID       uint   `json:"id"`
	Account  string `json:"account"`
	Realname string `json:"realname"`
	Email    string `json:"email"`
	Mobile   string `json:"mobile"`
	Avatar   string `json:"avatar"`
	JobRole  string `json:"jobRole"`
	DeptID   uint   `json:"deptId"`
	DeptName string `json:"deptName"`
	IsSuper  bool   `json:"isSuper"`
	// Rights 是权限点字符串列表。
	Rights []string `json:"rights"`
	// Groups 是所属权限组名称，供个人中心展示。
	Groups []string `json:"groups"`

	// DisplayName 是界面上显示的名字（真实姓名优先，回退账号）。
	DisplayName string `json:"displayName"`

	Last   *string `json:"last"`
	Visits int     `json:"visits"`
}

// UserResp 是用户列表/详情出参。
type UserResp struct {
	ID       uint   `json:"id"`
	Account  string `json:"account"`
	Realname string `json:"realname"`
	Nickname string `json:"nickname"`
	JobRole  string `json:"jobRole"`
	Type     string `json:"type"`

	DeptID   uint   `json:"deptId"`
	DeptName string `json:"deptName"`
	// Gender 取 m/f。
	Gender string `json:"gender"`

	Email  string `json:"email"`
	Mobile string `json:"mobile"`
	Phone  string `json:"phone"`
	Weixin string `json:"weixin"`
	QQ     string `json:"qq"`

	Avatar string `json:"avatar"`
	// SuperiorID/SuperiorName 是直属上级。
	SuperiorID   uint   `json:"superiorId"`
	SuperiorName string `json:"superiorName"`

	Join   *string `json:"join"`
	Last   *string `json:"last"`
	Visits int     `json:"visits"`

	Disabled bool `json:"disabled"`
	// Locked 表示账号因连续登录失败而处于锁定中。
	Locked bool `json:"locked"`

	// Groups 是所属权限组，仅在详情接口填充（列表接口留空以免 N+1）。
	Groups []GroupBrief `json:"groups,omitempty"`
}

// GroupBrief 是权限组的简要信息。
type GroupBrief struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// CreateUserReq 是新建用户请求。
type CreateUserReq struct {
	Account  string `json:"account" validate:"required,min=2,max=30,alphanum"`
	Password string `json:"password" validate:"required,min=8,max=128"`
	Realname string `json:"realname" validate:"required,max=60"`
	Nickname string `json:"nickname" validate:"max=60"`

	// Account 之外的字段都可选，由管理员按需填写。
	DeptID     uint   `json:"deptId"`
	JobRole    string `json:"jobRole" validate:"omitempty,oneof=dev qa pm po td pd qd top others"`
	Type       string `json:"type" validate:"omitempty,oneof=inside outside guest"`
	Gender     string `json:"gender" validate:"omitempty,oneof=m f"`
	Email      string `json:"email" validate:"omitempty,email,max=120"`
	Mobile     string `json:"mobile" validate:"max=30"`
	Phone      string `json:"phone" validate:"max=30"`
	Weixin     string `json:"weixin" validate:"max=60"`
	QQ         string `json:"qq" validate:"max=30"`
	SuperiorID uint   `json:"superiorId"`
	// Groups 是所属权限组 id 列表。
	Groups []uint `json:"groups"`
	// Join 是入职日期，格式 YYYY-MM-DD。
	Join string `json:"join" validate:"omitempty,datetime=2006-01-02"`
}

// UpdateUserReq 是编辑用户请求。
//
// 所有字段用指针：区分「未提供该字段」（nil，不改）
// 与「提供了空值」（如把手机号清空）。若用值类型，
// 清空手机号这类操作会被静默忽略。
type UpdateUserReq struct {
	Realname   *string `json:"realname" validate:"omitempty,max=60"`
	Nickname   *string `json:"nickname" validate:"omitempty,max=60"`
	DeptID     *uint   `json:"deptId"`
	JobRole    *string `json:"jobRole" validate:"omitempty,oneof=dev qa pm po td pd qd top others"`
	Type       *string `json:"type" validate:"omitempty,oneof=inside outside guest"`
	Gender     *string `json:"gender" validate:"omitempty,oneof=m f"`
	Email      *string `json:"email" validate:"omitempty,email,max=120"`
	Mobile     *string `json:"mobile" validate:"omitempty,max=30"`
	Phone      *string `json:"phone" validate:"omitempty,max=30"`
	Weixin     *string `json:"weixin" validate:"omitempty,max=60"`
	QQ         *string `json:"qq" validate:"omitempty,max=30"`
	SuperiorID *uint   `json:"superiorId"`
	Join       *string `json:"join" validate:"omitempty,datetime=2006-01-02"`
	// Disabled 用于停用/启用账号。
	Disabled *bool `json:"disabled"`
	// Groups 为 nil 表示不改动分组；传空数组表示清空全部分组。
	Groups *[]uint `json:"groups"`
}

// ResetPasswordReq 是管理员重置他人口令的请求。
type ResetPasswordReq struct {
	NewPassword string `json:"newPassword" validate:"required,min=8,max=128"`
}

// UserFilterReq 是用户列表的查询参数。
type UserFilterReq struct {
	DeptID uint `form:"deptId"`
	// IncludeSubDept 是否包含子部门。
	IncludeSubDept bool   `form:"includeSubDept"`
	JobRole        string `form:"jobRole"`
	Type           string `form:"type"`
	// Disabled 留空表示不过滤。
	Disabled *bool  `form:"disabled"`
	Keywords string `form:"keywords"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// UserBrief 是用户的极简信息，用于下拉选择与列表里的「经办人」列。
type UserBrief struct {
	ID       uint   `json:"id"`
	Account  string `json:"account"`
	Realname string `json:"realname"`
	Avatar   string `json:"avatar"`
	// DisplayName 便于前端直接显示，不必自己拼回退逻辑。
	DisplayName string `json:"displayName"`
	DeptName    string `json:"deptName,omitempty"`
}
