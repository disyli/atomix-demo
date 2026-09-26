package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config 汇总服务运行所需的全部配置。
type Config struct {
	Port      int
	DataDir   string
	JWTSecret string
	// TicketSecret 用于签发短期预览票据（60s），与 JWTSecret 分离避免互相干扰
	TicketSecret  string
	DeepSeekKey   string
	DeepSeekURL   string
	DeepSeekModel string
	UseMock       bool
	// DeploySHA 部署版本标识（构建时经 -ldflags 注入，health 接口返回，评审可核对）
	DeploySHA string
	// GuestEnabled 游客入口开关（默认开启；评审无需注册个人账号即可体验）
	GuestEnabled bool
}

// BuiltinDeploySHA 构建期经 -ldflags 注入的部署标识（Dockerfile 传入 git short SHA）。
var BuiltinDeploySHA string

// DefaultJWTSecret dev 兜底密钥：仅未配置环境变量时使用，生产部署必须显式覆盖
// （main.go 会拒绝在 live 模式下使用该默认值启动）。
const DefaultJWTSecret = "atomix-demo-dev-secret-please-change"

func Load() (*Config, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir = filepath.Dir(dir)

	dataDir := getEnv("ATOMIX_DATA_DIR", filepath.Join(dir, "data"))
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	cfg := &Config{
		Port:          getEnvInt("ATOMIX_PORT", 51720),
		DataDir:       dataDir,
		JWTSecret:     getEnv("ATOMIX_JWT_SECRET", DefaultJWTSecret),
		DeepSeekKey:   os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekURL:   getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		DeepSeekModel: getEnv("DEEPSEEK_MODEL", "deepseek-v4-flash-vision-exp"),
		// 部署标识：优先运行时环境变量（deploy.sh 注入），否则用构建期 ldflags 值
		DeploySHA:    getEnv("ATOMIX_DEPLOY_SHA", firstNonEmpty(BuiltinDeploySHA, "dev-local")),
		GuestEnabled: getEnv("ATOMIX_GUEST_ENABLED", "1") == "1",
	}
	// 票据密钥：优先环境变量，未配置时从 JWTSecret 派生（+_ticket 后缀），保持零配置可用
	cfg.TicketSecret = getEnv("ATOMIX_TICKET_SECRET", cfg.JWTSecret+"_ticket")
	cfg.UseMock = cfg.DeepSeekKey == ""
	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
