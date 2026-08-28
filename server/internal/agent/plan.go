package agent

import (
	"fmt"
	"strings"
)

// PlanStep 计划中的一步。
type PlanStep struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

// PlanResult 生成计划。
type PlanResult struct {
	AppName    string     `json:"appName"`
	Template   string     `json:"template"`
	Reason     string     `json:"reason"`
	Steps      []PlanStep `json:"steps"`
	Highlights []string   `json:"highlights"`
}

// TplInfo 模板元信息。
type TplInfo struct {
	ID          string
	Name        string
	Reason      string
	Assets      []string
	Build       []string
	Scripts     []string
	Features    []string
	Highlighter []string
}

// All 返回全部内置模板。
func All() []TplInfo {
	return []TplInfo{todoTpl(), notesTpl(), kanbanTpl()}
}

// Get 按 ID 获取模板。
func Get(id string) (TplInfo, bool) {
	for _, t := range All() {
		if t.ID == id {
			return t, true
		}
	}
	return TplInfo{}, false
}

// DefaultName 模板默认应用名。
func DefaultName(id string) string {
	if t, ok := Get(id); ok {
		return t.Name
	}
	return "我的应用"
}

// Match 根据自然语言需求推断最合适的模板 ID。
func Match(brief string) string {
	b := strings.ToLower(brief)
	type kv struct {
		words []string
		id    string
	}
	rules := []kv{
		{[]string{"todo", "待办", "任务", "清单", "checklist"}, "todo"},
		{[]string{"note", "便签", "笔记", "memo", "备忘"}, "notes"},
		{[]string{"kanban", "看板", "board", "进展"}, "kanban"},
	}
	for _, r := range rules {
		for _, w := range r.words {
			if strings.Contains(b, w) {
				return r.id
			}
		}
	}
	return "todo"
}

func todoTpl() TplInfo {
	return TplInfo{
		ID:     "todo",
		Name:   "极简待办清单",
		Reason: "需求围绕任务管理与完成追踪，待办清单模板最贴合",
		Assets: []string{"任务栏 + 过滤器 + 统计条布局"},
		Build:  []string{"任务模型（名称/完成态/优先级）", "localStorage 任务持久化"},
		Scripts: []string{
			"任务增删改查与勾选完成",
			"全部/未完成/已完成过滤",
			"完成率进度条",
		},
		Features:    []string{"任务增删与勾选", "状态过滤", "进度统计"},
		Highlighter: []string{"一键清空已完成", "优先级标记"},
	}
}

func notesTpl() TplInfo {
	return TplInfo{
		ID:     "notes",
		Name:   "彩色便签墙",
		Reason: "需求偏重碎片信息记录与浏览，便签墙模板最贴合",
		Assets: []string{"便签墙瀑布流布局", "六色随机便签主题"},
		Build:  []string{"便签模型（标题/内容/颜色/置顶）", "localStorage 分表持久化"},
		Scripts: []string{
			"新建/编辑/删除便签",
			"置顶排序与关键词搜索",
			"字数统计",
		},
		Features:    []string{"便签增删改", "置顶与搜索", "彩色主题"},
		Highlighter: []string{"瀑布流卡片墙", "字数实时统计"},
	}
}

func kanbanTpl() TplInfo {
	return TplInfo{
		ID:     "kanban",
		Name:   "轻量项目看板",
		Reason: "需求围绕阶段化任务流转，三列看板模板最贴合",
		Assets: []string{"三列看板（待办/进行中/已完成）", "列头计数与渐变页头"},
		Build:  []string{"卡片模型（标题/描述/列归属）", "看板列与卡片持久化"},
		Scripts: []string{
			"新建/删除卡片",
			"卡片跨列移动",
			"统计条进度可视化",
		},
		Features:    []string{"卡片增删", "跨列移动", "进度统计"},
		Highlighter: []string{"列头实时计数", "进度可视化"},
	}
}

// mockPlan 演示模式下的计划。
func mockPlan(brief string) PlanResult {
	tid := Match(brief)
	info, _ := Get(tid)
	return PlanResult{
		AppName:  info.Name,
		Template: tid,
		Reason:   info.Reason,
		Steps:    stepsFor(info),
		Highlights: append(
			[]string{"localStorage 数据持久化", "沙箱 iframe 实时预览"},
			info.Highlighter...,
		),
	}
}

// fallbackPlan LLM 输出异常时的兜底计划。
func fallbackPlan(brief string) PlanResult {
	p := mockPlan(brief)
	p.Reason = "模型输出解析失败，使用规则引擎兜底规划"
	return p
}

func stepsFor(info TplInfo) []PlanStep {
	steps := []PlanStep{}
	id := 1
	for _, a := range info.Assets {
		steps = append(steps, PlanStep{ID: id, Title: a, Kind: "assets"})
		id++
	}
	for _, b := range info.Build {
		steps = append(steps, PlanStep{ID: id, Title: b, Kind: "build"})
		id++
	}
	for _, s := range info.Scripts {
		steps = append(steps, PlanStep{ID: id, Title: s, Kind: "scripts"})
		id++
	}
	return steps
}

// fillSteps 补全计划中的步骤列表。
func fillSteps(plan *PlanResult) {
	info, ok := Get(plan.Template)
	if !ok {
		return
	}
	if plan.Reason == "" {
		plan.Reason = info.Reason
	}
	if len(plan.Steps) == 0 {
		plan.Steps = stepsFor(info)
	}
	if len(plan.Highlights) == 0 {
		plan.Highlights = append([]string{"localStorage 数据持久化"}, info.Highlighter...)
	}
}

// StageEvents 为指定阶段生成标准事件序列。
func StageEvents(stage string, plan PlanResult, brief string, useMock bool) []struct {
	Stage   string
	Message string
	Level   string
} {
	out := []struct {
		Stage   string
		Message string
		Level   string
	}{}
	add := func(s, m, l string) {
		out = append(out, struct {
			Stage   string
			Message string
			Level   string
		}{s, m, l})
	}
	switch stage {
	case "plan":
		add("plan", fmt.Sprintf("模板选定：%s", plan.Template), "info")
	case "build":
		add("build", fmt.Sprintf("目标模板：%s（%s）", plan.Template, plan.AppName), "info")
		if useMock {
			add("build", "演示模式：使用内置模板生成产物", "info")
		} else {
			add("build", "调用 DeepSeek 生成单文件应用代码", "info")
		}
	case "verify":
		add("verify", "产物校验通过，写入数据库", "info")
	case "done":
		add("done", "流水线结束，应用已可预览", "info")
	}
	return out
}
