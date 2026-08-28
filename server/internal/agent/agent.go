package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"atomix-demo/server/internal/llm"
	"atomix-demo/server/internal/store"
)

// Agent 负责编排一次应用生成流水线。
type Agent struct {
	LLM     llm.Service
	UseMock bool
}

// PipelineEvents 流水线事件回调。
type PipelineEvents struct {
	OnStage  func(stage, message string)
	OnDetail func(stage, message, level string)
}

// Run 执行完整流水线：规划 -> 构建 -> 运行 -> 校验 -> 完成。
func (a *Agent) Run(ctx context.Context, userID uint, brief string, ev PipelineEvents) (*store.Project, error) {
	stage := func(stage, msg string) {
		if ev.OnStage != nil {
			ev.OnStage(stage, msg)
		}
	}
	detail := func(stage, msg, level string) {
		if ev.OnDetail != nil {
			ev.OnDetail(stage, msg, level)
		}
	}

	// ---- 规划阶段 ----
	stage("plan", "Agent 正在理解需求并制定构建计划…")
	plan := a.makePlan(ctx, brief)
	detail("plan", fmt.Sprintf("选择模板 %s：%s", plan.Template, plan.Reason), "info")
	for _, s := range plan.Steps {
		detail("plan", fmt.Sprintf("Step %d · %s", s.ID, s.Title), "info")
	}
	a.appendEvents(userID, plan, brief, "plan")

	// ---- 构建阶段 ----
	stage("build", "正在生成应用代码…")
	html := a.generateHTML(ctx, brief, plan)
	detail("build", fmt.Sprintf("已生成完整单文件应用（约 %d 字符）", len(html)), "info")
	a.appendEvents(userID, plan, brief, "build")

	// ---- 运行阶段（模拟部署预览环境）----
	stage("run", "正在启动预览环境…")
	time.Sleep(400 * time.Millisecond)
	detail("run", "预览沙箱已就绪（sandboxed iframe）", "info")

	// ---- 校验阶段 ----
	stage("verify", "正在校验产物…")
	lower := strings.ToLower(html)
	if !strings.Contains(lower, "<!doctype html") && !strings.Contains(lower, "<html") {
		detail("verify", "产物缺少 HTML 文档结构，已回退到模板兜底", "warn")
		_, html, _ = RenderTemplate(Match(brief), plan.AppName)
	} else {
		detail("verify", "HTML 结构完整，允许在沙箱中渲染", "info")
	}

	// ---- 持久化 ----
	now := store.Now()
	project := &store.Project{
		UserID:      userID,
		Name:        plan.AppName,
		Brief:       brief,
		Template:    plan.Template,
		HTML:        html,
		Status:      "ready",
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}
	if err := store.DB.Create(project).Error; err != nil {
		return nil, err
	}
	a.appendEvents(userID, plan, brief, "verify")
	a.appendEvents(userID, plan, brief, "done")
	stage("done", "构建完成，预览已就绪 🎉")
	return project, nil
}

func (a *Agent) makePlan(ctx context.Context, brief string) PlanResult {
	if a.UseMock {
		return mockPlan(brief)
	}
	prompt := fmt.Sprintf(`你是 Atomix 平台的首席架构师。用户想构建一个网页小应用，需求如下：
"""%s"""
请只输出一个 JSON 对象（不要 markdown 代码块），字段：
- appName: 应用名（中文，不超过 8 字）
- template: 从 todo/notes/kanban 中选择最贴合的一个
- reason: 选择该模板的一句话理由（中文）
- steps: 3-5 个构建步骤，每步含 id(数字)、title(中文短语)、kind(assets/build/scripts 之一)
- highlights: 2-3 个应用亮点（中文短语数组）`, brief)
	resp, err := a.LLM.ChatJSON(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是一个严谨的应用规划 Agent，只输出 JSON。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return fallbackPlan(brief)
	}
	var plan PlanResult
	if err := json.Unmarshal([]byte(resp), &plan); err != nil || plan.Template == "" {
		return fallbackPlan(brief)
	}
	tid := Match(brief)
	if _, ok := Get(plan.Template); ok {
		tid = plan.Template
	}
	plan.Template = tid
	if plan.AppName == "" {
		plan.AppName = DefaultName(tid)
	}
	fillSteps(&plan)
	return plan
}

func (a *Agent) generateHTML(ctx context.Context, brief string, plan PlanResult) string {
	if a.UseMock {
		_, html, _ := RenderTemplate(plan.Template, plan.AppName)
		return html
	}
	info, _ := Get(plan.Template)
	caps := strings.Join(append(append(append([]string{}, info.Assets...), info.Build...), info.Scripts...), "、")
	prompt := fmt.Sprintf(`为以下需求生成一个完整的单文件网页应用（一个完整的 <!DOCTYPE html> 文档）：
"""%s"""
应用名：%s
实现要求：使用 HTML + CSS + 原生 JavaScript（可用 Tailwind CDN），必须包含：%s。
硬性要求：
1. 数据使用 localStorage 持久化，刷新不丢失
2. UI 精致现代，中文界面，含交互反馈
3. 只输出 HTML 代码，不要任何解释`, brief, plan.AppName, caps)
	html, err := a.LLM.ChatHTML(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是一个资深前端工程师，只输出完整 HTML 代码。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		_, fallback, _ := RenderTemplate(plan.Template, plan.AppName)
		return fallback
	}
	return html
}

// appendEvents 将某阶段的标准事件写入数据库。
func (a *Agent) appendEvents(userID uint, plan PlanResult, brief, stage string) {
	events := StageEvents(stage, plan, brief, a.UseMock)
	if len(events) == 0 {
		return
	}
	pid := a.latestProjectID(userID)
	for _, e := range events {
		store.DB.Create(&store.Event{ProjectID: pid, Stage: e.Stage, Message: e.Message, Level: e.Level, TsMs: store.Now()})
	}
}

func (a *Agent) latestProjectID(userID uint) uint {
	var p store.Project
	if err := store.DB.Where("user_id = ?", userID).Order("id DESC").First(&p).Error; err != nil {
		return 0
	}
	return p.ID
}
