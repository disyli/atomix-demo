package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config 汇总服务运行所需的全部配置。
type Config struct {
	Port          int
	DataDir       string
	JWTSecret     string
	DeepSeekKey   string
	DeepSeekURL   string
	DeepSeekModel string
	UseMock       bool
}

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
		JWTSecret:     getEnv("ATOMIX_JWT_SECRET", "atomix-demo-dev-secret-please-change"),
		DeepSeekKey:   os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekURL:   getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		DeepSeekModel: getEnv("DEEPSEEK_MODEL", "deepseek-chat"),
	}
	cfg.UseMock = cfg.DeepSeekKey == ""
	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
