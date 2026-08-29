package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"atomix-demo/server/internal/llm"
)

// researchBrief 需求研究子 Agent：以独立上下文对需求做要点拆解，产出简报供主循环注入。
// 子 Agent 与主 ReAct 循环上下文完全隔离，只通过结构化 JSON 交换结论（子 agent 委派模式的简化实现）。
// LLM 失败或输出异常时降级为规则拆解，保证主流程不中断。
func (a *Agent) researchBrief(ctx context.Context, brief string) string {
	if a.UseMock {
		return mockResearch(brief)
	}
	prompt := fmt.Sprintf(`你是 Atomix 平台的需求研究子 Agent。主 Agent 将根据你的简报构建一个单文件网页小应用，需求如下：
"""%s"""
请输出一个 JSON 对象（不要 markdown 代码块）：
{"brief":"研究简报正文，300 字以内，纯文本"}
简报正文依次包含：
1. 目标用户与使用场景（1 句）
2. 核心功能清单（3-5 条，具体可实施）
3. 信息结构（页面分区与数据实体，1-2 句）
4. 边界情况与注意点（空状态、localStorage 持久化、禁止 document.cookie 等）`, brief)
	resp, err := a.LLM.ChatJSON(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是需求研究子 Agent，只输出 JSON。"},
		{Role: "user", Content: prompt},
	})
	if err == nil {
		var r struct {
			Brief string `json:"brief"`
		}
		if json.Unmarshal([]byte(resp), &r) == nil && strings.TrimSpace(r.Brief) != "" {
			return strings.TrimSpace(r.Brief)
		}
	}
	return mockResearch(brief)
}

// mockResearch 规则版拆解（演示模式 / 子 Agent 失败时的降级路径）。
func mockResearch(brief string) string {
	tid := Match(brief)
	info, _ := Get(tid)
	var sb strings.Builder
	sb.WriteString("1. 目标用户与场景：需要一个轻量单页工具来记录与管理信息，开箱即用、刷新不丢数据。\n")
	sb.WriteString("2. 核心功能清单：\n")
	for _, f := range info.Features {
		sb.WriteString("   - " + f + "\n")
	}
	sb.WriteString("3. 信息结构：" + strings.Join(info.Assets, "；") + "；数据实体存 localStorage，键名带应用前缀避免冲突。\n")
	sb.WriteString("4. 边界与注意点：空状态需引导文案；localStorage 不可用时平台会注入垫片自动降级；禁止 document.cookie（沙箱会抛 SecurityError）；交互必须真实绑定事件。")
	return sb.String()
}
