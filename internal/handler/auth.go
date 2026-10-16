// Package handler 是 HTTP 入参出参层。
//
// 这里只做三件事：绑定并校验请求、调用 service、按统一格式写响应。
// 不含任何业务判断 —— 一旦在 handler 里出现「如果状态是 X 就不允许 Y」
// 这类逻辑，它就同时存在于两个地方，迟早会不一致。
package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/xywf221/xuliu/internal/dto"
	"github.com/xywf221/xuliu/internal/identity"
	"github.com/xywf221/xuliu/internal/pkg/response"
	"github.com/xywf221/xuliu/internal/service"
)

// AuthHandler 处理登录、令牌与个人口令相关请求。
type AuthHandler struct {
	auth *service.AuthService
}

// NewAuthHandler 构造认证 handler。
func NewAuthHandler(auth *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

// Login 处理 POST /auth/login。
//
// @Summary     登录
// @Description 账号口令登录，成功后返回访问令牌、刷新令牌与当前用户信息权限。
// @Tags        认证
// @Accept      json
// @Produce     json
// @Param       req body dto.LoginReq true "登录请求"
// @Success     200 {object} response.Body{data=dto.LoginResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "账号或密码错误"
// @Router      /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	req := dto.LoginReq{}
	if !response.Bind(c, &req) {
		return
	}

	resp, err := h.auth.Login(c.Request.Context(), req)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// Refresh 处理 POST /auth/refresh。
//
// @Summary     刷新访问令牌
// @Description 用刷新令牌换取新的访问令牌。公开接口，访问令牌已过期时仍可调用。
// @Tags        认证
// @Accept      json
// @Produce     json
// @Param       req body dto.RefreshReq true "刷新令牌请求"
// @Success     200 {object} response.Body{data=dto.LoginResp}
// @Failure     400 {object} response.Body "参数不合法"
// @Failure     401 {object} response.Body "刷新令牌无效或已过期"
// @Router      /auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	req := dto.RefreshReq{}
	if !response.Bind(c, &req) {
		return
	}

	resp, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, resp)
}

// Logout 处理 POST /auth/logout。
//
// 无状态 JWT 无法在服务端真正作废令牌，所以这里只做客户端语义：
// 返回成功，由前端清除本地令牌。
//
// 之所以不假装成功、而是保留这个接口：它给了前端一个明确的时机点，
// 也为将来引入令牌黑名单（按 jti）留好了位置。
//
// @Summary     登出
// @Description 无状态 JWT 无法在服务端作废令牌，这里只做客户端语义，由前端清除本地令牌。
// @Tags        认证
// @Produce     json
// @Security    BearerAuth
// @Success     204 {string} string "无内容"
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	response.OK(c, gin.H{"loggedOut": true})
}

// Profile 处理 GET /auth/profile，返回当前登录用户的信息与权限。
//
// @Summary     当前用户信息
// @Description 返回当前登录用户的基本信息与权限点列表，供前端渲染菜单与按钮。
// @Tags        认证
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=dto.ProfileResp}
// @Failure     401 {object} response.Body "未登录或令牌已失效"
// @Router      /auth/profile [get]
func (h *AuthHandler) Profile(c *gin.Context) {
	user := identity.MustUser(c.Request.Context())

	profile, err := h.auth.Profile(c.Request.Context(), user.ID)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, profile)
}

// ChangePassword 处理 POST /auth/password，修改本人口令。
//
// @Summary     修改本人口令
// @Description 只改当前登录人的口令，不收 userId 参数。旧口令校验失败返回 401 —— 那是「凭据不对」，不是「没登录」，但都归在这一类。
// @Tags        认证
// @Accept      json
// @Produce     json
// @Param       req body dto.ChangePasswordReq true "旧口令与新口令"
// @Security    BearerAuth
// @Success     200 {object} response.Body{data=object} "{\"changed\": true}"
// @Failure     400 {object} response.Body "参数不合法（新口令少于 8 位等）"
// @Failure     401 {object} response.Body "未登录，或旧口令不正确"
// @Router      /auth/password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	user := identity.MustUser(c.Request.Context())

	req := dto.ChangePasswordReq{}
	if !response.Bind(c, &req) {
		return
	}

	if err := h.auth.ChangePassword(c.Request.Context(), user.ID, req); err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, gin.H{"changed": true})
}
