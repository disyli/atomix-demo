// Package api 提供 HTTP 接口。
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
		c.JSON(http.StatusOK, gin.H{
			"status": "ok", "mode": modeName(h.Agent.UseMock),
			"time": time.Now().UnixMilli(),
			"sha": h.Cfg.DeploySHA, // 部署版本标识（核对线上是否为目标提交）
			"guest": h.Cfg.GuestEnabled,
		})
	})

	api := r.Group("/api")
	// 注册/登录：每 IP 每分钟最多 10 次，防止暴力破解与批量注册
	api.POST("/auth/register", authRateLimiter(), h.register)
	api.POST("/auth/login", authRateLimiter(), h.login)
	// 游客接口：每 IP 每分钟最多 5 次，防止批量刷库与余额消耗
	api.POST("/auth/guest", guestRateLimiter(), h.guestLogin)

	authed := api.Group("", middleware.UserIdentity(h.Cfg.JWTSecret, h.Cfg.TicketSecret))
	authed.GET("/me", h.me)
	authed.GET("/projects", h.listProjects)
	authed.POST("/projects", h.createProject)
	authed.GET("/projects/:id", h.getProject)
	authed.GET("/projects/:id/events", h.getEvents)
	authed.GET("/projects/:id/messages", h.getProjectMessages)
	authed.GET("/projects/:id/snapshots", h.listSnapshots)
	authed.POST("/projects/:id/rollback", h.rollbackVersion)
	// generate 用 ticketOrBearer：EventSource 无法设 Authorization 头，需要 ticket 参数
	// 票据仅在此接口接受，无法用于写操作（authed 组已拒绝 ticket）
	authed.POST("/projects/:id/refine", h.refineSSE)
	authed.POST("/chat", h.chatIntent)
	authed.POST("/attachments", h.uploadAttachment)
	authed.GET("/attachments", h.listAttachments)
	authed.GET("/attachments/:id", h.getAttachment)
	authed.POST("/permissions/:reqId", h.resolvePermission)
	authed.POST("/runs/:runId/cancel", h.cancelRun)
	// ticket：为 preview/download/generate 等需要在 URL query 传凭据的场景签发短期令牌（60s），
	// 避免长期 token 写入 nginx 访问日志
	authed.POST("/ticket", h.issueTicket)

	// 票据验证路由（不在 authed 组，只接受 ticket 或 Bearer）
	tb := ticketOrBearer(h.Cfg.JWTSecret, h.Cfg.TicketSecret)
	r.GET("/api/generate", tb, h.generateSSE)
	r.GET("/api/projects/:id/preview", tb, h.previewHTML)
	r.GET("/api/projects/:id/source", tb, h.projectSource)
}

// guestLogin 一次性评审账号：每次调用创建独立 guest 用户（guest_时间戳@guest.atomix），
// 返回与注册同构的 token。评审无需注册个人账号或暴露 API Key；开关经
// ATOMIX_GUEST_ENABLED 控制（默认开）。
// 游客密码为随机串，登录接口也拒绝 guest_* 账号，杜绝旁观者凭邮箱猜密码登录他人账号。
func (h *Handlers) guestLogin(c *gin.Context) {
	if !h.Cfg.GuestEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "游客入口未开放"})
		return
	}
	email := fmt.Sprintf("guest_%d@guest.atomix", time.Now().UnixNano())
	// 随机密码：32 位 hex，只用于创建账号，不对外暴露，使得任何人都无法用密码接口登录游客账号
	rawPwd := randomHex(16)
	hash, err := auth.HashPassword(rawPwd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	u := &store.User{Email: email, PasswordHash: hash, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建游客失败"})
		return
	}
	token, _ := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Email)
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  gin.H{"id": u.ID, "email": u.Email, "guest": true},
	})
}

// listSnapshots 项目版本快照列表（不含 HTML 正文）：回滚面板数据源。
func (h *Handlers) listSnapshots(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	snaps, err := store.ListSnapshots(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取快照失败"})
		return
	}
	out := make([]gin.H, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, gin.H{
			"id": s.ID, "version": s.Version, "label": s.Label,
			"status": s.Status, "createdAt": s.CreatedAtMs,
			"current": s.Version == p.Version,
		})
	}
	c.JSON(http.StatusOK, gin.H{"current": p.Version, "snapshots": out})
}

// rollbackVersion 回滚到指定版本：事务内原子更新 HTML/LastGoodHTML/Version，
// 生成回滚快照；返回最新项目（前端据此原子刷新预览与源码）。
func (h *Handlers) rollbackVersion(c *gin.Context) {
	var req struct {
		Version int `json:"version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Version <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version 必须为正整数"})
		return
	}
	var own store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&own).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	// 同一项目同时只允许一次构建/回滚；构建进行中直接返回 409，不阻塞等待
	unlock, ok := h.Agent.TryLockProject(own.ID)
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "项目正在构建中，请等待完成后再回滚"})
		return
	}
	defer unlock()
	p, snap, err := store.RollbackSnapshot(own.ID, req.Version)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	store.DB.Create(&store.Event{ProjectID: own.ID, Stage: "done", Message: fmt.Sprintf("已回滚至 v%d（当前 v%d）", req.Version, p.Version), Level: "warn", TsMs: store.Now()})
	c.JSON(http.StatusOK, gin.H{"project": projectBrief(*p), "rollbackTo": req.Version, "snapshot": gin.H{"version": snap.Version, "label": snap.Label}})
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

// sanitizeFilename 清洗项目名为安全文件名：保留中文/字母/数字/连字符下划线，其余替换为 -，限长 60。
func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r > 0x4e00 && r < 0x9fff:
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = out[:60]
	}
	// 逐 rune 截断防中文截半
	rs := []rune(out)
	if len(rs) > 60 {
		out = string(rs[:60])
	}
	return out
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
	// 游客账号只能通过 /auth/guest 接口获取 token，密码接口明确拒绝
	if strings.HasSuffix(req.Email, "@guest.atomix") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
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
		"template": p.Template, "status": p.Status, "version": p.Version,
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

// getProjectMessages 返回同一 project 的完整对话历史（每轮 user/assistant 消息按时间序）。
func (h *Handlers) getProjectMessages(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	var ms []store.Message
	store.DB.Where("project_id = ?", p.ID).Order("id ASC").Find(&ms)
	if ms == nil {
		ms = []store.Message{}
	}
	c.JSON(http.StatusOK, ms)
}

// projectSource 返回当前项目生成应用的完整源码（HTML 文档）。
// 默认 application/json（前端代码面板用），download=1 时以附件形式下发原始 HTML，
// 文件名取项目名（去扩展、白名单字符、限长），Content-Disposition 安全头。
func (h *Handlers) projectSource(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	if p.HTML == "" {
		c.JSON(http.StatusOK, gin.H{"name": p.Name, "filename": "", "source": "", "size": 0, "lines": 0})
		return
	}
	filename := sanitizeFilename(p.Name)
	if filename == "" {
		filename = "atomix-app-" + strconv.FormatUint(uint64(p.ID), 10)
	}
	if c.Query("download") == "1" {
		c.Header("Content-Disposition", "attachment; filename=\""+filename+".html\"")
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(p.HTML))
		return
	}
	lines := strings.Count(p.HTML, "\n") + 1
	c.JSON(http.StatusOK, gin.H{
		"name": p.Name, "filename": filename + ".html",
		"source": p.HTML, "size": len(p.HTML), "lines": lines,
	})
}
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
// 对话消息持久化：用户消息与 chat/clarify 回复均落 Message 表（projectId 可为 0 表示暂无项目）。
func (h *Handlers) chatIntent(c *gin.Context) {
	var req struct {
		Message       string `json:"message"`
		AttachmentIDs []uint `json:"attachmentIds"`
		Mode          string `json:"mode"`
		ProjectID     uint   `json:"projectId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "消息不能为空"})
		return
	}
	uid := middleware.UID(c)
	// 归属校验：projectId 必须属于当前用户
	if req.ProjectID > 0 {
		var cnt int64
		store.DB.Model(&store.Project{}).Where("id = ? AND user_id = ?", req.ProjectID, uid).Count(&cnt)
		if cnt == 0 {
			req.ProjectID = 0
		}
	}
	r := h.Agent.ClassifyIntent(c.Request.Context(), uid, req.Message, req.AttachmentIDs)
	// 落库：用户消息 + 助手回复（chat/clarify 时）
	if req.ProjectID > 0 {
		store.DB.Create(&store.Message{ProjectID: req.ProjectID, UserID: uid, Role: "user", Kind: "text", Text: req.Message, CreatedAtMs: store.Now()})
	}
	if r.Intent == "chat" || r.Intent == "clarify" {
		if req.ProjectID > 0 {
			store.DB.Create(&store.Message{ProjectID: req.ProjectID, UserID: uid, Role: "assistant", Kind: "text", Text: r.Reply, CreatedAtMs: store.Now()})
		}
	}
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
	// 流开始即下发 runId：前端的停止按钮依赖它调用 /runs/:runId/cancel，
	// 只在结束时发送会让进行中的任务永远无法被真正取消
	send("runId", runID)

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
	// 流开始即下发 runId：前端的停止按钮依赖它调用 /runs/:runId/cancel，
	// 只在结束时发送会让进行中的任务永远无法被真正取消
	send("runId", runID)

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
		// 用户主动停止：stopped 终态事件（Message 落库由 Refine 内部统一处理）
		send("runId", runID)
		send("stopped", "已按用户要求停止构建")
		return
	}
	if err != nil {
		send("error", "修改失败: "+err.Error())
		return
	}
	// 修改成功：每轮对话消息（user + assistant run）已由 Refine 内部落库
	send("done", toJSON(gin.H{"project": projectBrief(*updated), "runId": runID}))
}

// issueTicket 为当前登录用户签发一个 60 秒有效的短期票据，
// 用于 preview/source-download 等需要在 URL query 传凭据的场景。
// 前端持有该 ticket 后在 URL 里用 ticket= 参数代替 t= token，
// 避免长期 JWT token 写入 nginx 访问日志。
func (h *Handlers) issueTicket(c *gin.Context) {
	uid := middleware.UID(c)
	email := c.GetString("email")
	ticket, err := auth.IssueTicket(h.Cfg.TicketSecret, uid, email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "签发票据失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": ticket, "ttl": 60})
}

// ticketOrBearer 双通道鉴权中间件：
//   - 有 ticket 查询参数时用票据密钥验证（短期，供 preview/download/generate URL 使用）
//     并校验 Use == "ticket"，防止长期 token 当票据用
//   - 否则回退到 Authorization Bearer token 验证（正常 API 调用路径）
func ticketOrBearer(jwtSecret, ticketSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if t := c.Query("ticket"); t != "" {
			claims, err := auth.ParseToken(ticketSecret, t)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "票据无效或已过期"})
				return
			}
			// 票据必须携带 use=ticket 标志，防止旧格式或伪造的长期 token 混入
			if claims.Use != "ticket" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "非法票据类型"})
				return
			}
			c.Set("uid", claims.UserID)
			c.Set("email", claims.Email)
			c.Next()
			return
		}
		// 无 ticket 时走 Bearer token（直接 API 调用路径）
		token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ParseToken(jwtSecret, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.Use == "ticket" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "ticket 不可作为 Bearer 使用"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("email", claims.Email)
		c.Next()
	}
}

// randomHex 返回 n 字节的随机十六进制字符串（2n 字符）。
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败极为罕见；兜底用时间戳
		return fmt.Sprintf("%x%x", time.Now().UnixNano(), time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// authRateLimiter 注册/登录速率限制：每 IP 每分钟最多 10 次，
// 防止暴力破解密码和批量注册。与 guestRateLimiter 逻辑相同，只改限额。
func authRateLimiter() gin.HandlerFunc {
	type entry struct {
		count    int
		windowMs int64
	}
	var mu sync.Mutex
	ipMap := map[string]*entry{}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().UnixMilli()
			mu.Lock()
			for ip, e := range ipMap {
				if now-e.windowMs >= int64(2*60*1000) {
					delete(ipMap, ip)
				}
			}
			mu.Unlock()
		}
	}()
	return func(c *gin.Context) {
		ip := c.GetHeader("X-Real-IP")
		if ip == "" {
			ip = c.ClientIP()
		}
		now := time.Now().UnixMilli()
		const (windowSize = int64(60 * 1000); limit = 10)
		mu.Lock()
		e, ok := ipMap[ip]
		if !ok || now-e.windowMs >= windowSize {
			ipMap[ip] = &entry{count: 1, windowMs: now}
			mu.Unlock()
			c.Next()
			return
		}
		e.count++
		over := e.count > limit
		mu.Unlock()
		if over {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}

// guestRateLimiter 游客注册速率限制：每 IP 每分钟最多 5 次，防止批量刷库与消耗 LLM 余额。
// 优先读 nginx 注入的 X-Real-IP（比 c.ClientIP() 更可靠，不受 Docker 网桥影响）；
// 内存滑动窗口，后台协程每 5 分钟清理过期条目，防内存无限增长。
func guestRateLimiter() gin.HandlerFunc {
	type entry struct {
		count    int
		windowMs int64
	}
	var mu sync.Mutex
	ipMap := map[string]*entry{}

	// 后台清理：每 5 分钟扫描并删除窗口早已过期的条目
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().UnixMilli()
			mu.Lock()
			for ip, e := range ipMap {
				if now-e.windowMs >= int64(2*60*1000) { // 超过 2 分钟未活跃则清理
					delete(ipMap, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		// 优先取 nginx 注入的 X-Real-IP，回退到 gin 解析的 ClientIP
		ip := c.GetHeader("X-Real-IP")
		if ip == "" {
			ip = c.ClientIP()
		}
		now := time.Now().UnixMilli()
		windowSize := int64(60 * 1000) // 1 分钟
		const limit = 5

		mu.Lock()
		e, ok := ipMap[ip]
		if !ok || now-e.windowMs >= windowSize {
			ipMap[ip] = &entry{count: 1, windowMs: now}
			mu.Unlock()
			c.Next()
			return
		}
		e.count++
		over := e.count > limit
		mu.Unlock()

		if over {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
			return
		}
		c.Next()
	}
}
