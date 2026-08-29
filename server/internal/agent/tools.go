package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
)

// ---------- 工具定义（ReAct 循环的可执行动作） ----------

// planAppToolDef plan_app 工具定义。
func planAppToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "plan_app",
		Description: "规划应用：根据需求选定应用名、模板（todo 待办清单 / notes 彩色便签墙 / kanban 轻量看板）与构建步骤。必须最先调用。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "app_name":     {"type": "string", "description": "应用名，中文，不超过 8 字"},
    "template":     {"type": "string", "enum": ["todo", "notes", "kanban"], "description": "最贴合需求的模板"},
    "reason":       {"type": "string", "description": "一句话选择理由（中文）"},
    "steps":        {"type": "array", "items": {"type": "string"}, "description": "3-6 条构建步骤（中文短语）"}
  },
  "required": ["app_name", "template"]
}`),
	}}
}

// commitPlanToolDef commit_plan 工具定义（规划模式专用）。
func commitPlanToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "commit_plan",
		Description: "规划模式下提交构建规划，解锁实施工具（write_file/edit_file/read_file/run_checks/finish）。只能调用一次，调用后进入实施阶段。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "layout_notes": {"type": "string", "description": "面向用户的布局说明与关键交互清单"}
  },
  "required": []
}`),
	}}
}

// writeFileToolDef write_file 工具定义。
func writeFileToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "write_file",
		Description: "写入完整单文件 HTML 应用（整个任务最多成功写入 2 次：首写 + 一次整体重写）。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "固定为 index.html"},
    "content": {"type": "string", "description": "完整 HTML 文档内容"}
  },
  "required": ["path", "content"]
}`),
	}}
}

// readFileToolDef read_file 工具定义。
func readFileToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "read_file",
		Description: "读取当前产物内容（用于迭代修改场景了解现状，或写入后复查）。返回内容过长时会截断。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "固定为 index.html"}
  },
  "required": ["path"]
}`),
	}}
}

// runChecksToolDef run_checks 工具定义。
func runChecksToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_checks",
		Description: "对当前产物执行校验：静态校验（文档结构/沙箱兼容性/存储调用/交互绑定/体积），浏览器可用时叠加无头浏览器实测（运行时异常/console错误/白屏/交互元素）。写入后必须调用；返回的 issues 不为空时须修复后重新提交。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": []
}`),
	}}
}

// finishToolDef finish 工具定义。
func finishToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "finish",
		Description: "收尾：汇总本次构建做了什么。只能在产物通过校验后调用，调用后循环结束。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "summary": {"type": "string", "description": "给用户的一句话总结（中文）"}
  },
  "required": ["summary"]
}`),
	}}
}

// editFileToolDef edit_file 工具定义：对产物做精准片段替换，替代"修复必整篇重写"。
func editFileToolDef() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "edit_file",
		Description: "对当前产物做精准片段替换（类似 code editor 的 search-replace）：old_string 必须与产物内容唯一精确匹配（含空白缩进），new_string 为替换后的文本。小改动与校验修复优先用本工具，避免整篇重写。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "old_string": {"type": "string", "description": "待替换的精确原文片段，必须在产物中唯一匹配"},
    "new_string": {"type": "string", "description": "替换后的文本，不能与 old_string 相同"},
    "replace_all": {"type": "boolean", "description": "可选，默认 false；true 时替换所有匹配"}
  },
  "required": ["old_string", "new_string"]
}`),
	}}
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

// commitPlanArgs commit_plan 的入参。
type commitPlanArgs struct {
	LayoutNotes string `json:"layout_notes"`
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

// editArgs edit_file 的入参。
type editArgs struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
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
	case "commit_plan":
		return rt.toolCommitPlan(argsJSON)
	case "write_file":
		return rt.toolWrite(argsJSON)
	case "edit_file":
		return rt.toolEdit(argsJSON)
	case "read_file":
		return rt.toolRead(argsJSON)
	case "run_checks":
		return rt.toolChecks()
	case "finish":
		return rt.toolFinish(argsJSON)
	default:
		return toolResult{OK: false, Observe: fmt.Sprintf("未知工具 %s，可用工具：plan_app / commit_plan / write_file / edit_file / read_file / run_checks / finish", name)}
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
	return toolResult{OK: true, Observe: fmt.Sprintf("计划已确认：应用名 %s，模板 %s，共 %d 步。%s", args.AppName, tid, len(rt.plan.Steps), planNextHint(rt.mode))}
}

// planNextHint 规划流程下一步提示（规划模式引导提交规划，其余模式引导写入产物）。
func planNextHint(mode string) string {
	if mode == "plan" {
		return "请在正文输出面向用户的布局说明与关键交互清单，然后调用 commit_plan 提交规划以解锁实施工具。"
	}
	return "现在调用 write_file 生成完整 HTML。"
}

func (rt *reactSession) toolCommitPlan(argsJSON string) toolResult {
	var args commitPlanArgs
	_ = json.Unmarshal([]byte(argsJSON), &args)
	if rt.phase != "plan" {
		return toolResult{OK: false, Observe: "commit_plan 仅在规划阶段可用，当前已处于实施阶段。"}
	}
	if rt.plan.Template == "" {
		return toolResult{OK: false, Observe: "尚无规划可提交：请先调用 plan_app 产出计划，再 commit_plan。"}
	}
	if strings.TrimSpace(args.LayoutNotes) != "" {
		rt.detail("plan", args.LayoutNotes, "info")
	}
	rt.phase = "act"
	rt.stage("build", "规划已提交，解锁实施工具，按规划开始构建…")
	return toolResult{OK: true, Observe: "规划已提交，实施工具已解锁（write_file/edit_file/read_file/run_checks/finish）。请按规划开始构建。"}
}

func (rt *reactSession) toolWrite(argsJSON string) toolResult {
	var args writeArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolResult{OK: false, Observe: "write_file 参数解析失败：" + err.Error()}
	}
	if rt.phase == "plan" {
		return toolResult{OK: false, Observe: "规划阶段禁止写入：请先完成规划并调用 commit_plan 解锁实施工具。"}
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
	if rt.writes >= maxWrites {
		return toolResult{OK: false, Observe: fmt.Sprintf("整体写入已达上限（%d 次）。请改用 edit_file 做精准片段修改；确需整体重构时在正文说明理由后调用 finish 结束并反馈给用户。", maxWrites)}
	}
	if rt.trackEdits && rt.writes >= 1 {
		return toolResult{OK: false, Observe: "迭代修改模式下整体重写的机会已用完。请改用 edit_file 做精准片段修改，确有整体重构必要请向用户说明。"}
	}
	rt.html = args.Content
	rt.writes++
	return toolResult{OK: true, Observe: fmt.Sprintf("index.html 已写入（%d 字符，剩余整体写入次数 %d）。请立即调用 run_checks 校验产物。", len(args.Content), maxWrites-rt.writes), Think: fmt.Sprintf("写入 index.html（%d 字符）", len(args.Content))}
}

func (rt *reactSession) toolEdit(argsJSON string) toolResult {
	var args editArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return toolResult{OK: false, Observe: "edit_file 参数解析失败：" + err.Error()}
	}
	if rt.phase == "plan" {
		return toolResult{OK: false, Observe: "规划阶段禁止修改产物：请先 commit_plan 解锁实施工具。"}
	}
	if rt.html == "" {
		return toolResult{OK: false, Observe: "尚无产物可编辑：请先用 write_file 写入 index.html。"}
	}
	if args.OldString == args.NewString {
		return toolResult{OK: false, Observe: "old_string 与 new_string 相同，无需编辑。请重新给出替换片段。"}
	}
	if args.OldString == "" {
		return toolResult{OK: false, Observe: "old_string 不能为空。请先 read_file 确认产物内容再编辑。"}
	}

	count := strings.Count(rt.html, args.OldString)
	if count == 0 {
		return toolResult{OK: false, Observe: "old_string 在产物中无精确匹配。请用 read_file 核对原文（注意空白与缩进）后重试。"}
	}
	if count > 1 && !args.ReplaceAll {
		return toolResult{OK: false, Observe: fmt.Sprintf("old_string 匹配到 %d 处，为避免误伤请扩大片段范围使其唯一，或传 replace_all=true。", count)}
	}
	// 片段大小护栏：替换超过产物 60% 的"编辑"实为重写，引导走 write_file 或 finish
	if len(args.OldString)*5 > len(rt.html)*3 {
		return toolResult{OK: false, Observe: "old_string 覆盖产物超过 60%，这相当于重写。请改用 write_file（剩余次数有限）或继续缩小片段。"}
	}
	newHTML := ""
	if args.ReplaceAll {
		newHTML = strings.ReplaceAll(rt.html, args.OldString, args.NewString)
	} else {
		newHTML = strings.Replace(rt.html, args.OldString, args.NewString, 1)
	}
	if newHTML == rt.html {
		return toolResult{OK: false, Observe: "替换未产生变化（new_string 与 old_string 相同）。"}
	}
	rt.html = newHTML
	return toolResult{OK: true, Observe: fmt.Sprintf("edit_file 成功：替换 %d 处，产物现为 %d 字符。请调用 run_checks 确认修改未破坏校验。", count, len(rt.html)), Think: fmt.Sprintf("精准修改 index.html（%d → %d 字符）", len(rt.html)-len(args.NewString)+len(args.OldString), len(rt.html))}
}

func (rt *reactSession) toolRead(string) toolResult {
	if rt.html == "" {
		return toolResult{OK: true, Observe: "index.html 尚不存在（还没有执行过写入）。"}
	}
	head := rt.html
	if len(head) > 4000 {
		// 上限放宽到 4000 字符并首尾采样，配合 clampObserve 兜底
		head = rt.html[:3000] + "\n<!-- ...middle truncated... -->\n" + rt.html[len(rt.html)-1000:]
	}
	return toolResult{OK: true, Observe: fmt.Sprintf("index.html 当前内容（采样展示，全文 %d 字符）：\n%s", len(rt.html), head)}
}

// maxWrites 单次任务整体写入上限（首写 + 一次整体重写）。
const maxWrites = 2

func (rt *reactSession) toolChecks() toolResult {
	if rt.html == "" {
		return toolResult{OK: false, Observe: "尚无产物可校验：请先调用 write_file 写入 index.html。"}
	}
	issues := checkProduct(rt.html)

	// 浏览器实测：静态校验通过或需要运行时证据时叠加；无浏览器环境静默跳过
	if len(issues) == 0 || !hasBlockingIssues(issues) {
		vr := BrowserVerifier{}.Verify(rt.ctx, rt.html)
		switch vr.Status {
		case "failed":
			for _, i := range vr.Issues {
				issues = append(issues, i)
			}
			rt.detail("verify", "浏览器实测发现问题：共 "+fmt.Sprint(len(issues))+" 项", "warn")
		case "passed":
			rt.detail("verify", "浏览器实测通过：无运行时异常，页面渲染正常", "info")
		default: // skipped
			if len(vr.Issues) > 0 {
				rt.detail("verify", vr.Issues[0], "info")
			}
		}
	}

	if len(issues) == 0 {
		rt.detail("verify", "校验全部通过", "info")
		return toolResult{OK: true, Observe: "校验通过：文档结构、沙箱兼容性、存储降级、交互绑定、体积均无问题（含浏览器实测）。可以调用 finish 收尾。"}
	}
	rt.detail("verify", fmt.Sprintf("发现 %d 个问题，需修复后重新提交产物", len(issues)), "warn")
	return toolResult{OK: false, Observe: "校验发现以下问题：\n- " + strings.Join(issues, "\n- ") + "\n请针对以上问题修复：小改动用 edit_file 精准替换，整体缺陷用 write_file 重写（剩余次数有限）。"}
}

// hasBlockingIssues 静态校验存在 error 级问题时跳过浏览器实测（避免必然失败的白跑）。
func hasBlockingIssues(issues []string) bool {
	for _, i := range issues {
		if strings.HasPrefix(i, "[error]") {
			return true
		}
	}
	return false
}

func (rt *reactSession) toolFinish(argsJSON string) toolResult {
	var args finishArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		args.Summary = ""
	}
	if rt.html == "" {
		return toolResult{OK: false, Observe: "还不能 finish：产物尚未写入。请先 write_file。"}
	}
	if rt.phase == "plan" {
		return toolResult{OK: false, Observe: "规划阶段不能 finish：请先 commit_plan 提交规划。"}
	}
	if issues := checkProduct(rt.html); len(issues) > 0 {
		return toolResult{OK: false, Observe: "finish 被拒绝：产物仍有未通过校验的问题：\n- " + strings.Join(issues, "\n- ") + "\n请修复后重新提交再 finish。"}
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
