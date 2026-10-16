// Package identity 定义「当前请求是谁」以及「它能做什么」这两个最小抽象。
//
// 单独成包是为了打断依赖环：middleware 需要知道当前用户与权限判定接口，
// 而 acl 需要引用用户类型。若把这两个类型放进其中任一方，另一方就得反向
// 依赖它，形成环。放在这里，双方都只依赖本包。
package identity

import "context"

// User 是挂在请求上下文上的登录用户快照。
//
// 刻意只保留鉴权与展示所需的最小字段：真正的用户实体在 model 包里，
// 若把它整个塞进 context，会诱使各处在 context 上做数据库查询。
type User struct {
	ID       uint
	Account  string
	Realname string
	DeptID   uint
	// IsSuper 为 true 时跳过所有权限点校验（对应 sys_company.admins）。
	IsSuper bool
	// Rights 是已解析的权限点集合，形如 {"bug-resolve": true}。
	// 权限点命名沿用禅道的 module-method 形式。
	Rights map[string]bool
	// GroupIDs 是用户所属权限组，供需要按组判定的场景使用。
	GroupIDs []uint
}

// Has 报告用户是否拥有指定权限点。超管恒为 true。
func (u *User) Has(perm string) bool {
	if u == nil {
		return false
	}
	if u.IsSuper {
		return true
	}
	return u.Rights[perm]
}

// PermChecker 由 acl 包的 Enforcer 实现，供中间件做权限判定。
//
// 定义成接口而非直接依赖 acl 包，是为了让中间件可以在测试里
// 用一行假实现替换掉真实的权限计算。
type PermChecker interface {
	Can(user *User, perm string) bool
}

// contextKey 是私有类型，避免与其他包的 context 键冲突。
type contextKey struct{}

// userKey 是用户快照在 context 里的键。
var userKey = contextKey{}

// WithUser 把登录用户放入 context。
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// CurrentUser 取出当前登录用户。未登录时返回 (nil, false)。
func CurrentUser(ctx context.Context) (*User, bool) {
	if ctx == nil {
		return nil, false
	}
	u, ok := ctx.Value(userKey).(*User)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}

// MustUser 取出当前登录用户，不存在则 panic。
//
// 只允许在「已经过鉴权中间件」的 handler 里调用 —— 那种位置拿不到用户
// 属于编程错误（路由漏挂中间件），应当尽早暴露而不是静默降级。
func MustUser(ctx context.Context) *User {
	u, ok := CurrentUser(ctx)
	if !ok {
		panic("identity: 当前上下文没有登录用户，请确认该路由已挂载鉴权中间件")
	}
	return u
}
