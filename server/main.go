// Atomix Demo 服务入口。
package main

import (
	"fmt"
	"log"
	"time"

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

	// 游客账号过期清理：游客是一次性评审账号，默认 24h 过期（ATOMIX_GUEST_TTL_HOURS，0=关闭），
	// 过期后连同项目/事件/消息/附件/快照级联删除；生成中的游客项目自动跳过。
	// 启动即执行一次，此后每小时巡检。
	if cfg.GuestTTLHours > 0 {
		go func() {
			run := func() {
				n, err := store.CleanupGuests(cfg.GuestTTLHours)
				switch {
				case err != nil:
					log.Printf("guest cleanup: %v", err)
				case n > 0:
					log.Printf("guest cleanup: 已清理 %d 个过期游客账号（TTL=%dh）", n, cfg.GuestTTLHours)
				}
			}
			run()
			for range time.Tick(time.Hour) {
				run()
			}
		}()
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