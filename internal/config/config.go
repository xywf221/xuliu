// Package config 负责加载与校验运行配置。
//
// 优先级：内置默认值 < YAML 文件 < 环境变量。
// 三层叠加后仍然缺关键项（如生产环境的 JWT 密钥）就拒绝启动，
// 而不是带着一个不安全的默认值跑起来。
//
// 环境变量命名：前缀 XULIU_，用双下划线表达层级。
//
//	XULIU_DATABASE__DRIVER=postgres
//	XULIU_DATABASE__DSN="host=127.0.0.1 user=xuliu dbname=xuliu"
//	XULIU_LOG__LEVEL=debug
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	envprovider "github.com/knadh/koanf/providers/env"
	fileprovider "github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// EnvPrefix 是所有环境变量覆盖项的前缀。
const EnvPrefix = "XULIU_"

// devJWTSecretPlaceholder 是示例配置里的占位密钥。
// 生产环境带着它启动会被 finalize 拒绝。
const devJWTSecretPlaceholder = "change-me-in-production"

// Config 是完整的运行配置。
type Config struct {
	App      App      `koanf:"app"`
	Server   Server   `koanf:"server"`
	Database Database `koanf:"database"`
	Auth     Auth     `koanf:"auth"`
	Storage  Storage  `koanf:"storage"`
	Log      Log      `koanf:"log"`
}

// App 是应用级信息。
type App struct {
	Name string `koanf:"name"`
	// Env 取 dev / test / prod，影响日志格式与若干安全校验。
	Env string `koanf:"env"`
	// Bootstrap 决定首次启动时是否要创建超级管理员。
	Bootstrap Bootstrap `koanf:"bootstrap"`
}

// Bootstrap 描述初始化管理员账号。
//
// 仅在库里一个用户都没有时生效，所以重复启动是安全的。
type Bootstrap struct {
	Enabled  bool   `koanf:"enabled"`
	Account  string `koanf:"account"`
	Password string `koanf:"password"`
	Realname string `koanf:"realname"`
	Email    string `koanf:"email"`
}

// Server 是 HTTP 服务参数。
type Server struct {
	Addr                string        `koanf:"addr"`
	ReadTimeout         time.Duration `koanf:"readTimeout"`
	ReadHeaderTimeout   time.Duration `koanf:"readHeaderTimeout"`
	WriteTimeout        time.Duration `koanf:"writeTimeout"`
	IdleTimeout         time.Duration `koanf:"idleTimeout"`
	ShutdownTimeout     time.Duration `koanf:"shutdownTimeout"`
	MaxHeaderBytes      int           `koanf:"maxHeaderBytes"`
	MaxHeaderValueCount int           `koanf:"maxHeaderValueCount"`
	// CORSOrigins 为空表示不启用 CORS。生产环境应与前端同源部署，留空。
	CORSOrigins []string `koanf:"corsOrigins"`
	// TrustedProxies 为空表示不信任任何代理，此时 RemoteAddr 即为客户端地址。
	TrustedProxies []string `koanf:"trustedProxies"`
}

// Database 是数据库连接参数。
type Database struct {
	// Driver 取 sqlite / mysql / postgres。换库只改这一项与 DSN。
	Driver string `koanf:"driver"`
	DSN    string `koanf:"dsn"`

	MaxOpenConns    int           `koanf:"maxOpenConns"`
	MaxIdleConns    int           `koanf:"maxIdleConns"`
	ConnMaxLifetime time.Duration `koanf:"connMaxLifetime"`
	ConnMaxIdleTime time.Duration `koanf:"connMaxIdleTime"`

	// LogLevel 取 silent / error / warn / info。
	LogLevel string `koanf:"logLevel"`
	// AutoMigrate 是否在启动时自动建表/加字段。
	AutoMigrate bool `koanf:"autoMigrate"`
	// Seed 是否写入权限点字典等基础数据。
	Seed bool `koanf:"seed"`
}

// Auth 是认证与口令参数。
type Auth struct {
	JWTSecret  string        `koanf:"jwtSecret"`
	Issuer     string        `koanf:"issuer"`
	AccessTTL  time.Duration `koanf:"accessTTL"`
	RefreshTTL time.Duration `koanf:"refreshTTL"`
	// BcryptCost 取 10..31。12 在安全与登录耗时之间比较平衡。
	BcryptCost int `koanf:"bcryptCost"`
	// MaxLoginFailures 连续失败多少次后锁定账号，0 表示不锁。
	MaxLoginFailures int           `koanf:"maxLoginFailures"`
	LockDuration     time.Duration `koanf:"lockDuration"`

	// 是否在本次启动中临时生成了 JWT 密钥（仅 dev 允许）。
	secretGenerated bool
}

// SecretGenerated 报告 JWT 密钥是否是本次启动临时生成的。
// 是的话意味着重启后旧 token 全部失效，启动日志会就此告警。
func (a Auth) SecretGenerated() bool { return a.secretGenerated }

// Storage 是附件存储参数。
type Storage struct {
	// Backend 取 local / s3。
	Backend string `koanf:"backend"`
	Local   Local  `koanf:"local"`
	S3      S3     `koanf:"s3"`
}

// Local 是本地磁盘存储参数。
type Local struct {
	Dir string `koanf:"dir"`
	// MaxSize 单文件大小上限，单位字节。
	MaxSize int64 `koanf:"maxSize"`
	// AllowedExtensions 为空表示不限制扩展名。匹配时不区分大小写，需带点。
	AllowedExtensions []string `koanf:"allowedExtensions"`
}

// S3 是 S3 兼容对象存储参数。
type S3 struct {
	Endpoint  string `koanf:"endpoint"`
	Region    string `koanf:"region"`
	Bucket    string `koanf:"bucket"`
	AccessKey string `koanf:"accessKey"`
	SecretKey string `koanf:"secretKey"`
	UseSSL    bool   `koanf:"useSsl"`
	// PublicBaseURL 若非空，用于拼出对外可访问的附件地址（如 CDN 域名）。
	PublicBaseURL string `koanf:"publicBaseUrl"`
}

// Log 是日志参数。
type Log struct {
	// Level 取 debug / info / warn / error。
	Level string `koanf:"level"`
	// Format 取 text / json。生产环境建议 json 便于采集。
	Format string `koanf:"format"`
	// AddSource 是否在日志里带上源码位置。
	AddSource bool `koanf:"addSource"`
}

// Default 返回内置默认值：一套面向 SQLite 的基础配置。
//
// 注意它本身不是可直接运行的完整配置：这只是「默认层」，
// JWT 密钥、日志格式这些派生项要经 finalize 才会补齐，
// 例如 Default().Auth.JWTSecret 是空的。拿到可用配置的正确入口是 Load。
//
// 保留这个函数是为了让测试能在不读磁盘的前提下拿到一份基础配置，
// 并供 Load 叠加 YAML / 环境变量。
func Default() Config {
	return Config{
		App: App{
			Name: "序流",
			Env:  "dev",
			Bootstrap: Bootstrap{
				Enabled:  true,
				Account:  "admin",
				Password: "admin123456",
				Realname: "系统管理员",
			},
		},
		Server: Server{
			Addr:                ":8080",
			ReadTimeout:         15 * time.Second,
			ReadHeaderTimeout:   10 * time.Second,
			WriteTimeout:        60 * time.Second,
			IdleTimeout:         120 * time.Second,
			ShutdownTimeout:     15 * time.Second,
			MaxHeaderBytes:      1 << 20, // 1 MiB
			MaxHeaderValueCount: 100,
		},
		Database: Database{
			Driver:          "sqlite",
			DSN:             "data/xuliu.db",
			MaxOpenConns:    25,
			MaxIdleConns:    5,
			ConnMaxLifetime: time.Hour,
			ConnMaxIdleTime: 10 * time.Minute,
			LogLevel:        "warn",
			AutoMigrate:     true,
			Seed:            true,
		},
		Auth: Auth{
			Issuer:           "xuliu",
			AccessTTL:        2 * time.Hour,
			RefreshTTL:       7 * 24 * time.Hour,
			BcryptCost:       12,
			MaxLoginFailures: 10,
			LockDuration:     15 * time.Minute,
		},
		Storage: Storage{
			Backend: "local",
			Local: Local{
				Dir:     "data/uploads",
				MaxSize: 64 << 20, // 64 MiB
				AllowedExtensions: []string{
					".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg",
					".txt", ".md", ".log", ".csv",
					".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
					".zip", ".tar", ".gz", ".7z", ".rar",
					".json", ".xml", ".yaml", ".yml", ".sql",
				},
			},
			S3: S3{Region: "us-east-1", UseSSL: true},
		},
		Log: Log{Level: "info", Format: "text"},
	}
}

// Load 读取配置文件与环境变量，叠加到默认值之上并完成校验。
//
// path 指向的文件不存在不算错误 —— 只靠默认值与环境变量也能启动。
func Load(path string) (*Config, error) {
	cfg := Default()

	k := koanf.New(".")

	if path != "" {
		switch _, err := os.Stat(path); {
		case err == nil:
			if err := k.Load(fileprovider.Provider(path), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("config: 解析配置文件 %s 失败: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// 允许缺失，继续。
		default:
			return nil, fmt.Errorf("config: 读取配置文件 %s 失败: %w", path, err)
		}
	}

	// XULIU_DATABASE__DRIVER -> database.driver
	//
	// 注意 delim 传 "."：koanf 的 delim 是「回调产出的键里所使用的分隔符」，
	// 它会拿这个字符对结果做 Unflatten。回调已把 __ 换成 .，所以这里必须传 .。
	// 若误传 "__"，回调产出的 server.addr 会作为**字面量键**留下，与 YAML 的
	// 嵌套键 server.addr 撞车，导致环境变量覆盖失效。
	if err := k.Load(envprovider.Provider(EnvPrefix, ".", func(s string) string {
		trimmed := strings.TrimPrefix(s, EnvPrefix)
		return strings.ReplaceAll(strings.ToLower(trimmed), "__", ".")
	}), nil); err != nil {
		return nil, fmt.Errorf("config: 读取环境变量失败: %w", err)
	}

	// koanf 默认的解码钩子已包含 StringToTimeDurationHookFunc，
	// 所以 YAML 里可以直接写 "15s"。
	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("config: 解析配置失败: %w", err)
	}

	if err := cfg.finalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// finalize 补齐可推导的配置项并做合法性校验。
func (c *Config) finalize() error {
	c.App.Env = strings.ToLower(strings.TrimSpace(c.App.Env))
	c.Database.Driver = strings.ToLower(strings.TrimSpace(c.Database.Driver))
	c.Storage.Backend = strings.ToLower(strings.TrimSpace(c.Storage.Backend))
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))

	switch c.Database.Driver {
	case "sqlite", "mysql", "postgres":
	case "":
		c.Database.Driver = "sqlite"
	default:
		return fmt.Errorf("config: 不支持的 database.driver %q（可选 sqlite / mysql / postgres）", c.Database.Driver)
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return errors.New("config: database.dsn 不能为空")
	}

	switch c.Storage.Backend {
	case "local", "s3":
	case "":
		c.Storage.Backend = "local"
	default:
		return fmt.Errorf("config: 不支持的 storage.backend %q（可选 local / s3）", c.Storage.Backend)
	}
	if c.Storage.Backend == "local" && c.Storage.Local.Dir == "" {
		return errors.New("config: storage.backend=local 时 storage.local.dir 不能为空")
	}
	if c.Storage.Backend == "s3" {
		if c.Storage.S3.Bucket == "" {
			return errors.New("config: storage.backend=s3 时 storage.s3.bucket 不能为空")
		}
		if c.Storage.S3.AccessKey == "" || c.Storage.S3.SecretKey == "" {
			return errors.New("config: storage.backend=s3 时 accessKey 与 secretKey 都不能为空")
		}
	}

	if c.Auth.BcryptCost == 0 {
		c.Auth.BcryptCost = 12
	}
	if c.Auth.BcryptCost < 10 || c.Auth.BcryptCost > 31 {
		return fmt.Errorf("config: auth.bcryptCost 应在 10..31 之间，当前 %d", c.Auth.BcryptCost)
	}
	if c.Auth.AccessTTL <= 0 {
		c.Auth.AccessTTL = 2 * time.Hour
	}
	if c.Auth.RefreshTTL <= 0 {
		c.Auth.RefreshTTL = 7 * 24 * time.Hour
	}

	if err := c.finalizeJWTSecret(); err != nil {
		return err
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.IsProd() && c.Log.Format != "json" {
		// 生产环境强制结构化日志，否则采集端没法按字段过滤。
		c.Log.Format = "json"
	}

	return nil
}

// finalizeJWTSecret 处理 JWT 密钥：
//
//	生产环境 —— 必须显式配置，且不能是示例占位值；
//	开发环境 —— 缺省时临时生成一个，让 clone 下来就能跑，但要告警。
func (c *Config) finalizeJWTSecret() error {
	secret := strings.TrimSpace(c.Auth.JWTSecret)

	if c.IsProd() {
		if secret == "" {
			return errors.New("config: 生产环境必须设置 auth.jwtSecret（可用 XULIU_AUTH__JWTSECRET 注入）")
		}
		if secret == devJWTSecretPlaceholder {
			return errors.New("config: 生产环境的 auth.jwtSecret 不能沿用示例占位值，请更换")
		}
		return nil
	}

	if secret == "" || secret == devJWTSecretPlaceholder {
		// crypto/rand.Text 是 Go 1.27 新增的，直接给出 128 位以上熵的
		// RFC 4648 base32 文本，不用自己拼 base64。
		c.Auth.JWTSecret = rand.Text()
		c.Auth.secretGenerated = true
	}
	return nil
}

// IsDev 报告是否运行在开发/测试环境。
func (c *Config) IsDev() bool { return c.App.Env == "dev" || c.App.Env == "test" }

// IsProd 报告是否运行在生产环境。
func (c *Config) IsProd() bool { return c.App.Env == "prod" }
