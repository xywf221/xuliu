// Command xuliu 是序流研发管理平台的入口。
//
// 启动流程：解析参数 → 载入配置 → 初始化日志 → 组装应用 → 监听并优雅退出。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xywf221/xuliu/internal/bootstrap"
	"github.com/xywf221/xuliu/internal/config"
	"github.com/xywf221/xuliu/internal/logging"
)

// version 由构建时通过 -ldflags 注入。
var version = "dev"

// 以下是 swaggo 的接口文档元信息，由 `go tool swag init` 解析。
//
//	swag init -g cmd/xuliu/main.go -o docs --parseDependency --parseInternal
//
// 文档只在非生产环境挂载（见 internal/router/swagger.go）。
//
// @title                       序流 API
// @version                     1.0
// @description                 让项目有序，让协作流动。需求、任务、迭代与缺陷一体化的研发管理平台。
// @description
// @description                 响应外壳：成功为 `{"data": …, "requestId": …}`，失败为 `{"code": …, "message": …, "requestId": …}`。
// @description                 判成功只看 HTTP 状态码是否为 2xx，不必再在成功体里判 code。
// @BasePath                    /api/v1
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 填 `Bearer <登录返回的 accessToken>`。
func main() {
	if err := run(); err != nil {
		// 此时日志器可能还没建起来，直接写 stderr 保证错误可见。
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", "configs/config.yaml", "配置文件路径（可为空，将仅使用默认值与环境变量）")
		showVersion = flag.Bool("version", false, "打印版本号后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("xuliu", version)
		return nil
	}

	// ---- 配置 ----
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	// ---- 日志 ----
	logger := logging.New(cfg.Log, cfg.App.Env)
	logger.Info("序流启动中",
		slog.String("version", version),
		slog.String("env", cfg.App.Env),
		slog.String("addr", cfg.Server.Addr),
		slog.String("driver", cfg.Database.Driver),
	)

	// 开发环境下临时生成的密钥会在重启后失效，必须显式告警 ——
	// 否则用户会遇到「重启后莫名其妙要重新登录」且不明原因。
	if cfg.Auth.SecretGenerated() {
		logger.Warn("JWT 密钥为本次启动临时生成，重启后所有登录状态将失效",
			slog.String("建议", "如需保持登录，请配置 auth.jwtSecret 或设置 XULIU_AUTH__JWTSECRET"))
	}

	// ---- 组装 ----
	// 用一个独立的根 context，便于将来在此挂载需要感知退出的后台任务。
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	app, err := bootstrap.New(rootCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Close(); err != nil {
			logger.Error("释放资源时出错", slog.Any("error", err))
		}
	}()

	// ---- HTTP 服务 ----
	// 用 http.Server 而不是 engine.Run()：后者不支持优雅退出，
	// 且无法配置各类超时（超时缺失会让慢连接耗尽服务端资源）。
	srv := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           app.Engine,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
		// Go 1.27 新增：限制单个请求头里值的数量，
		// 防御用大量重复头撑爆内存的请求。
		MaxHeaderValueCount: cfg.Server.MaxHeaderValueCount,
		// 不信任任何代理：此时 RemoteAddr 就是真实客户端地址。
		// 若部署在反向代理后，应在配置里显式列出代理地址。
	}

	// 监听放在 goroutine 里，主 goroutine 负责等待退出信号。
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已启动", slog.String("地址", cfg.Server.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// ---- 等待退出 ----
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务异常退出: %w", err)
	case sig := <-quit:
		logger.Info("收到退出信号，开始优雅关闭", slog.String("signal", sig.String()))
	}

	// 先停止接收新任务，再等在途请求处理完。
	started := time.Now()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("优雅关闭超时，仍有请求未完成", slog.Any("error", err))
		return err
	}

	logger.Info("服务已停止", slog.Duration("关闭耗时", time.Since(started)))
	return nil
}
