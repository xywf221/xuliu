package response

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"github.com/xywf221/xuliu/internal/pkg/apperr"
)

// init 让 gin 的校验器在报错时使用 json 标签名，而不是 Go 的字段名。
//
// 这样用户看到的会是 "productID 必填"，而不是 "ProductID 必填"。
// 放在 init 而不是 bootstrap 里，是为了保证单测直接构造路由时行为也一致 ——
// 全局配置一旦被漏掉，用户看到的就是内部字段名。
func init() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		// 我们统一用 camelCase 的 json 标签对外，优先取它。
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})
}

// Bind 解析并校验请求（body / query / uri）到 obj。
//
// 失败时已经把错误响应写出去了，调用方直接 return 即可：
//
//	if !response.Bind(c, &req) {
//	    return
//	}
func Bind(c *gin.Context, obj any) bool {
	if err := c.ShouldBind(obj); err != nil {
		Fail(c, bindError(err))
		return false
	}
	return true
}

// bindError 把绑定/校验错误翻译成带可读文案的 InvalidParam。
func bindError(err error) error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		if len(verrs) == 0 {
			return apperr.Wrap(apperr.InvalidParam, err)
		}
		// 只报第一个错误：一次性糊一屏校验信息对用户没帮助。
		return apperr.Wrapf(apperr.InvalidParam, err, "%s", describe(verrs[0]))
	}
	return apperr.Wrapf(apperr.InvalidParam, err, "请求格式不正确")
}

// describe 把单条校验失败翻译成中文文案。
func describe(fe validator.FieldError) string {
	field := fe.Field()
	switch fe.Tag() {
	case "required":
		return field + " 不能为空"
	case "email":
		return field + " 邮箱格式不正确"
	case "url":
		return field + " 必须是合法的链接"
	case "min":
		return fmt.Sprintf("%s 不能小于 %s", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s 不能大于 %s", field, fe.Param())
	case "len":
		return fmt.Sprintf("%s 长度必须为 %s", field, fe.Param())
	case "gte":
		return fmt.Sprintf("%s 不能小于 %s", field, fe.Param())
	case "lte":
		return fmt.Sprintf("%s 不能大于 %s", field, fe.Param())
	case "gt":
		return fmt.Sprintf("%s 必须大于 %s", field, fe.Param())
	case "lt":
		return fmt.Sprintf("%s 必须小于 %s", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s 的取值必须是以下之一：%s", field, fe.Param())
	case "datetime":
		return fmt.Sprintf("%s 的日期格式应为 %s", field, fe.Param())
	case "eqfield":
		return fmt.Sprintf("%s 必须与 %s 一致", field, fe.Param())
	case "nefield":
		return fmt.Sprintf("%s 不能与 %s 相同", field, fe.Param())
	case "alphanum":
		return field + " 只能包含字母和数字"
	default:
		return fmt.Sprintf("%s 校验未通过（%s）", field, fe.Tag())
	}
}
