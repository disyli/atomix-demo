// Package api 提供 HTTP 接口。
package api

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	authed.POST("/projects/:id/refine", h.refineSSE)
	authed.POST("/chat", h.chatIntent)
	authed.POST("/attachments", h.uploadAttachment)
	authed.GET("/attachments", h.listAttachments)
	authed.GET("/attachments/:id", h.getAttachment)
	authed.POST("/permissions/:reqId", h.resolvePermission)
	authed.POST("/runs/:runId/cancel", h.cancelRun)
}

// resolvePermission 用户对权限确认卡片做出决定：allow / allow_session / reject。
func (h *Handlers) resolvePermission(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	switch req.Action {
	case "allow", "allow_session", "reject":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action 必须是 allow / allow_session / reject"})
		return
	}
	if h.Agent.PermRegistry == nil {
		h.Agent.PermRegistry = agent.NewPermRegistry()
	}
	if !h.Agent.PermRegistry.Resolve(c.Param("reqId"), req.Action) {
		c.JSON(http.StatusGone, gin.H{"error": "确认请求不存在或已处理（可能已超时）"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// cancelRun 停止按钮：取消一个运行中的构建/迭代任务（校验归属，只能停止自己的任务）。
func (h *Handlers) cancelRun(c *gin.Context) {
	if h.Agent.Runs.Cancel(middleware.UID(c), c.Param("runId")) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在或已结束"})
}

// uploadAttachment 保存用户上传的附件。图片转 dataURL（vision 识图），文本类存原文。
func (h *Handlers) uploadAttachment(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少文件"})
		return
	}
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件不能超过 10MB"})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取失败"})
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取失败"})
		return
	}
	mime := file.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	a := &store.Attachment{
		UserID: middleware.UID(c), Name: file.Filename, MimeType: mime,
		Size: file.Size, CreatedAtMs: store.Now(),
	}
	if strings.HasPrefix(mime, "image/") {
		a.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
	} else {
		a.Content = truncateText(string(raw), 100000)
	}
	if err := store.DB.Create(a).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": a.ID, "name": a.Name, "mimeType": a.MimeType,
		"size": a.Size, "isImage": a.DataURL != "", "createdAt": a.CreatedAtMs,
	})
}

func (h *Handlers) listAttachments(c *gin.Context) {
	var as []store.Attachment
	store.DB.Where("user_id = ?", middleware.UID(c)).Order("id DESC").Limit(50).Find(&as)
	out := make([]gin.H, 0, len(as))
	for _, a := range as {
		out = append(out, gin.H{
			"id": a.ID, "name": a.Name, "mimeType": a.MimeType,
			"size": a.Size, "isImage": a.DataURL != "", "createdAt": a.CreatedAtMs,
		})
	}
	c.JSON(http.StatusOK, out)
}

// getAttachment 返回单条附件全文（供构建上下文注入，文本类返回原文，图片返回 dataURL）。
func (h *Handlers) getAttachment(c *gin.Context) {
	var a store.Attachment
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&a).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "附件不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": a.ID, "name": a.Name, "mimeType": a.MimeType, "size": a.Size,
		"isImage": a.DataURL != "", "content": a.Content, "dataURL": a.DataURL,
	})
}

func modeName(useMock bool) string {
	if useMock {
		return "demo"
	}
	return "live"
}

// truncateText 截断过长文本（api 包内使用）。
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
// 沙箱 iframe 无 allow-same-origin 时产物访问 localStorage 会抛 SecurityError，
// 这里在 <head> 前注入存储垫片：探测失败则以内存存储降级并通知父页面。
func (h *Handlers) previewHTML(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.String(http.StatusNotFound, "project not found")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(agent.InjectStorageShim(p.HTML)))
}

// chatIntent 对用户消息做意图识别：chat 闲聊回复 / clarify 澄清 / build 构建。
// 前端据此决定展示聊天回复还是进入构建流程。可携带附件 ID（图片走多模态识图）。
func (h *Handlers) chatIntent(c *gin.Context) {
	var req struct {
		Message       string `json:"message"`
		AttachmentIDs []uint `json:"attachmentIds"`
		Mode          string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息不能为空"})
		return
	}
	h.Agent.CurrentUserID = middleware.UID(c)
	r := h.Agent.ClassifyIntent(c.Request.Context(), req.Message, req.AttachmentIDs)
	c.JSON(http.StatusOK, gin.H{"intent": r.Intent, "reply": r.Reply, "brief": r.Brief})
}

// generateSSE 以 SSE 流式推送一次完整生成流水线的进度。
func (h *Handlers) generateSSE(c *gin.Context) {
	uid := middleware.UID(c)
	brief := c.Query("brief")
	if brief == "" {
		c.String(http.StatusBadRequest, "brief required")
		return
	}
	mode := c.DefaultQuery("mode", "build")
	if mode != "build" && mode != "plan" && mode != "research" {
		mode = "build"
	}
	var attachIDs []uint
	for _, s := range strings.Split(c.Query("attachmentIds"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			attachIDs = append(attachIDs, uint(n))
		}
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

	runID, runCtx := h.Agent.Runs.Start(uid)
	defer h.Agent.Runs.Remove(runID)

	project, err := h.Agent.Run(runCtx, uid, brief, mode, attachIDs, agent.PipelineEvents{
		OnStage: func(stage, message string) {
			send("stage", stage+"\x1f"+message)
		},
		OnDetail: func(stage, message, level string) {
			send("detail", stage+"\x1f"+message+"\x1f"+level)
		},
		OnPermission: func(reqID, tool, detail string) {
			// detail 含多行 diff：base64 编码为单行载荷下发（前缀 b64:），
			// 规避 gin SSEvent 多行 data 编码缺陷与 \x1f 歧义；前端解码渲染
			send("permission", reqID+"\x1f"+tool+"\x1fb64:"+base64.StdEncoding.EncodeToString([]byte(detail)))
		},
	})
	if errors.Is(err, agent.ErrCanceled) {
		// 用户主动停止：发 runId 供前端定位 + stopped 终态事件（非 error）
		send("runId", runID)
		send("stopped", "已按用户要求停止构建")
		return
	}
	if err != nil {
		send("error", "生成失败: "+err.Error())
		return
	}
	// 重新加载完整事件历史
	var es []store.Event
	store.DB.Where("project_id = ?", project.ID).Order("id ASC").Find(&es)
	payload := gin.H{"project": projectBrief(*project), "runId": runID}
	c.SSEvent("done", toJSON(payload))
}

// refineSSE 以 SSE 流式推送一次迭代修改任务（ReAct 循环）。
func (h *Handlers) refineSSE(c *gin.Context) {
	uid := middleware.UID(c)
	var req struct {
		Instruction   string `json:"instruction"`
		AttachmentIDs []uint `json:"attachmentIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Instruction == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "修改指令不能为空"})
		return
	}
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), uid).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
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

	runID, runCtx := h.Agent.Runs.Start(uid)
	defer h.Agent.Runs.Remove(runID)

	updated, err := h.Agent.Refine(runCtx, uid, p.ID, req.Instruction, req.AttachmentIDs, agent.PipelineEvents{
		OnStage: func(stage, message string) {
			send("stage", stage+"\x1f"+message)
		},
		OnDetail: func(stage, message, level string) {
			send("detail", stage+"\x1f"+message+"\x1f"+level)
		},
		OnPermission: func(reqID, tool, detail string) {
			// 与 generateSSE 相同：diff 多行内容 base64 单行下发
			send("permission", reqID+"\x1f"+tool+"\x1fb64:"+base64.StdEncoding.EncodeToString([]byte(detail)))
		},
	})
	if errors.Is(err, agent.ErrCanceled) {
		send("runId", runID)
		send("stopped", "已按用户要求停止构建")
		return
	}
	if err != nil {
		send("error", "修改失败: "+err.Error())
		return
	}
	send("done", toJSON(gin.H{"project": projectBrief(*updated), "runId": runID}))
}
