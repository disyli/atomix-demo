// Atomix Demo 服务入口。
package main

import (
	"fmt"
	"log"

	"atomix-demo/server/internal/agent"
	"atomix-demo/server/internal/api"
	"atomix-demo/server/internal/config"
	"atomix-demo/server/internal/llm"
	"atomix-demo/server/internal/store"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	// 生产安全检查：接入真实 LLM（live 模式）时禁止使用默认 JWT 密钥，
	// 防止默认密钥泄露导致的令牌伪造。dev/demo 模式放行（无真实数据风险）。
	if !cfg.UseMock && cfg.JWTSecret == config.DefaultJWTSecret {
		log.Fatal("安全检查失败：live 模式检测到默认 JWT 密钥，请设置 ATOMIX_JWT_SECRET 环境变量（至少 32 位随机字符串）后重启")
	}
	if err := store.Open(cfg.DataDir); err != nil {
		log.Fatalf("open store: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	// 信任本机 nginx 反向代理（127.0.0.1）及 Docker 默认网桥（172.17.0.0/16）
	// 使 c.ClientIP() 能读取 nginx 注入的 X-Real-IP，得到真实客户端 IP
	if err := r.SetTrustedProxies([]string{"127.0.0.1", "::1", "172.16.0.0/12"}); err != nil {
		log.Printf("SetTrustedProxies: %v", err)
	}

	var llmSvc llm.Service
	if !cfg.UseMock {
		llmSvc = llm.NewDeepSeek(llm.Options{
			APIKey:  cfg.DeepSeekKey,
			BaseURL: cfg.DeepSeekURL,
			Model:   cfg.DeepSeekModel,
		})
	} else {
		llmSvc = llm.NewDeepSeek(llm.Options{APIKey: "unused"})
	}

	ag := agent.NewAgent(llmSvc, cfg.UseMock)
	h := &api.Handlers{Cfg: cfg, Agent: ag}
	api.Register(r, h)

	// 前端静态资源（构建后）
	r.NoRoute(func(c *gin.Context) {
		c.File("./static/index.html")
	})
	r.Static("/assets", "./static/assets")

	addr := fmt.Sprintf(":%d", cfg.Port)
	mode := "demo"
	if !cfg.UseMock {
		mode = "live(deepseek)"
	}
	log.Printf("Atomix Demo listening on %s [mode=%s]", addr, mode)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
