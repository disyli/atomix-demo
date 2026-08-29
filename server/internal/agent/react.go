package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
	"atomix-demo/server/internal/store"
)

const (
	maxReActLoops    = 12 // 单次任务的工具调用轮数上限，防失控
	maxAutoRepairs   = 3  // 校验失败后的自动修复次数上限
	reactMaxTokens   = 8192
	reactTemperature = 0.4
)

// reactSession 一次 ReAct 任务会话：持有状态、推送事件、驱动 think→act→observe 循环。
type reactSession struct {
	a          *Agent
	ctx        context.Context
	brief      string
	html       string
	plan       PlanResult
	summary    string
	refineNote string // 非空表示迭代修改模式，值为修改指令
	ev         PipelineEvents
}

func (rt *reactSession) stage(stage, msg string) {
	if rt.ev.OnStage != nil {
		rt.ev.OnStage(stage, msg)
	}
}

func (rt *reactSession) detail(stage, msg, level string) {
	if rt.ev.OnDetail != nil {
		rt.ev.OnDetail(stage, msg, level)
	}
}

// reactPrompt 构建 ReAct 系统提示词。refineTo 非空表示迭代修改模式。
func reactPrompt(brief, refineTo string) string {
	var sb strings.Builder
	sb.WriteString(`你是 Atomix 平台的构建 Agent，通过「思考 → 调用工具 → 观察结果」的循环完成应用构建。

可用工具：
- plan_app：规划应用（应用名/模板/步骤），必须最先调用
- write_file：写入完整单文件 HTML 应用（整个任务只允许成功写入一次）
- read_file：读取当前产物（迭代修改时先读后改）
- run_checks：对产物执行静态校验（写入后必须调用）
- finish：校验通过后收尾

硬性规则：
1. 产物是一个完整 <!DOCTYPE html> 文档：HTML + CSS + 原生 JS（可用 Tailwind CDN），中文界面，UI 精致现代
2. 数据用 localStorage 持久化（沙箱内不可用时平台会自动降级，无需担心）
3. 禁止使用 document.cookie（沙箱环境会抛 SecurityError）
4. 必须包含真实交互（事件绑定、DOM 更新），不能是静态页面
5. run_checks 返回 issues 时必须修复并重新 write_file，直到校验通过才能 finish
6. 每轮先想清楚再行动，不要空转`)
	if refineTo != "" {
		sb.WriteString("\n\n本次任务是在已有应用上做迭代修改。当前产物会通过 read_file 提供，按要求最小化修改后重新 write_file 完整文档。")
	} else {
		sb.WriteString("\n\n用户需求：" + brief)
	}
	return sb.String()
}

// reactLoop 驱动一次完整的 ReAct 循环，返回最终产物 HTML。
// live 模式：真实 DeepSeek function calling 多轮循环；demo 模式：脚本化模拟轨迹（工具全真执行）。
func (rt *reactSession) reactLoop(ctx context.Context) error {
	if rt.a.UseMock {
		return rt.demoRun()
	}
	return rt.liveRun(ctx)
}

// liveRun 真实 LLM 的 ReAct 循环。
func (rt *reactSession) liveRun(ctx context.Context) error {
	messages := []llm.ChatMessage{
		{Role: "system", Content: reactPrompt(rt.brief, rt.refineTo())},
	}
	if rt.refineTo() != "" {
		messages = append(messages, llm.ChatMessage{Role: "user", Content: rt.brief})
	}

	tools := toolDefs()
	loops, repairs := 0, 0
	for {
		loops++
		if loops > maxReActLoops {
			if rt.html == "" {
				return fmt.Errorf("ReAct 循环超过 %d 轮仍未产出可用产物", maxReActLoops)
			}
			rt.detail("done", "达到轮数上限，以当前最优产物收尾", "warn")
			break
		}

		resp, err := rt.a.LLM.ChatWithTools(ctx, messages, tools, reactTemperature, reactMaxTokens)
		if err != nil {
			if rt.html != "" {
				rt.detail("build", "模型调用失败，以已有产物收尾："+err.Error(), "warn")
				break
			}
			return fmt.Errorf("模型调用失败: %w", err)
		}

		if resp.Content != "" {
			rt.detail("build", truncateText(resp.Content, 160), "info")
		}

		// 模型直接给出最终回答（未调用工具）
		if len(resp.ToolCalls) == 0 {
			if rt.html != "" {
				break
			}
			// 循环还没产出任何东西就结束回答：强制拉回工具循环
			messages = append(messages, llm.ChatMessage{Role: "assistant", Content: resp.Content})
			messages = append(messages, llm.ChatMessage{Role: "user", Content: "请立即调用工具完成任务：先用 plan_app 规划，再用 write_file 写入完整 HTML。"})
			continue
		}

		// 逐个执行工具调用，观察结果回喂
		messages = append(messages, llm.ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		shouldStop := false
		for _, tc := range resp.ToolCalls {
			result := rt.runTool(tc.Function.Name, tc.Function.Arguments)

			// 行动事件（前端展示）
			think := result.Think
			if think == "" {
				think = toolActionText(tc.Function.Name, tc.Function.Arguments)
			}
			rt.detail("act", fmt.Sprintf("调用 %s → %s", tc.Function.Name, think), "info")

			// 思考事件
			if resp.Content != "" {
				rt.detail("think", truncateText(resp.Content, 200), "info")
			}

			if tc.Function.Name == "run_checks" && !result.OK {
				repairs++
				if repairs > maxAutoRepairs {
					if rt.html != "" {
						rt.detail("verify", "自动修复次数达上限，以当前产物收尾", "warn")
						shouldStop = true
					}
				}
			}
			if tc.Function.Name == "finish" && result.OK {
				shouldStop = true
			}

			messages = append(messages, llm.ChatMessage{
				Role:       "tool",
				Content:    result.Observe,
				ToolCallID: tc.ID,
			})
		}
		if shouldStop {
			break
		}
	}
	return nil
}

// refineTo 是否处于迭代修改模式（非空即修改指令）。
func (rt *reactSession) refineTo() string { return rt.refineNote }

// demoRun 演示模式的脚本化 ReAct 轨迹：与真实循环同构（plan → write → checks(失败) → 修复重写 → checks(通过) → finish），
// 工具全部真实执行，让评审者无 Key 也能观察到完整的 think→act→observe 闭环。
func (rt *reactSession) demoRun() error {
	rt.stage("plan", "Agent 正在理解需求并制定构建计划…")

	rt.detail("think", "分析需求关键词，匹配内置模板能力清单，确定应用形态与构建步骤", "info")
	tid := Match(rt.brief)
	planRes := rt.runTool("plan_app", mustJSON(planArgs{
		AppName:  DefaultName(tid),
		Template: tid,
		Reason:   "演示模式：按关键词规则匹配最贴合的内置模板",
		Steps:    []string{"搭建布局骨架", "实现数据模型与持久化", "绑定交互事件", "自检产物"},
	}))
	rt.observe("plan_app", planRes)

	rt.detail("think", "按计划生成完整单文件应用：布局 + 数据层 + 交互层 + 持久化", "info")
	_, html, _ := RenderTemplate(tid, DefaultName(tid))
	// 演示自修复：首版注入一个会被真实校验器抓住的问题（document.cookie）
	broken := strings.Replace(html, "<head>", "<head>\n<script>document.cookie='demo=1';</script>", 1)
	writeRes := rt.runTool("write_file", mustJSON(writeArgs{Path: "index.html", Content: broken}))
	rt.observe("write_file", writeRes)

	rt.detail("think", "产物已写入，执行静态校验确认文档结构、沙箱兼容性与交互完整性", "info")
	checkRes := rt.runTool("run_checks", "{}")
	rt.observe("run_checks", checkRes)

	if !checkRes.OK {
		rt.detail("think", "校验发现沙箱兼容性问题：document.cookie 在 opaque origin 下抛 SecurityError。定位后移除该调用，重新写入", "warn")
		fixed := rt.html
		// 真实修复：移除注入的问题代码
		fixed = strings.Replace(fixed, "<script>document.cookie='demo=1';</script>\n", "", 1)
		fixed = strings.Replace(fixed, "<script>document.cookie='demo=1';</script>", "", 1)
		rewriteRes := rt.runTool("write_file", mustJSON(writeArgs{Path: "index.html", Content: fixed}))
		rt.observe("write_file", rewriteRes)

		rt.detail("think", "修复完成，重新校验全部检查项", "info")
		recheck := rt.runTool("run_checks", "{}")
		rt.observe("run_checks", recheck)
	}

	rt.detail("think", "产物已通过全部校验，汇总构建结果", "info")
	finRes := rt.runTool("finish", mustJSON(finishArgs{Summary: "已完成 " + DefaultName(tid) + " 的构建与自检"}))
	rt.observe("finish", finRes)
	return nil
}

// observe 展示工具观察结果到时间线。
func (rt *reactSession) observe(tool string, res toolResult) {
	level := "info"
	if !res.OK {
		level = "warn"
	}
	msg := strings.ReplaceAll(res.Observe, "\n", " ")
	rt.detail("observe", fmt.Sprintf("[%s] %s", tool, truncateText(msg, 200)), level)
}

// toolActionText 生成工具行动的前端展示文本。
func toolActionText(name, argsJSON string) string {
	switch name {
	case "plan_app":
		var a planArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err == nil {
			return fmt.Sprintf("选定模板 %s", a.Template)
		}
	case "write_file":
		var a writeArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err == nil {
			return fmt.Sprintf("写入 %s（%d 字符）", a.Path, len(a.Content))
		}
	case "read_file":
		return "读取当前产物"
	case "run_checks":
		return "执行静态校验"
	case "finish":
		return "收尾汇总"
	}
	return ""
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------- 对外入口：与旧流水线 API 兼容 ----------

// Run 执行完整构建任务：先落库项目行（生成中状态可见），再跑 ReAct 循环，完成后回填产物。
func (a *Agent) Run(ctx context.Context, userID uint, brief string, ev PipelineEvents) (*store.Project, error) {
	rt := &reactSession{a: a, ctx: ctx, brief: brief, ev: ev}
	return rt.runProject(ctx, userID, brief, "")
}

// Refine 在已有项目上执行迭代修改：读取旧产物 → ReAct 循环修改 → 回填。
func (a *Agent) Refine(ctx context.Context, userID, projectID uint, instruction string, ev PipelineEvents) (*store.Project, error) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", projectID, userID).First(&p).Error; err != nil {
		return nil, fmt.Errorf("项目不存在")
	}
	rt := &reactSession{a: a, ctx: ctx, brief: instruction, html: p.HTML, ev: ev}
	rt.refineNote = instruction
	return rt.runProject(ctx, userID, instruction, p.Name)
}

// runProject 通用执行壳：建项目行 → ReAct 循环 → 回填产物与状态。
// 项目行先落库修复了旧流水线"事件挂在旧项目 / 生成中不可见"的问题。
func (rt *reactSession) runProject(ctx context.Context, userID uint, brief, existingName string) (*store.Project, error) {
	now := store.Now()
	name := existingName
	if name == "" {
		name = defaultNameFor(rt.a, brief)
	}
	project := &store.Project{
		UserID:      userID,
		Name:        name,
		Brief:       brief,
		Template:    "pending",
		Status:      "generating",
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}
	if err := store.DB.Create(project).Error; err != nil {
		return nil, err
	}
	rt.stage("plan", "Agent 已接管任务，开始分析需求…")

	if err := rt.reactLoop(ctx); err != nil {
		project.Status = "failed"
		project.UpdatedAtMs = store.Now()
		store.DB.Model(project).Updates(map[string]interface{}{"status": "failed", "updated_at_ms": project.UpdatedAtMs})
		rt.appendEvent(project.ID, "done", "构建失败: "+err.Error(), "err")
		return nil, err
	}

	// 模板与名称回填
	if rt.plan.Template != "" {
		project.Template = rt.plan.Template
	} else {
		project.Template = Match(brief)
	}
	if existingName == "" && rt.plan.AppName != "" {
		project.Name = rt.plan.AppName
	}
	project.HTML = rt.html
	project.Status = "ready"
	project.UpdatedAtMs = store.Now()
	if err := store.DB.Model(project).Updates(map[string]interface{}{
		"name": project.Name, "template": project.Template, "html": project.HTML,
		"status": project.Status, "updated_at_ms": project.UpdatedAtMs,
	}).Error; err != nil {
		return nil, err
	}

	if rt.summary != "" {
		rt.appendEvent(project.ID, "done", rt.summary, "info")
	} else {
		rt.appendEvent(project.ID, "done", "构建完成，预览已就绪", "info")
	}
	rt.stage("done", "构建完成，预览已就绪 🎉")
	return project, nil
}

// appendEvent 写入一条项目事件。
func (rt *reactSession) appendEvent(projectID uint, stage, message, level string) {
	store.DB.Create(&store.Event{ProjectID: projectID, Stage: stage, Message: message, Level: level, TsMs: store.Now()})
}

// defaultNameFor 在 ReAct 循环开始前用规则引擎预估应用名（循环内 plan_app 会覆盖）。
func defaultNameFor(a *Agent, brief string) string {
	return DefaultName(Match(brief))
}
