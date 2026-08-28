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
	if err := store.Open(cfg.DataDir); err != nil {
		log.Fatalf("open store: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

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

	ag := &agent.Agent{LLM: llmSvc, UseMock: cfg.UseMock}
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
