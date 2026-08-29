package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
)

// ---------- 工具定义（ReAct 循环的可执行动作） ----------

// toolDefs 返回 ReAct 循环暴露给模型的全部工具定义（OpenAI 兼容格式）。
func toolDefs() []llm.Tool {
	return []llm.Tool{
		{Type: "function", Function: llm.ToolFunction{
			Name:        "plan_app",
			Description: "规划应用：根据需求选定应用名、模板（todo 待办清单 / notes 彩色便签墙 / kanban 轻量看板）与构建步骤。必须最先调用。",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "app_name":     {"type": "string", "description": "应用名，中文，不超过 8 字"},
    "template":     {"type": "string", "enum": ["todo", "notes", "kanban"], "description": "最贴合需求的模板"},
    "reason":       {"type": "string", "description": "一句话选择理由（中文）"},
    "steps":        {"type": "array", "items": {"type": "string"}, "description": "3-5 条构建步骤（中文短语）"}
  },
  "required": ["app_name", "template"]
}`),
		}},
		{Type: "function", Function: llm.ToolFunction{
			Name:        "write_file",
			Description: "写入完整单文件应用产物（一个完整 <!DOCTYPE html> 文档）。整个构建过程只允许成功写入一次。",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "固定为 index.html"},
    "content": {"type": "string", "description": "完整 HTML 文档内容"}
  },
  "required": ["path", "content"]
}`),
		}},
		{Type: "function", Function: llm.ToolFunction{
			Name:        "read_file",
			Description: "读取当前产物内容（用于迭代修改场景了解现状，或写入后复查）。",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "固定为 index.html"}
  },
  "required": ["path"]
}`),
		}},
		{Type: "function", Function: llm.ToolFunction{
			Name:        "run_checks",
			Description: "对当前产物执行真实静态校验（文档结构/沙箱兼容性/存储调用/交互绑定/体积）。写入后必须调用；返回的 issues 不为空时须修复后重写。",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": []
}`),
		}},
		{Type: "function", Function: llm.ToolFunction{
			Name:        "finish",
			Description: "收尾：汇总本次构建做了什么。只能在产物通过校验后调用，调用后循环结束。",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "summary": {"type": "string", "description": "给用户的一句话总结（中文）"}
  },
  "required": ["summary"]
}`),
		}},
	}
}

// ---------- 工具执行结果 ----------

// toolResult 一次工具执行的结构化结果。
type toolResult struct {
	OK      bool
	Observe string // 回喂给模型的观察文本
	Think   string // 前端展示的行动说明
}

// planArgs plan_app 的入参。
type planArgs struct {
	AppName  string   `json:"app_name"`
	Template string   `json:"template"`
	Reason   string   `json:"reason"`
	Steps    []string `json:"steps"`
}

// pathArgs read_file / write_file 的 path 入参。
type pathArgs struct {
	Path string `json:"path"`
}

// writeArgs write_file 的入参。
type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// finishArgs finish 的入参。
type finishArgs struct {
	Summary string `json:"summary"`
}

// runTool 执行一个工具调用，返回观察结果。sess 提供执行上下文（当前会话状态）。
func (rt *reactSession) runTool(name, argsJSON string) toolResult {
	switch name {
	case "plan_app":
		return rt.toolPlan(argsJSON)
	case "write_file":
		return rt.toolWrite(argsJSON)
	case "read_file":
		return rt.toolRead(argsJSON)
	case "run_checks":
		return rt.toolChecks()
	case "finish":
		return rt.toolFinish(argsJSON)
	default:
		return toolResult{OK: false, Observe: fmt.Sprintf("未知工具 %s，可用工具：plan_app / write_file / read_file / run_checks / finish", name)}
	}
}

func (rt *reactSession) toolPlan(argsJSON string) toolResult {
	var args planArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolResult{OK: false, Observe: "plan_app 参数解析失败：" + err.Error()}
	}
	tid := Match(rt.brief)
	if _, ok := Get(args.Template); ok {
		tid = args.Template
	}
	if args.AppName == "" {
		args.AppName = DefaultName(tid)
	}
	if args.Reason == "" {
		args.Reason = "贴合需求的内置模板"
	}
	rt.plan = PlanResult{AppName: args.AppName, Template: tid, Reason: args.Reason}
	for _, s := range args.Steps {
		rt.plan.Steps = append(rt.plan.Steps, PlanStep{ID: len(rt.plan.Steps) + 1, Title: s, Kind: "build"})
	}
	fillSteps(&rt.plan)
	rt.stage("plan", fmt.Sprintf("选定模板 %s（%s）", tid, args.AppName))
	for _, s := range rt.plan.Steps {
		rt.detail("plan", fmt.Sprintf("Step %d · %s", s.ID, s.Title), "info")
	}
	return toolResult{OK: true, Observe: fmt.Sprintf("计划已确认：应用名 %s，模板 %s，共 %d 步。现在调用 write_file 生成完整 HTML。", args.AppName, tid, len(rt.plan.Steps))}
}

func (rt *reactSession) toolWrite(argsJSON string) toolResult {
	var args writeArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolResult{OK: false, Observe: "write_file 参数解析失败：" + err.Error()}
	}
	if args.Path != "" && args.Path != "index.html" {
		return toolResult{OK: false, Observe: "path 必须是 index.html（单文件产物约束）"}
	}
	if len(args.Content) < 200 {
		return toolResult{OK: false, Observe: fmt.Sprintf("写入被拒绝：内容仅 %d 字符，不像完整应用。请输出完整 HTML 文档。", len(args.Content))}
	}
	lower := strings.ToLower(args.Content)
	if !strings.Contains(lower, "<!doctype html") && !strings.Contains(lower, "<html") {
		return toolResult{OK: false, Observe: "写入被拒绝：缺少 <!DOCTYPE html> 或 <html> 文档结构，请重新输出完整文档。"}
	}
	rt.html = args.Content
	return toolResult{OK: true, Observe: fmt.Sprintf("index.html 已写入（%d 字符）。请立即调用 run_checks 校验产物。", len(args.Content)), Think: fmt.Sprintf("写入 index.html（%d 字符）", len(args.Content))}
}

func (rt *reactSession) toolRead(string) toolResult {
	if rt.html == "" {
		return toolResult{OK: true, Observe: "index.html 尚不存在（还没有执行过写入）。"}
	}
	head := rt.html
	if len(head) > 1500 {
		head = head[:1500] + "\n<!-- ... truncated ... -->"
	}
	return toolResult{OK: true, Observe: fmt.Sprintf("index.html 当前内容（截断展示，共 %d 字符）：\n%s", len(rt.html), head)}
}

func (rt *reactSession) toolChecks() toolResult {
	if rt.html == "" {
		return toolResult{OK: false, Observe: "尚无产物可校验：请先调用 write_file 写入 index.html。"}
	}
	issues := checkProduct(rt.html)
	if len(issues) == 0 {
		rt.detail("verify", "静态校验全部通过", "info")
		return toolResult{OK: true, Observe: "校验通过：文档结构、沙箱兼容性、存储降级、交互绑定、体积均无问题。可以调用 finish 收尾。"}
	}
	rt.detail("verify", fmt.Sprintf("发现 %d 个问题，需修复后重写", len(issues)), "warn")
	return toolResult{OK: false, Observe: "校验发现以下问题：\n- " + strings.Join(issues, "\n- ") + "\n请针对以上问题修复代码，并重新调用 write_file 写入完整文档。"}
}

func (rt *reactSession) toolFinish(argsJSON string) toolResult {
	var args finishArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		args.Summary = ""
	}
	if rt.html == "" {
		return toolResult{OK: false, Observe: "还不能 finish：产物尚未写入。请先 write_file。"}
	}
	if issues := checkProduct(rt.html); len(issues) > 0 {
		return toolResult{OK: false, Observe: "finish 被拒绝：产物仍有未通过校验的问题：\n- " + strings.Join(issues, "\n- ") + "\n请修复后重写再 finish。"}
	}
	rt.summary = args.Summary
	return toolResult{OK: true, Observe: "构建完成。"}
}

// ---------- 产物静态校验（run_checks 的真实实现） ----------

// checkProduct 对产物执行真实静态校验，返回问题列表（空列表 = 通过）。
// 这使 run_checks 不是装饰性工具：失败会回喂给模型触发真实的修复循环。
func checkProduct(html string) []string {
	var issues []string
	lower := strings.ToLower(html)

	if !strings.Contains(lower, "<!doctype html") && !strings.Contains(lower, "<html") {
		issues = append(issues, "[error] 缺少 HTML 文档结构（<!DOCTYPE html>）")
	}
	if !strings.Contains(lower, "</html>") {
		issues = append(issues, "[error] 文档未闭合（缺少 </html>）")
	}
	if !strings.Contains(lower, "<script") {
		issues = append(issues, "[error] 没有任何 <script>，应用不具备真实交互")
	}
	if !strings.Contains(lower, "localstorage") && !strings.Contains(lower, "sessionstorage") {
		issues = append(issues, "[warn] 未检测到 localStorage/sessionStorage 调用，数据可能不持久化（要求：数据刷新不丢失）")
	}
	// 沙箱兼容性：document.cookie 在 opaque origin 的沙箱 iframe 中会抛 SecurityError
	if strings.Contains(lower, "document.cookie") {
		issues = append(issues, "[error] 使用了 document.cookie：沙箱 iframe（opaque origin）中会抛 SecurityError 导致应用白屏，请改用内存变量或 localStorage")
	}
	// 常见交互占位符残留
	for _, ph := range []string{"todo：", "此处留空", "placeholder-body", "YOUR_API_KEY"} {
		if strings.Contains(lower, strings.ToLower(ph)) {
			issues = append(issues, fmt.Sprintf("[warn] 疑似占位符残留：%q", ph))
		}
	}
	if len(html) < 1000 {
		issues = append(issues, fmt.Sprintf("[warn] 产物仅 %d 字符，过于单薄，可能不满足真实交互要求", len(html)))
	}
	if len(html) > 180000 {
		issues = append(issues, fmt.Sprintf("[warn] 产物达 %d 字符，超出单文件合理体积", len(html)))
	}
	return issues
}
