package dto

// 本文件是部门与权限组的请求/响应结构。

// DeptResp 是部门出参（扁平结构，前端自行组树）。
type DeptResp struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	ParentID uint   `json:"parentId"`
	Path     string `json:"path"`
	Grade    int    `json:"grade"`
	Order    int    `json:"order"`

	ManagerID   uint   `json:"managerId"`
	ManagerName string `json:"managerName"`

	// UserCount 是直属本部门的用户数（不含子部门）。
	UserCount int64 `json:"userCount"`
	// HasChildren 让前端知道是否显示展开箭头。
	HasChildren bool `json:"hasChildren"`

	// Children 仅在返回树形结构时填充。
	Children []DeptResp `json:"children,omitempty"`
}

// CreateDeptReq 是新建部门请求。
type CreateDeptReq struct {
	Name     string `json:"name" validate:"required,max=60"`
	ParentID uint   `json:"parentId"`
	Manager  uint   `json:"manager"`
	Order    int    `json:"order"`
}

// UpdateDeptReq 是编辑部门请求。
type UpdateDeptReq struct {
	Name     *string `json:"name" validate:"omitempty,max=60"`
	ParentID *uint   `json:"parentId"`
	Manager  *uint   `json:"manager"`
	Order    *int    `json:"order"`
}

// GroupResp 是权限组出参。
type GroupResp struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	JobRole string `json:"jobRole"`
	Desc    string `json:"desc"`
	// ProjectID 为 0 表示全局组。
	ProjectID uint `json:"projectId"`

	// Privs 是权限点列表，仅详情接口填充。
	Privs []string `json:"privs,omitempty"`
	// PrivCount 是权限点数量，列表接口用它替代 Privs 以免响应过大。
	PrivCount int `json:"privCount"`
	// UserCount 是组内成员数。
	UserCount int64 `json:"userCount"`

	// Users 是组内成员，仅详情接口填充。
	Users []UserBrief `json:"users,omitempty"`
}

// CreateGroupReq 是新建权限组请求。
type CreateGroupReq struct {
	Name    string   `json:"name" validate:"required,max=60"`
	JobRole string   `json:"jobRole" validate:"omitempty,max=10"`
	Desc    string   `json:"desc" validate:"max=255"`
	Privs   []string `json:"privs"`
}

// UpdateGroupReq 是编辑权限组请求。
type UpdateGroupReq struct {
	Name    *string `json:"name" validate:"omitempty,max=60"`
	JobRole *string `json:"jobRole" validate:"omitempty,max=10"`
	Desc    *string `json:"desc" validate:"omitempty,max=255"`
	// Privs 为 nil 表示不改动权限点；传空数组表示清空全部权限。
	Privs *[]string `json:"privs"`
}

// PrivGroupResp 是按模块分组的权限点，供权限配置界面渲染。
type PrivGroupResp struct {
	Module string     `json:"module"`
	Name   string     `json:"name"`
	Privs  []PrivResp `json:"privs"`
}

// PrivResp 是单个权限点。
type PrivResp struct {
	Priv   string `json:"priv"`
	Module string `json:"module"`
	Method string `json:"method"`
	Name   string `json:"name"`
	// Held 表示目标组是否已持有该权限点（仅在查询某个组时填充）。
	Held bool `json:"held"`
}
