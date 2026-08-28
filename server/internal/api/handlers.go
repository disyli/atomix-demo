// Package api 提供 HTTP 接口。
package api

import (
	"net/http"
	"time"

	"atomix-demo/server/internal/agent"
	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/config"
	"atomix-demo/server/internal/middleware"
	"atomix-demo/server/internal/store"

	"github.com/gin-gonic/gin"
)

// Handlers 集中所有路由处理器。
type Handlers struct {
	Cfg   *config.Config
	Agent *agent.Agent
}

// Register 注册全部路由。
func Register(r *gin.Engine, h *Handlers) {
	r.Use(cors())
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "mode": modeName(h.Agent.UseMock), "time": time.Now().UnixMilli()})
	})

	api := r.Group("/api")
	api.POST("/auth/register", h.register)
	api.POST("/auth/login", h.login)

	authed := api.Group("", middleware.UserIdentity(h.Cfg.JWTSecret))
	authed.GET("/me", h.me)
	authed.GET("/projects", h.listProjects)
	authed.POST("/projects", h.createProject)
	authed.GET("/projects/:id", h.getProject)
	authed.GET("/projects/:id/events", h.getEvents)
	authed.GET("/projects/:id/preview", h.previewHTML)
	authed.GET("/generate", h.generateSSE)
}

func modeName(useMock bool) string {
	if useMock {
		return "demo"
	}
	return "live"
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

type credReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handlers) register(c *gin.Context) {
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" || len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱不能为空，密码至少 6 位"})
		return
	}
	var cnt int64
	store.DB.Model(&store.User{}).Where("email = ?", req.Email).Count(&cnt)
	if cnt > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已注册"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	u := &store.User{Email: req.Email, PasswordHash: hash, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败"})
		return
	}
	token, _ := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Email)
	c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": u.ID, "email": u.Email}})
}

func (h *Handlers) login(c *gin.Context) {
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var u store.User
	if err := store.DB.Where("email = ?", req.Email).First(&u).Error; err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	token, _ := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Email)
	c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": u.ID, "email": u.Email}})
}

func (h *Handlers) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"id": middleware.UID(c), "email": c.GetString("email")})
}

func (h *Handlers) listProjects(c *gin.Context) {
	var ps []store.Project
	store.DB.Where("user_id = ?", middleware.UID(c)).Order("id DESC").Find(&ps)
	out := make([]gin.H, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectBrief(p))
	}
	c.JSON(http.StatusOK, out)
}

func projectBrief(p store.Project) gin.H {
	return gin.H{
		"id": p.ID, "userId": p.UserID, "name": p.Name, "brief": p.Brief,
		"template": p.Template, "status": p.Status,
		"createdAt": p.CreatedAtMs, "updatedAt": p.UpdatedAtMs,
	}
}

func (h *Handlers) createProject(c *gin.Context) {
	var req struct {
		Brief string `json:"brief"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Brief == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需求描述不能为空"})
		return
	}
	p := &store.Project{UserID: middleware.UID(c), Brief: req.Brief, Status: "draft", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, projectBrief(*p))
}

func (h *Handlers) getProject(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	c.JSON(http.StatusOK, projectBrief(p))
}

func (h *Handlers) getEvents(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	var es []store.Event
	store.DB.Where("project_id = ?", p.ID).Order("id ASC").Find(&es)
	if es == nil {
		es = []store.Event{}
	}
	c.JSON(http.StatusOK, es)
}

// previewHTML 以独立文档形式返回生成的应用（供 iframe srcdoc/src 使用）。
func (h *Handlers) previewHTML(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.String(http.StatusNotFound, "project not found")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(p.HTML))
}

// generateSSE 以 SSE 流式推送一次完整生成流水线的进度。
func (h *Handlers) generateSSE(c *gin.Context) {
	uid := middleware.UID(c)
	brief := c.Query("brief")
	if brief == "" {
		c.String(http.StatusBadRequest, "brief required")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}

	send := func(event, data string) {
		c.SSEvent(event, data)
		flusher.Flush()
	}

	project, err := h.Agent.Run(c.Request.Context(), uid, brief, agent.PipelineEvents{
		OnStage: func(stage, message string) {
			send("stage", stage+"\x1f"+message)
		},
		OnDetail: func(stage, message, level string) {
			send("detail", stage+"\x1f"+message+"\x1f"+level)
		},
	})
	if err != nil {
		send("error", "生成失败: "+err.Error())
		return
	}
	// 重新加载完整事件历史
	var es []store.Event
	store.DB.Where("project_id = ?", project.ID).Order("id ASC").Find(&es)
	payload := gin.H{"project": projectBrief(*project)}
	c.SSEvent("done", toJSON(payload))
}
