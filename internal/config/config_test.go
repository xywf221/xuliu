package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xywf221/xuliu/internal/config"
)

// TestDefaultsAreRunnable 验证「一行配置都不给」也能起来。
// 这是 clone 下来就能跑的前提。
func TestDefaultsAreRunnable(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "not-exist.yaml"))
	if err != nil {
		t.Fatalf("缺省配置应可直接加载，实际报错: %v", err)
	}

	if cfg.Database.Driver != "sqlite" {
		t.Errorf("默认驱动应为 sqlite，实际 %q", cfg.Database.Driver)
	}
	if cfg.Database.DSN == "" {
		t.Error("默认 DSN 不应为空")
	}
	if !cfg.IsDev() {
		t.Errorf("默认环境应为 dev，实际 %q", cfg.App.Env)
	}
	// 开发环境应自动补齐 JWT 密钥，否则登录功能没法用。
	if cfg.Auth.JWTSecret == "" {
		t.Error("开发环境应自动生成 JWT 密钥")
	}
	if !cfg.Auth.SecretGenerated() {
		t.Error("应标记出密钥是临时生成的，以便启动时告警")
	}
}

// TestYAMLDurationAndLayering 验证 YAML 解析、时长字段解析，
// 以及「默认值 < YAML < 环境变量」的优先级。
func TestYAMLDurationAndLayering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
app:
  name: 测试平台
  env: prod
server:
  addr: ":9090"
  readTimeout: 7s
database:
  driver: postgres
  dsn: "host=127.0.0.1 user=xuliu dbname=xuliu sslmode=disable"
  autoMigrate: false
auth:
  jwtSecret: "a-real-production-secret"
  bcryptCost: 11
storage:
  backend: s3
  s3:
    bucket: xuliu-attachments
    accessKey: ak
    secretKey: sk
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}

	// 环境变量应当压过 YAML 里的同名项。
	t.Setenv("XULIU_SERVER__ADDR", ":7070")
	t.Setenv("XULIU_LOG__LEVEL", "debug")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}

	if cfg.App.Name != "测试平台" {
		t.Errorf("YAML 里的中文值应正确解析，实际 %q", cfg.App.Name)
	}
	if cfg.Server.ReadTimeout != 7*time.Second {
		t.Errorf("时长字段应解析为 7s，实际 %v", cfg.Server.ReadTimeout)
	}
	// YAML 覆盖了默认值...
	if cfg.Database.Driver != "postgres" {
		t.Errorf("驱动应被 YAML 覆盖为 postgres，实际 %q", cfg.Database.Driver)
	}
	// ...环境变量又覆盖了 YAML。
	if cfg.Server.Addr != ":7070" {
		t.Errorf("环境变量应覆盖 YAML 的 addr，实际 %q", cfg.Server.Addr)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("环境变量应覆盖日志级别，实际 %q", cfg.Log.Level)
	}
	// 没被覆盖的项应保留默认值。
	if cfg.Server.WriteTimeout != config.Default().Server.WriteTimeout {
		t.Error("未在 YAML 中出现的项应保留默认值")
	}
	if cfg.Database.Seed != config.Default().Database.Seed {
		t.Error("未在 YAML 中出现的布尔项应保留默认值")
	}
	// 生产环境强制 JSON 日志，便于采集端按字段过滤。
	if cfg.Log.Format != "json" {
		t.Errorf("生产环境应强制 json 日志格式，实际 %q", cfg.Log.Format)
	}
	if cfg.Auth.SecretGenerated() {
		t.Error("生产环境显式配置了密钥，不应标记为临时生成")
	}
}

// TestProdRejectsMissingOrPlaceholderSecret 验证生产环境不会带着
// 不安全/占位的密钥启动 —— 这类问题一旦上线就很难回头改。
func TestProdRejectsMissingOrPlaceholderSecret(t *testing.T) {
	cases := map[string]string{
		"完全没配密钥": `
app:
  env: prod
`,
		"沿用示例占位值": `
app:
  env: prod
auth:
  jwtSecret: change-me-in-production
`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("写测试配置失败: %v", err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatal("生产环境应拒绝不安全的 JWT 密钥，实际放行了")
			}
		})
	}
}

// TestInvalidValuesRejected 覆盖各枚举项的取值校验。
func TestInvalidValuesRejected(t *testing.T) {
	cases := map[string]string{
		"未知驱动":        "database:\n  driver: oracle\n  dsn: x\n",
		"空 DSN":       "database:\n  dsn: \"\"\n",
		"未知存储后端":      "storage:\n  backend: ftp\n",
		"s3 缺 bucket": "storage:\n  backend: s3\n  s3:\n    accessKey: a\n    secretKey: b\n",
		"bcrypt 代价过低": "auth:\n  bcryptCost: 4\n",
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("写测试配置失败: %v", err)
			}
			_, err := config.Load(path)
			if err == nil {
				t.Fatal("应拒绝非法配置，实际放行了")
			}
			// 报错信息要能指导用户改正，不能只说「配置无效」。
			if !strings.Contains(err.Error(), "config:") {
				t.Errorf("错误信息应带 config: 前缀便于定位，实际: %v", err)
			}
		})
	}
}
