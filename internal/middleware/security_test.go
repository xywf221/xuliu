package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCrossOriginRejectionAbortsChain 锁死一个曾经真实存在的漏洞:
//
// 跨站写请求被拒(403)后,业务 handler 仍然被执行了。
// 根因是用 prot.Handler(h) 包装 —— 标准库在拒绝时不调用 h,
// 但那只对 net/http 有意义;gin 的处理链由引擎的循环驱动,
// 中间件不调用 c.Next() 并不等于终止,只有 c.Abort() 才是。
//
// 后果不是「返回了错误的错误码」,而是「CSRF 防护完全失效且响应看起来正常」,
// 所以这里断言的是「业务逻辑没有执行」,而不只是状态码。
func TestCrossOriginRejectionAbortsChain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cop, err := CrossOriginProtection(nil)
	if err != nil {
		t.Fatalf("构造中间件失败: %v", err)
	}

	var ran bool
	engine := gin.New()
	engine.Use(cop)
	engine.POST("/api/v1/users", func(c *gin.Context) {
		ran = true
		c.JSON(http.StatusOK, gin.H{"created": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	req.Host = "app.example.com"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example.com")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("跨站写请求应被拒绝,实际状态码 %d", w.Code)
	}
	if ran {
		t.Fatal("跨站写请求被拒后业务 handler 仍然执行了 —— CSRF 防护失效")
	}
}

// TestTrustedOriginAllowed 验证白名单源(开发期 Vite)可正常写入。
func TestTrustedOriginAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cop, err := CrossOriginProtection([]string{"http://localhost:5173"})
	if err != nil {
		t.Fatalf("构造中间件失败: %v", err)
	}

	var ran bool
	engine := gin.New()
	engine.Use(cop)
	engine.POST("/api/v1/users", func(c *gin.Context) {
		ran = true
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	req.Host = "app.example.com"
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Origin", "http://localhost:5173")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if !ran {
		t.Fatal("白名单内的源应被放行")
	}
	if w.Code != http.StatusCreated {
		t.Errorf("期望 201,实际 %d", w.Code)
	}
}

// TestNonBrowserRequestAllowed 验证不带浏览器头的请求(curl、服务端调用)放行。
func TestNonBrowserRequestAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cop, err := CrossOriginProtection(nil)
	if err != nil {
		t.Fatalf("构造中间件失败: %v", err)
	}

	var ran bool
	engine := gin.New()
	engine.Use(cop)
	engine.POST("/api/v1/users", func(c *gin.Context) {
		ran = true
		c.Status(http.StatusCreated)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if !ran {
		t.Fatal("不带 Origin / Sec-Fetch-Site 的请求应被放行(非浏览器请求)")
	}
}

// TestWildcardOriginRejected 验证通配符会让防护静默失效,因此必须显式拒绝。
func TestWildcardOriginRejected(t *testing.T) {
	if _, err := CrossOriginProtection([]string{"*"}); err == nil {
		t.Fatal("CORS 通配符应被拒绝,否则跨站防护会静默失效")
	}
}
