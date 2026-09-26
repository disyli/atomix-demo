package agent

import (
	"strings"
	"testing"

	"atomix-demo/server/internal/llm"
)

// TestCompressBuildModeKeepsPairing 回归测试：build 模式消息序列为
// [system, assistant(tool_calls), tool, ...]，头部没有初始 user 消息（需求并入 system）。
// 历史上 maybeCompress 硬编码保留 msgs[0]/msgs[1]，会把首条 assistant(tool_calls)
// 留在头部、其配对 tool 落入压缩区被丢弃 → 孤儿 tool_calls → DeepSeek 400
// "insufficient tool messages following tool_calls message"。压缩后必须保证：
// 1) 头部不含孤儿 tool_calls（每条带 tool_calls 的 assistant 后紧跟配对 tool）；
// 2) 消息总数明显减少；3) 保留 system。
func TestCompressBuildModeKeepsPairing(t *testing.T) {
	// 填充长内容让总字符超预算触发压缩
	filler := strings.Repeat("历史工具观察回喂数据。", 800)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "系统提示词，包含用户需求：做一个极简计算器"},
	}
	// 构造 20 轮 [assistant(tool_calls), tool] 循环，模拟 ReAct 多轮交互
	for i := 0; i < 20; i++ {
		call := mkCall("call_"+itoa(i), "write_file", `{"path":"index.html","content":"`+filler+`"}`)
		msgs = append(msgs, llm.ChatMessage{
			Role:      "assistant",
			Content:   "第" + itoa(i) + "轮思考：继续推进构建",
			ToolCalls: []llm.ToolCall{call},
		})
		msgs = append(msgs, llm.ChatMessage{
			Role: "tool", ToolCallID: "call_" + itoa(i),
			Content: "写入成功（第" + itoa(i) + "轮观察）：" + filler[:600],
		})
	}

	b := newContextBudget()
	if !b.maybeCompress(&msgs) {
		t.Fatal("超预算消息序列应触发压缩")
	}

	// system 必须保留
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("压缩后首条消息应为 system，实际 role=%q", firstRole(msgs))
	}
	// 压缩必须确实缩减了历史
	if len(msgs) >= 40 {
		t.Fatalf("压缩后仍有 %d 条消息，未起到压缩作用", len(msgs))
	}
	// 全序列配对校验：每条 tool 消息必须紧跟其 assistant(tool_calls)；反向每条
	// assistant 的每个 toolCall 都能在其后找到配对 tool
	assertPairing(t, msgs)
}

// TestCompressRefineModeKeepsInitialUser 回归测试：refine 模式消息序列为
// [system, user(修改指令), assistant(tool_calls), tool, ...]，头部含初始 user。
// 压缩后必须保留初始 user 消息，且配对不破坏。
func TestCompressRefineModeKeepsInitialUser(t *testing.T) {
	filler := strings.Repeat("迭代修改的工具观察。", 800)
	msgs := []llm.ChatMessage{
		{Role: "system", Content: "系统提示词：迭代修改模式"},
		{Role: "user", Content: "增加历史记录功能，显示最近计算表达式"},
	}
	for i := 0; i < 18; i++ {
		call := mkCall("c"+itoa(i), "edit_file", `{"old":"`+filler+`","new":"x"}`)
		msgs = append(msgs, llm.ChatMessage{
			Role:      "assistant",
			Content:   "第" + itoa(i) + "轮思考",
			ToolCalls: []llm.ToolCall{call},
		})
		msgs = append(msgs, llm.ChatMessage{
			Role: "tool", ToolCallID: "c" + itoa(i), Content: "观察：" + filler[:600],
		})
	}

	b := newContextBudget()
	if !b.maybeCompress(&msgs) {
		t.Fatal("超预算消息序列应触发压缩")
	}
	// 初始 user 消息必须保留在头部
	found := false
	for i, m := range msgs {
		if i == 0 {
			continue
		}
		if m.Role == "user" && strings.Contains(m.Content, "历史记录") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("压缩后初始 user 消息（修改指令）丢失")
	}
	assertPairing(t, msgs)
}

// assertPairing 全序列校验 assistant(tool_calls)/tool 配对完整性：
// 每个工具调用的 ID 都必须在其后相邻位置由一条 tool 消息应答。
func assertPairing(t *testing.T, msgs []llm.ChatMessage) {
	t.Helper()
	answered := map[string]bool{}
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				ok := false
				// 配对 tool 允许出现在 assistant 之后连续的 tool 消息区间内
				for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
					if msgs[j].ToolCallID == tc.ID {
						ok = true
						answered[tc.ID] = true
						break
					}
				}
				if !ok {
					t.Fatalf("孤儿 tool_calls：assistant 消息 #%d 的调用 %s 无配对 tool 消息", i, tc.ID)
				}
			}
		}
		if m.Role == "tool" && !answered[m.ToolCallID] {
			t.Fatalf("游离 tool 消息：#%d (%s) 没有对应的 assistant(tool_calls)", i, m.ToolCallID)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func firstRole(msgs []llm.ChatMessage) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0].Role
}

// mkCall 构造一条工具调用（ToolCall.Function 为匿名结构体，无法用字面量嵌套初始化）。
func mkCall(id, name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.ID = id
	c.Type = "function"
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}
