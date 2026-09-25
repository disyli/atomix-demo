package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
	"atomix-demo/server/internal/store"
)

const (
	maxReActLoops    = 12 // 单次任务的工具调用轮数上限，防失控（规划模式另加预算）
	maxAutoRepairs   = 3  // 校验失败后的自动修复次数上限
	reactMaxTokens   = 8192
	reactTemperature = 0.4
)

// reactSession 一次 ReAct 任务会话：持有状态、推送事件、驱动 think→act→observe 循环。
type reactSession struct {
	a          *Agent
	ctx        context.Context
	userID     uint   // 本次任务归属用户（附件归属校验用，不走全局共享字段）
	brief      string
	html       string
	plan       PlanResult
	summary    string
	refineNote string // 非空表示迭代修改模式，值为修改指令
	mode       string // build | plan | research
	attachIDs  []uint // 本次任务携带的附件 ID
	ev         PipelineEvents

	// agent 加固状态
	perm       *permGateway   // 工具权限网关
	budget     *contextBudget // 上下文预算与压缩器
	phase      string         // plan 模式两阶段门控：plan（只允许规划工具）→ act（全量工具）
	research   string         // research 模式下子 Agent 产出的需求简报
	writes     int            // write_file 成功写入次数（上限 2：首写 + 一次整体重写）
	trackEdits bool           // 迭代修改模式：write 成功后进入编辑跟踪，强制后续用 edit_file 精准修改
	existing   *store.Project // 迭代修改模式：复用的已有项目行，不新建 project
	checksPassed bool         // run_checks 最近一次执行且零 issue（完成门控：落库前必须为 true）
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
// mode：build 标准构建；plan 两阶段规划先行；research 基于子 Agent 研究简报构建。
func reactPrompt(brief, refineTo, mode string) string {
	var sb strings.Builder
	sb.WriteString(`你是 Atomix 平台的构建 Agent，通过「思考 → 调用工具 → 观察结果」的循环完成应用构建。

可用工具：
- plan_app：规划应用（应用名/模板/步骤），必须最先调用
- commit_plan：规划模式下提交规划、解锁实施工具（仅规划模式可用）
- write_file：写入完整单文件 HTML 应用（整个任务最多成功写入 2 次：首写 + 一次整体重写）
- edit_file：对产物做精准片段替换（old_string 必须与产物唯一匹配），修复与小改动优先用编辑而不是重写
- read_file：读取当前产物（迭代修改时先读后改）
- run_checks：对产物执行静态校验，浏览器可用时还会实测运行时异常（写入后必须调用）
- finish：校验通过后收尾

硬性规则：
1. 产物是一个完整 <!DOCTYPE html> 文档：HTML + CSS + 原生 JS（可用 Tailwind CDN），中文界面，UI 精致现代
2. 产物必须精炼：总字符控制在 9000 以内，不写冗余注释与无用样式，实现上点到为止（输出体积有硬上限，超出会被截断导致写入失败）
3. 数据用 localStorage 持久化（沙箱内不可用时平台会自动降级，无需担心）
4. 禁止使用 document.cookie（沙箱环境会抛 SecurityError）
5. 必须包含真实交互（事件绑定、DOM 更新），不能是静态页面
6. run_checks 返回 issues 时必须修复并重新提交产物，直到校验通过才能 finish；小问题优先用 edit_file 精准修复，整体性缺陷才 write_file 重写
7. 每轮先用一两句话正文说明本轮思路（为什么这么做、下一步做什么），再发起工具调用；正文不要为空
8. 不要空转：同一工具不要连续重复调用，除非按规则修复后重写`)
	switch mode {
	case "plan":
		sb.WriteString(`

本次为【规划模式】，分两个阶段执行：
- 阶段一（当前）：工具层只有 plan_app 与 commit_plan。先调用 plan_app 产出构建计划（应用名/模板/4-6 条细化步骤），然后在正文给出面向用户的布局说明与关键交互清单，最后调用 commit_plan 提交规划并解锁实施工具。
- 阶段二：全量工具解锁，严格按已提交的规划实施构建。`)
	case "research":
		sb.WriteString(`

本次为【深度研究模式】：研究子 Agent 已对需求做了要点拆解（目标用户、核心功能、信息结构、边界情况），简报将以用户消息注入。先阅读简报再规划实施；write_file 的产物需体现简报结论。`)
	}
	if refineTo != "" {
		sb.WriteString("\n\n本次任务是在已有应用上做迭代修改。当前产物会通过 read_file 提供，优先用 edit_file 最小化修改；确需整体重构时才 write_file 完整文档。")
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
	// research 模式：先委派研究子 Agent（独立上下文）拆解需求，产出简报注入主循环
	if rt.mode == "research" {
		rt.stage("plan", "研究子 Agent 正在拆解需求…")
		brief := rt.a.researchBrief(ctx, rt.brief)
		rt.research = brief
		rt.detail("plan", "研究简报：\n"+truncateText(brief, 500), "info")
	}
	return rt.liveRun(ctx)
}

// toolSpec 工具注册项：定义 + 激活条件（模式门控在工具层物理生效，而非仅靠提示词）。
type toolSpec struct {
	def    llm.Tool
	active func(rt *reactSession) bool
}

// toolSpecs 全量工具注册表。
func toolSpecs() []toolSpec {
	return []toolSpec{
		{def: planAppToolDef(), active: func(rt *reactSession) bool { return true }},
		{def: commitPlanToolDef(), active: func(rt *reactSession) bool { return rt.phase == "plan" }},
		{def: writeFileToolDef(), active: func(rt *reactSession) bool { return rt.phase != "plan" }},
		{def: editFileToolDef(), active: func(rt *reactSession) bool { return rt.phase != "plan" }},
		{def: readFileToolDef(), active: func(rt *reactSession) bool { return rt.phase != "plan" }},
		{def: runChecksToolDef(), active: func(rt *reactSession) bool { return rt.phase != "plan" }},
		{def: finishToolDef(), active: func(rt *reactSession) bool { return rt.phase != "plan" }},
	}
}

// activeTools 按当前阶段返回可用工具定义。
func (rt *reactSession) activeTools() []llm.Tool {
	out := make([]llm.Tool, 0, len(toolSpecs()))
	for _, s := range toolSpecs() {
		if s.active(rt) {
			out = append(out, s.def)
		}
	}
	return out
}

// liveRun 真实 LLM 的 ReAct 循环：权限网关 + 上下文压缩 + 工具回喂闭环。
func (rt *reactSession) liveRun(ctx context.Context) error {
	messages := []llm.ChatMessage{
		{Role: "system", Content: reactPrompt(rt.brief, rt.refineTo(), rt.mode)},
	}
	if rt.refineTo() != "" {
		messages = append(messages, llm.ChatMessage{Role: "user", Content: rt.brief})
	}
	// 附件上下文：文本附件并入用户消息；图片以多模态 parts 发给 vision 模型
	if atts := loadAttachments(rt.userID, rt.attachIDs); len(atts) > 0 {
		var parts []llm.ContentPart
		var textNotes []string
		for _, at := range atts {
			if at.DataURL != "" {
				parts = append(parts, llm.ContentPart{Type: "text", Text: "【附件图片 " + at.Name + "，请在构建时参考其内容】"})
				p := llm.ContentPart{Type: "image_url"}
				p.ImageURL = &struct {
					URL string `json:"url"`
				}{URL: at.DataURL}
				parts = append(parts, p)
			} else if at.Content != "" {
				textNotes = append(textNotes, "【附件 "+at.Name+"】\n"+at.Content)
			}
		}
		if len(parts) > 0 {
			messages = append(messages, llm.ChatMessage{Role: "user", ContentParts: parts})
		}
		if len(textNotes) > 0 {
			messages = append(messages, llm.ChatMessage{Role: "user", Content: strings.Join(textNotes, "\n\n")})
		}
		rt.detail("build", fmt.Sprintf("已注入 %d 个附件作为构建上下文", len(atts)), "info")
	}
	// 研究简报注入（research 模式）
	if rt.research != "" {
		messages = append(messages, llm.ChatMessage{Role: "user", Content: "【研究简报（研究子 Agent 产出）】\n" + rt.research})
	}

	loopCap := maxReActLoops
	if rt.mode == "plan" {
		loopCap += 4 // 规划模式两阶段，多给 4 轮预算
	}
	loops, repairs := 0, 0
	for {
		// 用户停止检查点：每轮循环、每次模型调用与每个工具执行前都会检查
		if ctx.Err() != nil {
			rt.stage("done", "已按用户要求停止构建")
			return ErrCanceled
		}
		loops++
		if loops > loopCap {
			if rt.html == "" {
				return fmt.Errorf("ReAct 循环超过 %d 轮仍未产出可用产物", loopCap)
			}
			rt.detail("done", "达到轮数上限，以当前最优产物收尾", "warn")
			break
		}

		resp, err := rt.a.LLM.ChatWithTools(ctx, messages, rt.activeTools(), reactTemperature, reactMaxTokens)
		if err != nil {
			if ctx.Err() != nil {
				rt.stage("done", "已按用户要求停止构建")
				return ErrCanceled
			}
			if rt.html != "" {
				rt.detail("build", "模型调用失败，以已有产物收尾："+err.Error(), "warn")
				break
			}
			return fmt.Errorf("模型调用失败: %w", err)
		}

		// 思考事件：每轮模型回复文本（若有）展示一次
		if resp.Content != "" {
			rt.detail("think", truncateText(resp.Content, 200), "info")
		}

		// 模型直接给出最终回答（未调用工具）
		if len(resp.ToolCalls) == 0 {
			if rt.html != "" {
				break
			}
			// 循环还没产出任何东西就结束回答：强制拉回工具循环
			messages = append(messages, llm.ChatMessage{Role: "assistant", Content: resp.Content})
			pull := "请立即调用工具完成任务：先用 plan_app 规划，再用 write_file 写入完整 HTML。"
			if rt.phase == "plan" {
				pull = "请立即调用 plan_app 产出构建计划，然后调用 commit_plan 提交规划。"
			}
			messages = append(messages, llm.ChatMessage{Role: "user", Content: pull})
			continue
		}

		// 逐个执行工具调用，观察结果回喂
		messages = append(messages, llm.ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		shouldStop := false
		for _, tc := range resp.ToolCalls {
			// 用户停止检查点：工具之间也可退出
			if ctx.Err() != nil {
				rt.stage("done", "已按用户要求停止构建")
				return ErrCanceled
			}
			// 权限网关：ask 级工具先向用户推送确认卡片，拒绝/超时则拦截并把结果回喂模型
			allowed, denyMsg := rt.perm.authorize(ctx, tc.Function.Name, permissionDetail(rt, tc.Function.Name, tc.Function.Arguments), rt.ev)
			if !allowed {
				messages = append(messages, llm.ChatMessage{Role: "tool", Content: denyMsg, ToolCallID: tc.ID})
				rt.detail("observe", fmt.Sprintf("[%s] 权限拦截：%s", tc.Function.Name, truncateText(denyMsg, 120)), "warn")
				continue
			}

			result := rt.runTool(tc.Function.Name, tc.Function.Arguments)

			// 行动事件（前端展示）
			think := result.Think
			if think == "" {
				think = toolActionText(tc.Function.Name, tc.Function.Arguments)
			}
			rt.detail("act", fmt.Sprintf("调用 %s → %s", tc.Function.Name, think), "info")

			// 观察事件：工具真实执行结果同步展示到时间线（与回喂模型的文本一致）
			rt.observe(tc.Function.Name, result)

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
				Content:    clampObserve(result.Observe), // 单条观察限长，防污染上下文
				ToolCallID: tc.ID,
			})
		}
		if shouldStop {
			break
		}
		// 上下文压缩：超预算时把中间历史压成状态摘要（保留 system/需求/最近消息）
		if rt.budget.maybeCompress(&messages) {
			rt.detail("build", "上下文接近预算，已自动压缩历史消息为状态摘要", "info")
		}
	}
	return nil
}

// refineTo 是否处于迭代修改模式（非空即修改指令）。
func (rt *reactSession) refineTo() string { return rt.refineNote }

// act 展示工具行动事件（与 live 模式格式一致）。
func (rt *reactSession) act(tool, argsJSON string) {
	rt.detail("act", fmt.Sprintf("调用 %s → %s", tool, toolActionText(tool, argsJSON)), "info")
}

// demoRun 演示模式的脚本化 ReAct 轨迹：与真实循环同构（plan → write → checks(失败) → edit_file 精准修复 → checks(通过) → finish），
// 工具全部真实执行，让评审者无 Key 也能观察到完整的 think→act→observe 闭环。
// 迭代修改模式（refineTo 非空）：read_file → edit_file 指令感知的最小化修改 → checks → finish，
// 与 live 模式增量行为同构（旧功能保留、源码真实变化、edit_file 留痕）。
func (rt *reactSession) demoRun() error {
	if rt.ctx != nil && rt.ctx.Err() != nil {
		rt.stage("done", "已按用户要求停止构建")
		return ErrCanceled
	}
	if rt.refineTo() != "" && rt.html != "" {
		return rt.demoRefine()
	}
	rt.stage("plan", "Agent 正在理解需求并制定构建计划…")

	rt.detail("think", "分析需求关键词，匹配内置模板能力清单，确定应用形态与构建步骤", "info")
	tid := Match(rt.brief)
	planArgsJSON := mustJSON(planArgs{
		AppName:  DefaultName(tid),
		Template: tid,
		Reason:   "演示模式：按关键词规则匹配最贴合的内置模板",
		Steps:    []string{"搭建布局骨架", "实现数据模型与持久化", "绑定交互事件", "自检产物"},
	})
	rt.act("plan_app", planArgsJSON)
	planRes := rt.runTool("plan_app", planArgsJSON)
	rt.observe("plan_app", planRes)

	rt.detail("think", "按计划生成完整单文件应用：布局 + 数据层 + 交互层 + 持久化", "info")
	_, html, _ := RenderTemplate(tid, DefaultName(tid))
	// 演示自修复：首版注入一个会被真实校验器抓住的问题（document.cookie）
	broken := strings.Replace(html, "<head>", "<head>\n<script>document.cookie='demo=1';</script>", 1)
	writeArgsJSON := mustJSON(writeArgs{Path: "index.html", Content: broken})
	rt.act("write_file", writeArgsJSON)
	writeRes := rt.runTool("write_file", writeArgsJSON)
	rt.observe("write_file", writeRes)

	rt.detail("think", "产物已写入，执行静态校验确认文档结构、沙箱兼容性与交互完整性", "info")
	rt.act("run_checks", "{}")
	checkRes := rt.runTool("run_checks", "{}")
	rt.observe("run_checks", checkRes)

	if !checkRes.OK {
		rt.detail("think", "校验发现沙箱兼容性问题：document.cookie 在 opaque origin 下抛 SecurityError。用 edit_file 精准移除该片段，不重写全文", "warn")
		// 真实修复：edit_file 精准替换（演示行级编辑能力）
		editJSON := mustJSON(editArgs{
			OldString: "<script>document.cookie='demo=1';</script>\n",
			NewString: "",
		})
		if !strings.Contains(rt.html, "<script>document.cookie='demo=1';</script>\n") {
			editJSON = mustJSON(editArgs{OldString: "<script>document.cookie='demo=1';</script>", NewString: ""})
		}
		rt.act("edit_file", editJSON)
		editRes := rt.runTool("edit_file", editJSON)
		rt.observe("edit_file", editRes)

		rt.detail("think", "修复完成，重新校验全部检查项", "info")
		rt.act("run_checks", "{}")
		recheck := rt.runTool("run_checks", "{}")
		rt.observe("run_checks", recheck)
	}

	rt.detail("think", "产物已通过全部校验，汇总构建结果", "info")
	finishJSON := mustJSON(finishArgs{Summary: "已完成 " + DefaultName(tid) + " 的构建与自检"})
	rt.act("finish", finishJSON)
	finRes := rt.runTool("finish", finishJSON)
	rt.observe("finish", finRes)
	return nil
}

// demoRefine 演示模式的迭代修改轨迹：read_file → edit_file 指令感知最小化修改 → run_checks → finish。
// 按修改指令关键词注入对应功能（历史记录 / 主题 / 其他统一为标题徽标更新），保证
// 源码真实变化、旧功能保留、edit_file 事件留痕——与 live 模式增量语义一致。
func (rt *reactSession) demoRefine() error {
	rt.stage("plan", "Agent 正在理解修改指令…")
	rt.detail("think", "读取当前产物，规划最小化修改点（不整体重写）", "info")

	readJSON := "{}"
	rt.act("read_file", readJSON)
	readRes := rt.runTool("read_file", readJSON)
	rt.observe("read_file", readRes)

	instr := strings.ToLower(rt.refineTo())
	old, add := "", ""
	titleMark := "</h1>"
	switch {
	case strings.Contains(instr, "历史"):
		// 注入历史记录功能：容器 + 逻辑 + 样式三处小编辑
		old = titleMark
		add = `</h1><button id="toggleHist" style="margin-left:12px;font-size:14px;padding:4px 12px;border:1px solid #cbd5e1;border-radius:8px;background:#f8fafc;cursor:pointer">历史</button><div id="histBox" style="display:none;margin-top:10px;padding:10px;border:1px dashed #cbd5e1;border-radius:10px;max-height:160px;overflow:auto;font-size:13px;color:#475569"></div>`
		rt.detail("think", "需求为历史记录：在标题区加入历史开关按钮与面板，并在逻辑层记录最近操作", "info")
	default:
		old = titleMark
		add = `</h1><span style="margin-left:10px;font-size:12px;color:#64748b;background:#f1f5f9;padding:2px 10px;border-radius:999px">已按指令更新</span>`
		rt.detail("think", "通用修改：在标题区注入更新徽标，最小化改动", "info")
	}
	if !strings.Contains(rt.html, titleMark) {
		// 产物标题结构异常时兜底：改在 body 起始处插入提示条
		titleMark = "<body>"
		old = titleMark
		add = `<body><div style="padding:8px 14px;background:#ecfdf5;color:#065f46;border-radius:10px;font-size:13px;margin-bottom:10px">已按修改指令更新（演示模式）</div>`
	}
	if strings.Contains(rt.html, old) {
		editJSON := mustJSON(editArgs{OldString: old, NewString: add})
		rt.act("edit_file", editJSON)
		editRes := rt.runTool("edit_file", editJSON)
		rt.observe("edit_file", editRes)

		if strings.Contains(instr, "历史") {
			// 逻辑层：绑定历史记录（监听按键/点击并写入 localStorage 历史）
			logicOld := "</script>"
			logicNew := `(function(){var H='atomix_hist',box=document.getElementById('histBox'),btn=document.getElementById('toggleHist');if(!box||!btn){return;}
var read=function(){try{return JSON.parse(localStorage.getItem(H)||'[]')}catch(e){return[]}};
var render=function(){var a=read();box.innerHTML=a.length?a.slice(-20).map(function(x){return '<div>• '+x+'</div>'}).join(''):'暂无历史记录'};
if(btn){btn.addEventListener('click',function(){box.style.display=box.style.display==='none'?'block':'none';render()})}
document.addEventListener('keydown',function(e){var k=read();k.push('按键 '+(e.key||e.code)+' @'+new Date().toLocaleTimeString());localStorage.setItem(H,JSON.stringify(k));render()});
document.addEventListener('click',function(e){var k=read();k.push('点击 '+((e.target&&e.target.tagName)||'?')+' @'+new Date().toLocaleTimeString());localStorage.setItem(H,JSON.stringify(k));render()});
render()})();
</script>`
			if strings.Contains(rt.html, logicOld) {
				lj := mustJSON(editArgs{OldString: logicOld, NewString: logicNew})
				rt.act("edit_file", lj)
				lr := rt.runTool("edit_file", lj)
				rt.observe("edit_file", lr)
			}
		}
	} else {
		rt.detail("think", "产物中未找到预期锚点，无法做最小化修改", "warn")
	}

	rt.detail("think", "修改完成，执行静态校验确认未破坏产物", "info")
	rt.act("run_checks", "{}")
	recheck := rt.runTool("run_checks", "{}")
	rt.observe("run_checks", recheck)

	rt.detail("think", "校验通过，汇总修改结果", "info")
	finishJSON := mustJSON(finishArgs{Summary: "已按指令完成增量修改并通过自检"})
	rt.act("finish", finishJSON)
	finRes := rt.runTool("finish", finishJSON)
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
	case "commit_plan":
		return "提交规划，解锁实施工具"
	case "write_file":
		var a writeArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err == nil {
			return fmt.Sprintf("写入 %s（%d 字符）", a.Path, len(a.Content))
		}
	case "edit_file":
		var a editArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err == nil {
			return fmt.Sprintf("精准修改 index.html（替换 %d 字符片段）", len(a.OldString))
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

// permissionDetail 生成权限确认卡片上展示的操作详情。
// write_file / edit_file 附带内容级统一 diff（增行 + / 删行 -），供用户审查后再放行；
// 格式为"摘要首行 + 分隔标记 + diff 正文"，前端按 diffMarker 切分渲染。
func permissionDetail(rt *reactSession, name, argsJSON string) string {
	switch name {
	case "write_file":
		var a writeArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
			break
		}
		if rt.html == "" {
			return fmt.Sprintf("首次写入 %s（%d 字符），产物为完整单文件应用。\n%s\n%s",
				a.Path, len(a.Content), diffMarker, renderDiff(lineDiff("", a.Content), writeDiffCap))
		}
		lines := lineDiff(rt.html, a.Content)
		adds, dels, _ := diffStats(lines)
		return fmt.Sprintf("整体重写 %s（%d → %d 字符，+%d/-%d 行），将覆盖当前产物。\n%s\n%s",
			a.Path, len(rt.html), len(a.Content), adds, dels, diffMarker, renderDiff(lines, writeDiffCap))
	case "edit_file":
		var a editArgs
		if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
			break
		}
		// 在产物中定位 old_string，带上前后各 2 行上下文生成定位 diff；找不到时退化为片段级 diff
		lines := fragmentDiff(rt.html, a.OldString, a.NewString, a.ReplaceAll)
		adds, dels, _ := diffStats(lines)
		head := fmt.Sprintf("精准修改 index.html（替换 %d 处，+%d/-%d 行）", 1, adds, dels)
		if a.ReplaceAll {
			head = fmt.Sprintf("精准修改 index.html（替换全部匹配，+%d/-%d 行）", adds, dels)
		}
		return head + "\n" + diffMarker + "\n" + renderDiff(lines, editDiffCap)
	}
	return "Agent 请求调用工具 " + name
}

// fragmentDiff 生成 edit_file 的上下文 diff：以 old_string 为中心，保留前后各 2 行未改动上下文。
// old_string 在产物中无匹配或匹配多处且未指定 replace_all 时，退化为 old/new 片段的直接对比。
func fragmentDiff(html, oldStr, newStr string, replaceAll bool) []diffLine {
	count := strings.Count(html, oldStr)
	if count == 0 || (count > 1 && !replaceAll) {
		return lineDiff(oldStr, newStr)
	}
	idx := strings.Index(html, oldStr)
	before := splitLines(html[:idx])
	after := splitLines(html[idx+len(oldStr):])
	inner := lineDiff(oldStr, newStr)

	ctx := 2
	out := make([]diffLine, 0, len(inner)+2*ctx)
	for i := len(before) - ctx; i < len(before); i++ {
		if i >= 0 {
			out = append(out, diffLine{' ', before[i]})
		}
	}
	out = append(out, inner...)
	for i := 0; i < ctx && i < len(after); i++ {
		out = append(out, diffLine{' ', after[i]})
	}
	return out
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
// mode: build | plan | research；attachmentIDs: 随任务携带的附件。
func (a *Agent) Run(ctx context.Context, userID uint, brief, mode string, attachmentIDs []uint, ev PipelineEvents) (*store.Project, error) {
	rt := &reactSession{
		a: a, ctx: ctx, userID: userID, brief: brief, mode: mode, attachIDs: attachmentIDs, ev: ev,
		perm: newPermGateway(a.PermRegistry), budget: newContextBudget(),
		phase: "act",
	}
	if mode == "plan" {
		rt.phase = "plan" // 规划模式从规划阶段起步：实施工具在工具层物理不可见
	}
	return rt.runProject(ctx, userID, brief, "")
}

// Refine 在已有项目上执行迭代修改：读取旧产物 → ReAct 循环修改 → 回填。
// 同一 project 的每一轮修改都保存为新的对话轮次（Message 表），项目行本身复用不新建。
func (a *Agent) Refine(ctx context.Context, userID, projectID uint, instruction string, attachmentIDs []uint, ev PipelineEvents) (*store.Project, error) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", projectID, userID).First(&p).Error; err != nil {
		return nil, fmt.Errorf("项目不存在")
	}
	// 同一项目同时只允许一次构建/迭代，防并发覆盖
	defer a.LockProject(projectID)()
	rt := &reactSession{
		a: a, ctx: ctx, userID: userID, brief: instruction, html: p.HTML, attachIDs: attachmentIDs, ev: ev,
		perm: newPermGateway(a.PermRegistry), budget: newContextBudget(),
		phase: "act", trackEdits: true, existing: &p,
	}
	rt.refineNote = instruction
	return rt.runProject(ctx, userID, instruction, p.Name)
}

// runProject 通用执行壳：建项目行（或复用已有行）→ ReAct 循环 → verified 状态机收尾。
// 新构建时项目行先落库修复了旧流水线"事件挂在旧项目 / 生成中不可见"的问题；
// 迭代修改时（existing 非空）复用同一项目行，事件与产物更新都落在该项目上。
//
// 状态机（修复"未校验即完成"）：产物必须满足【写入(write/edit) → 校验通过(run_checks) →
// 落库(HTML+LastGoodHTML) → 快照(CreateSnapshot)】全链路成功，项目才会进入 ready("完成")；
// 校验未通过 / 循环失败 / 循环耗尽时项目状态落 failed 并保留 LastGoodHTML（最后成功版本），
// 预览始终指向可用产物；每轮终态同时落 Message 表（此前 generate/refine 失败轮的
// 用户消息在 handlers 层重复落库且 projectId 传参错位，统一收敛到这里）。
func (rt *reactSession) runProject(ctx context.Context, userID uint, brief, existingName string) (*store.Project, error) {
	now := store.Now()
	project := rt.existing
	if project == nil {
		name := existingName
		if name == "" {
			name = defaultNameFor(rt.a, brief)
		}
		project = &store.Project{
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
		// 新项目的首轮用户消息在此落库（成功失败都保留，历史回看完整）
		AppendUserMessage(project.ID, userID, brief)
	} else {
		// 复用已有项目行：状态回到 generating，展示"本轮修改进行中"；本轮指令落 Message
		AppendUserMessage(project.ID, userID, brief)
		store.DB.Model(project).Updates(map[string]interface{}{"status": "generating", "updated_at_ms": now})
	}

	// 事件实时落库：SSE 推送的同时写入 Event 表，历史回看可完整回放 ReAct 轨迹
	if rt.ev.OnStage != nil {
		userStage := rt.ev.OnStage
		rt.ev.OnStage = func(stage, message string) {
			userStage(stage, message)
			store.DB.Create(&store.Event{ProjectID: project.ID, Stage: stage, Message: message, Level: "stage", TsMs: store.Now()})
		}
	}
	if rt.ev.OnDetail != nil {
		userDetail := rt.ev.OnDetail
		rt.ev.OnDetail = func(stage, message, level string) {
			userDetail(stage, message, level)
			store.DB.Create(&store.Event{ProjectID: project.ID, Stage: stage, Message: message, Level: level, TsMs: store.Now()})
		}
	}
	if rt.ev.OnPermission != nil {
		userPerm := rt.ev.OnPermission
		rt.ev.OnPermission = func(reqID, tool, detail string) {
			userPerm(reqID, tool, detail)
			store.DB.Create(&store.Event{ProjectID: project.ID, Stage: "act", Message: "权限确认请求 [" + tool + "]：" + detail, Level: "warn", TsMs: store.Now()})
		}
	}

	rt.stage("plan", "Agent 已接管任务，开始分析需求…")

	loopErr := rt.reactLoop(ctx)

	// 用户主动停止：状态 stopped（区别于失败），保留最后成功版本，事件留痕
	if errors.Is(loopErr, ErrCanceled) {
		store.MarkProjectStatus(project.ID, "stopped", true)
		rt.appendEvent(project.ID, "done", "用户已停止本次构建", "warn")
		AppendRunMessage(project.ID, brief, userID, "stopped")
		return nil, loopErr
	}

	// ---------- verified 状态机收尾：完成 = 写入 + 校验通过 + 落库 + 快照 ----------
	// checksPassed 由 run_checks 工具成功执行且无 issue 时置位（tools.go）
	hasProduct := rt.html != ""
	if loopErr == nil && hasProduct && rt.checksPassed {
		// 全链路成功：模板/名称回填 → 快照事务（version+1 / HTML / LastGoodHTML / status=ready）
		if rt.existing == nil {
			if rt.plan.Template != "" {
				project.Template = rt.plan.Template
			} else {
				project.Template = Match(brief)
			}
			if existingName == "" && rt.plan.AppName != "" {
				project.Name = rt.plan.AppName
			}
			store.DB.Model(project).Updates(map[string]interface{}{"name": project.Name, "template": project.Template})
		}
		label := "首次构建"
		if rt.existing != nil {
			label = "迭代：" + truncateText(brief, 60)
		}
		snap, snapErr := store.CreateSnapshot(project.ID, rt.html, label)
		if snapErr != nil {
			store.MarkProjectStatus(project.ID, "failed", true)
			rt.appendEvent(project.ID, "done", "版本落库失败: "+snapErr.Error(), "err")
			AppendRunMessage(project.ID, brief, userID, "failed")
			return nil, snapErr
		}
		// 重载最新项目行（快照事务已更新 version/html 等）
		if err := store.DB.Where("id = ?", project.ID).First(project).Error; err != nil {
			return nil, err
		}
		rt.appendEvent(project.ID, "done", fmt.Sprintf("产物已通过校验并落库为 v%d：%s", snap.Version, label), "info")
		if rt.summary != "" {
			rt.appendEvent(project.ID, "done", rt.summary, "info")
		}
		if rt.existing != nil {
			rt.stage("done", fmt.Sprintf("修改完成，预览已更新（v%d）🎉", snap.Version))
		} else {
			rt.stage("done", fmt.Sprintf("构建完成，预览已就绪（v%d）🎉", snap.Version))
		}
		AppendRunMessage(project.ID, brief, userID, "done")
		return project, nil
	}

	// ---------- 失败路径：状态 failed 落库 + 保留最后成功版本 ----------
	reason := "构建失败"
	switch {
	case loopErr != nil:
		reason = "构建失败: " + loopErr.Error()
	case !hasProduct:
		reason = "构建失败：循环结束但产物未写入"
	case !rt.checksPassed:
		reason = "构建失败：产物未通过校验（存在未修复的 issues），已保留最后成功版本"
	}
	store.MarkProjectStatus(project.ID, "failed", true)
	rt.appendEvent(project.ID, "done", reason, "err")
	if loopErr == nil {
		loopErr = fmt.Errorf("%s", reason)
	}
	AppendRunMessage(project.ID, brief, userID, "failed")
	return nil, loopErr
}

// appendEvent 写入一条项目事件。
func (rt *reactSession) appendEvent(projectID uint, stage, message, level string) {
	store.DB.Create(&store.Event{ProjectID: projectID, Stage: stage, Message: message, Level: level, TsMs: store.Now()})
}

// AppendUserMessage 对话消息落库：用户一轮输入（构建需求或修改指令）。
func AppendUserMessage(projectID, userID uint, text string) {
	store.DB.Create(&store.Message{ProjectID: projectID, UserID: userID, Role: "user", Kind: "text", Text: text, CreatedAtMs: store.Now()})
}

// AppendRunMessage 对话消息落库：助手一轮构建/修改回合的终态记录。
// status：done / failed / stopped；text 为该轮的用户输入（回看时与用户消息成对还原）。
func AppendRunMessage(projectID uint, text string, userID uint, status string) {
	store.DB.Create(&store.Message{ProjectID: projectID, UserID: userID, Role: "assistant", Kind: "run", Text: text, Status: status, CreatedAtMs: store.Now()})
}

// defaultNameFor 在 ReAct 循环开始前用规则引擎预估应用名（循环内 plan_app 会覆盖）。
func defaultNameFor(a *Agent, brief string) string {
	return DefaultName(Match(brief))
}
