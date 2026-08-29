package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
)

// Agent 负责编排一次应用生成任务。
type Agent struct {
	LLM           llm.Service
	UseMock       bool
	CurrentUserID uint // 最近一次请求的用户 ID，用于附件归属校验
}

// PipelineEvents 流水线事件回调。
type PipelineEvents struct {
	OnStage  func(stage, message string)
	OnDetail func(stage, message, level string)
}

// makePlan 旧版独立规划（保留给规划回退与单测使用；主流程由 ReAct 循环内的 plan_app 工具承担）。
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

// generateHTMLWith 调用 LLM 生成单文件应用。refineTo 非空时为迭代修改：附上当前产物全文做最小化修改。
func (a *Agent) generateHTMLWith(ctx context.Context, brief string, plan PlanResult, currentHTML, refineTo string) string {
	if a.UseMock {
		_, html, _ := RenderTemplate(plan.Template, plan.AppName)
		return html
	}
	info, _ := Get(plan.Template)
	caps := strings.Join(append(append(append([]string{}, info.Assets...), info.Build...), info.Scripts...), "、")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`为以下需求生成一个完整的单文件网页应用（一个完整的 <!DOCTYPE html> 文档）：
"""%s"""
应用名：%s
实现要求：使用 HTML + CSS + 原生 JavaScript（可用 Tailwind CDN），必须包含：%s。
硬性要求：
1. 数据使用 localStorage 持久化，刷新不丢失
2. UI 精致现代，中文界面，含交互反馈
3. 只输出 HTML 代码，不要任何解释
4. 禁止使用 document.cookie（沙箱环境不支持）`, brief, plan.AppName, caps))
	if refineTo != "" && currentHTML != "" {
		sb.WriteString("\n\n这是对已有应用的迭代修改请求：" + refineTo)
		sb.WriteString("\n\n当前应用完整代码如下，请在其基础上最小化修改后输出完整新文档：\n```html\n" + currentHTML + "\n```")
	}
	html, err := a.LLM.ChatHTML(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是一个资深前端工程师，只输出完整 HTML 代码。"},
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		_, fallback, _ := RenderTemplate(plan.Template, plan.AppName)
		return fallback
	}
	return html
}

// generateHTML 兼容旧调用（无迭代上下文）。
func (a *Agent) generateHTML(ctx context.Context, brief string, plan PlanResult) string {
	return a.generateHTMLWith(ctx, brief, plan, "", "")
}
