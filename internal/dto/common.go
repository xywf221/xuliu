package dto

// 通用对象：评论、附件、操作历史。
//
// 三者都挂在「任意业务对象」上，故用 objectType + objectID 定位。
// objectType 取值见 model.ObjectTypeStory 等常量。

// ---------------------------------------------------------------------------
// 评论
// ---------------------------------------------------------------------------

// CommentReq 是发表评论的请求。
type CommentReq struct {
	ObjectType string `json:"objectType" validate:"required,oneof=story task bug project execution product productplan module"`
	ObjectID   uint   `json:"objectID" validate:"required"`
	Content    string `json:"content" validate:"required,max=5000"`
	// Files 是评论附带的附件 id。
	Files []uint `json:"files"`
}

// CommentResp 是评论出参。
type CommentResp struct {
	ID         uint   `json:"id"`
	ObjectType string `json:"objectType"`
	ObjectID   uint   `json:"objectID"`

	UserID   uint   `json:"userId"`
	UserName string `json:"userName,omitempty"`
	// Avatar 便于前端直接渲染头像，不必再查一次用户。
	Avatar string `json:"avatar,omitempty"`

	Content   string     `json:"content"`
	CreatedAt string     `json:"createdAt"`
	Files     []FileResp `json:"files,omitempty"`
}

// CommentFilterReq 是评论列表的查询参数。
type CommentFilterReq struct {
	ObjectType string `form:"objectType" validate:"required"`
	ObjectID   uint   `form:"objectID" validate:"required"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// ---------------------------------------------------------------------------
// 附件
// ---------------------------------------------------------------------------

// FileResp 是附件出参。
//
// 刻意不返回存储路径（Path）：那是后端实现细节，暴露出去既无用又是线索。
// 前端要下载就走带鉴权的 /files/:id/download。
type FileResp struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// SizeText 是人读的大小（如 "1.2 MB"），前端不必自己写换算。
	SizeText string `json:"sizeText"`
	// Ext 是扩展名（含点），供前端挑图标。
	Ext         string `json:"ext"`
	ContentType string `json:"contentType"`
	Downloads   int    `json:"downloads"`

	UserID    uint   `json:"userId"`
	UserName  string `json:"userName,omitempty"`
	CreatedAt string `json:"createdAt"`

	// URL 仅在后端支持直链（如配了 CDN 的 S3）时非空。
	// 为空时前端走 /files/:id/download。
	URL string `json:"url,omitempty"`
}

// FileFilterReq 是附件列表的查询参数。
type FileFilterReq struct {
	ObjectType string `form:"objectType" validate:"required"`
	ObjectID   uint   `form:"objectID" validate:"required"`
}

// ---------------------------------------------------------------------------
// 操作历史
// ---------------------------------------------------------------------------

// ActionFilterReq 是操作历史的查询参数。
//
// 三种用法：
//
//	?objectType=story&objectID=7     某个对象的完整时间线
//	?projectId=3                     某项目下的全部动态
//	?actorId=9                      某人做过什么
type ActionFilterReq struct {
	ObjectType  string `form:"objectType"`
	ObjectID    uint   `form:"objectID"`
	ProductID   uint   `form:"productId"`
	ProjectID   uint   `form:"projectId"`
	ExecutionID uint   `form:"executionId"`
	ActorID     uint   `form:"actorId"`

	Page  int    `form:"page"`
	Size  int    `form:"size"`
	Order string `form:"order"`
}

// ActionResp 是操作历史出参。
type ActionResp struct {
	ID uint `json:"id"`
	// ObjectType/ObjectID 让前端能跳到对应对象的详情页。
	ObjectType string `json:"objectType"`
	ObjectID   uint   `json:"objectID"`

	ActorID   uint   `json:"actorId"`
	ActorName string `json:"actorName,omitempty"`
	Avatar    string `json:"avatar,omitempty"`

	Action string `json:"action"`
	// ActionText 是动词的中文名（如「解决」）。
	ActionText string `json:"actionText"`
	Date       string `json:"date"`

	Comment string `json:"comment"`
	// Extra 是附加信息：指派存目标用户名、解决存原因、状态变更存 "old|new"。
	Extra string `json:"extra"`
	// Changes 是字段级变更，已可读化（含中文名与中文值）。
	Changes []ActionChangeResp `json:"changes,omitempty"`
	// Files 是本次操作附带的附件。
	Files []FileResp `json:"files,omitempty"`
}

// ActionChangeResp 是字段级变更的出参。
type ActionChangeResp struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Old   string `json:"old"`
	New   string `json:"new"`
	// OldText/NewText 是已可读化的值，前端优先显示这两个。
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText,omitempty"`
	// Text 是拼好的整句（如「状态：草稿 → 激活」），时间线里直接显示。
	Text string `json:"text"`
}
