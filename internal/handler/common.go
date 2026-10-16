package handler

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/pkg/apperr"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// CommentHandler 处理评论请求。
type CommentHandler struct {
	comments *service.CommentService
}

// NewCommentHandler 构造评论 handler。
func NewCommentHandler(comments *service.CommentService) *CommentHandler {
	return &CommentHandler{comments: comments}
}

// List 处理 GET /comments?objectType=story&objectID=7。
//
// @Summary     评论列表
// @Description 分页查询挂在某业务对象下的评论。评论挂在任意对象上，权限判定复用所属对象的可见性：看不到那条需求的人，也不该看到它的评论。objectType 与 objectID 必填。
// @Tags        评论
// @Produce     json
// @Param       objectType query string true  "对象类型：story/task/bug/project/execution/product/productplan/module"
// @Param       objectID   query int    true  "对象 ID"
// @Param       page       query int    false "页码，默认 1"
// @Param       size       query int    false "每页条数，默认 20，最大 200"
// @Param       order      query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.CommentResp"
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "所属对象不可见"
// @Failure     404 {object} response.Body "所属对象不存在"
// @Router      /comments [get]
func (h *CommentHandler) List(c *gin.Context) {
	req := dto.CommentFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.comments.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}

// Create 处理 POST /comments。
//
// @Summary     发表评论
// @Description 在任意业务对象上发表评论。必须能看到被评论的对象（权限复用所属对象的可见性）；content 非空且不超过 5000 字；files 为附件 id 列表，会一并挂到被评论的对象上。
// @Tags        评论
// @Accept       json
// @Produce     json
// @Param       body body dto.CommentReq true "评论内容"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.CommentResp}
// @Failure     400 {object} response.Body "参数不合法或评论内容为空"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "所属对象不可见"
// @Failure     404 {object} response.Body "所属对象不存在"
// @Router      /comments [post]
func (h *CommentHandler) Create(c *gin.Context) {
	req := dto.CommentReq{}
	if !response.Bind(c, &req) {
		return
	}
	comment, err := h.comments.Create(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, comment)
}

// Delete 处理 DELETE /comments/:id。
//
// @Summary     删除评论
// @Description 删除指定评论。只有评论作者本人或超级管理员能删；同时仍要求能看到评论所属的对象。
// @Tags        评论
// @Produce     json
// @Param       id   path int true  "评论 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "只能删除自己发表的评论"
// @Failure     404 {object} response.Body "评论不存在"
// @Router      /comments/{id} [delete]
func (h *CommentHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.comments.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ---------------------------------------------------------------------------
// 附件
// ---------------------------------------------------------------------------

// FileHandler 处理附件请求。
type FileHandler struct {
	files *service.FileService
}

// NewFileHandler 构造附件 handler。
func NewFileHandler(files *service.FileService) *FileHandler {
	return &FileHandler{files: files}
}

// Upload 处理 POST /files（multipart/form-data）。
//
// 表单字段：
//
//	file        必填，文件内容
//	objectType  可选，挂载目标类型
//	objectID    可选，挂载目标 id
//
// 允许「先上传、后挂载」：表单里上传完文件还没提交时，附件先落库，
// 提交业务对象时再把 id 带回来挂上。
//
// @Summary     上传附件
// @Description multipart/form-data 上传附件。file 必填；objectType/objectID 可选，留空表示先上传、后挂载（此时仅上传者可访问）。单文件上限与扩展名白名单来自配置 storage.local（默认上限 64 MiB）：超出上限返回 413，扩展名不在白名单返回 400，空文件返回 400。给了 objectType/objectID 时先校验挂载目标可见性。
// @Tags        附件
// @Accept       multipart/form-data
// @Produce     json
// @Param       file       formData file   true  "文件内容"
// @Param       objectType formData string false "挂载目标类型，留空表示暂不挂载"
// @Param       objectID   formData int    false "挂载目标 id，留空表示暂不挂载"
// @Security    BearerAuth
// @Success     201 {object} response.Body{data=dto.FileResp}
// @Failure     400 {object} response.Body "未选择文件、文件为空、扩展名不在白名单或 objectID 非法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "挂载目标不可见"
// @Failure     404 {object} response.Body "挂载目标不存在"
// @Failure     413 {object} response.Body "附件超出大小限制"
// @Failure     503 {object} response.Body "附件存储未启用"
// @Router      /files [post]
func (h *FileHandler) Upload(c *gin.Context) {
	header, err := c.FormFile("file")
	if err != nil {
		response.Fail(c, apperr.Newf(apperr.InvalidParam, "请选择要上传的文件"))
		return
	}

	src, err := header.Open()
	if err != nil {
		response.Fail(c, apperr.Wrap(apperr.FileStoreFailed, err))
		return
	}
	defer func() { _ = src.Close() }()

	var objectID uint
	if raw := c.PostForm("objectID"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.Fail(c, invalidID("objectID", raw))
			return
		}
		objectID = uint(parsed)
	}

	file, err := h.files.Upload(c.Request.Context(), service.UploadInput{
		Name:        header.Filename,
		Size:        header.Size,
		ContentType: header.Header.Get("Content-Type"),
		Reader:      src,
		ObjectType:  c.PostForm("objectType"),
		ObjectID:    objectID,
	})
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.Created(c, file)
}

// List 处理 GET /files?objectType=story&objectID=7。
//
// @Summary     附件列表
// @Description 查询挂在某业务对象下的全部附件（不分页）。权限判定复用所属对象的可见性；尚未挂载的附件不出现在任何列表里。objectType 与 objectID 必填。
// @Tags        附件
// @Produce     json
// @Param       objectType query string true "对象类型"
// @Param       objectID   query int    true "对象 ID"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=[]dto.FileResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "所属对象不可见"
// @Failure     404 {object} response.Body "所属对象不存在"
// @Router      /files [get]
func (h *FileHandler) List(c *gin.Context) {
	req := dto.FileFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	files, err := h.files.List(c.Request.Context(), req.ObjectType, req.ObjectID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, files)
}

// Download 处理 GET /files/:id/download。
//
// 不用 c.File() 之类的静态文件助手：那会把「怎么读」交给框架，
// 而我们读的是对象存储，需要通过 Storage 接口取流。
//
// @Summary     下载附件
// @Description 按 ID 下载附件内容，返回二进制流而非 JSON。响应头带 Content-Disposition: attachment（文件名经 RFC 2231 编码，中文名不乱码）、Content-Type（取附件元数据，缺失时回退 application/octet-stream）与已知大小时的 Content-Length；成功后累加下载次数。令牌除 Authorization 头外，也接受 ?access_token= 查询参数（浏览器直接打开下载链接的场景）。
// @Tags        附件
// @Produce     octet-stream
// @Param       id   path int true  "附件 ID"
// @Security    BearerAuth
// @Success     200 {file} file "附件内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "无权访问该附件"
// @Failure     404 {object} response.Body "附件不存在"
// @Failure     503 {object} response.Body "附件存储未启用"
// @Router      /files/{id}/download [get]
func (h *FileHandler) Download(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}

	file, reader, err := h.files.Download(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, err)
		return
	}
	defer func() { _ = reader.Close() }()

	// 文件名可能含中文，必须走 RFC 2231 编码，
	// 否则浏览器拿到的会是乱码或被截断的文件名。
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	c.Header("Content-Disposition", disposition)
	if file.ContentType != "" {
		c.Header("Content-Type", file.ContentType)
	} else {
		c.Header("Content-Type", "application/octet-stream")
	}
	// 表达「长度已知，可显示进度条」；写失败时连接断开，客户端能察觉。
	if file.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(file.Size, 10))
	}
	c.Status(http.StatusOK)

	if _, err := io.Copy(c.Writer, reader); err != nil {
		// 响应头已经发出去了，这里没有「改成一个错误响应」的余地，
		// 只能记日志。客户端会因为长度不符而认为下载失败。
		_ = c.Error(err)
	}
}

// Delete 处理 DELETE /files/:id。
//
// @Summary     删除附件
// @Description 删除附件：先删元信息记录，再删存储中的对象（对象删除失败只记日志，不影响用户视角的删除结果）。只有上传者本人或超级管理员能删；可见性按已挂载对象判定，未挂载的仅上传者可访问。
// @Tags        附件
// @Produce     json
// @Param       id   path int true  "附件 ID"
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "只能删除自己上传的附件"
// @Failure     404 {object} response.Body "附件不存在"
// @Router      /files/{id} [delete]
func (h *FileHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.files.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// ---------------------------------------------------------------------------
// 操作历史
// ---------------------------------------------------------------------------

// ActionHandler 处理操作历史请求。
type ActionHandler struct {
	actions *service.ActionService
}

// NewActionHandler 构造操作历史 handler。
func NewActionHandler(actions *service.ActionService) *ActionHandler {
	return &ActionHandler{actions: actions}
}

// List 处理 GET /actions。
//
// @Summary     操作历史列表
// @Description 分页查询只读的操作留痕，供详情页的时间线展示。必须给出至少一个查询范围：objectType+objectID（某对象的完整时间线）、productId、projectId、executionId 或 actorId，否则返回 400。按对象查时先校验该对象的可见性；其余查询按当前用户可见的产品/项目过滤。
// @Tags        操作历史
// @Produce     json
// @Param       objectType   query string false "对象类型，与 objectID 成对使用"
// @Param       objectID     query int    false "对象 ID，与 objectType 成对使用"
// @Param       productId    query int    false "按产品过滤"
// @Param       projectId    query int    false "按项目过滤"
// @Param       executionId  query int    false "按迭代过滤"
// @Param       actorId      query int    false "按操作人过滤"
// @Param       page         query int    false "页码，默认 1"
// @Param       size         query int    false "每页条数，默认 20，最大 200"
// @Param       order        query string false "排序，如 id_desc"
// @Security    BearerAuth
// @Success     200 {object} response.Body "分页对象，data 为 {list,total,page,size}，list 元素为 dto.ActionResp"
// @Failure     400 {object} response.Body "未指定任何查询范围"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Failure     403 {object} response.Body "所属对象不可见"
// @Failure     404 {object} response.Body "所属对象不存在"
// @Router      /actions [get]
func (h *ActionHandler) List(c *gin.Context) {
	req := dto.ActionFilterReq{}
	if !response.Bind(c, &req) {
		return
	}
	result, err := h.actions.List(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, result)
}
