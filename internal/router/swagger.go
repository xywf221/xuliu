package router

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	// 导入生成出来的文档包：它通过 init() 把 swagger.json 注册到
	// gin-swagger 的全局容器里，本包不直接引用它的任何符号。
	_ "github.com/xywf221/xuliu/docs"
)

// registerSwagger 挂载 /swagger/index.html。
//
// 只在非生产环境挂载。接口文档把一个系统的全部能力、参数形态和错误码
// 摊开给人看，等价于一份完整的内部结构说明书 —— 它对开发期极有价值，
// 对公网暴露则只是信息泄露。需要线上也开的话，应当由网关做白名单，
// 而不是把默认值改成「开着」。
func registerSwagger(engine *gin.Engine, enabled bool) {
	if !enabled {
		return
	}
	engine.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}
