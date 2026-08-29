package agent

import (
	"fmt"

	"atomix-demo/server/internal/llm"
)

// 上下文预算常量（按字符估算，1 token ≈ 2-3 字符中文混合文本，阈值取保守值）。
const (
	// compressThreshold 消息总字符超过该值触发压缩（≈ 40k token 窗口下留足余量）。
	compressThreshold = 60000
	// keepRecent 每次压缩至少保留最近 N 条消息（含配对的 tool 消息）。
	keepRecent = 6
	// observeTruncate 单条工具观察回喂给模型的截断上限，防止单条超长污染上下文。
	observeTruncate = 3000
)

// contextBudget 单次 ReAct 会话的上下文预算。
type contextBudget struct {
	charBudget int
}

func newContextBudget() *contextBudget {
	return &contextBudget{charBudget: compressThreshold}
}

// maybeCompress 检查消息总字符量，超预算时压缩中间历史为一条状态摘要。
// 压缩结构（保证 tool_calls/tool 配对完整）：
// [system] + [初始 user（需求+附件）] + [压缩摘要 user] + [最近 keepRecent 条消息]
// 返回是否发生了压缩（用于事件留痕）。
func (b *contextBudget) maybeCompress(messages *[]llm.ChatMessage) bool {
	msgs := *messages
	total := 0
	for _, m := range msgs {
		total += len(m.Content) + len(m.ToolCallID)*8
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments)
		}
	}
	if total <= b.charBudget || len(msgs) <= keepRecent+2 {
		return false
	}

	// 定位配对边界：从「最近 keepRecent 条」往前找，确保不把 assistant(tool_calls)
	// 与其配对的 tool 消息拆开。切点必须落在 user 或无 tool_calls 的 assistant 上。
	cut := len(msgs) - keepRecent
	for cut > 2 && !boundaryOK(msgs, cut) {
		cut--
	}
	if cut <= 2 {
		return false
	}

	// 收集压缩区信息：产物规模、已执行的关键动作、最近一次校验状态
	var planText, lastObserve string
	writes := 0
	for _, m := range msgs[2:cut] {
		for _, tc := range m.ToolCalls {
			switch tc.Function.Name {
			case "plan_app":
				planText = tc.Function.Arguments
			case "write_file", "edit_file":
				writes++
			case "finish":
				lastObserve = "已完成 finish"
			}
		}
		if m.Role == "tool" && len(m.Content) > 80 {
			// 保留最近一条有信息量的工具观察
			if len(m.Content) < 2000 {
				lastObserve = truncateText(m.Content, 300)
			}
		}
	}
	summary := fmt.Sprintf(
		"[上下文压缩] 之前的工具交互已汇总：计划参数 %s；累计写入/编辑 %d 次；最近观察：%s。产物内容保存在平台侧，用 read_file 可重新获取当前产物全文。",
		truncateText(planText, 200), writes, truncateText(lastObserve, 300),
	)

	compressed := []llm.ChatMessage{msgs[0], msgs[1], {Role: "user", Content: summary}}
	compressed = append(compressed, msgs[cut:]...)
	*messages = compressed
	return true
}

// boundaryOK 判断 msgs[cut:] 开头是否是安全的切分边界（不会拆开 tool_calls/tool 配对）。
func boundaryOK(msgs []llm.ChatMessage, cut int) bool {
	if cut >= len(msgs) {
		return false
	}
	switch msgs[cut].Role {
	case "tool":
		return false // tool 消息必须紧跟其 assistant(tool_calls)，不能作切点
	case "assistant":
		return len(msgs[cut].ToolCalls) == 0
	default:
		return true
	}
}

// clampObserve 控制单条观察回喂长度。
func clampObserve(s string) string {
	if len(s) <= observeTruncate*3 { // 中英混合按字节算，放宽 3 倍
		return s
	}
	return s[:observeTruncate*3] + "\n…（观察内容过长已截断，可用 read_file 查看产物全文）"
}
