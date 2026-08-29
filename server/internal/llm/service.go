// Package llm 定义模型服务接口与 DeepSeek 实现。
package llm

import (
	"context"
	"encoding/json"
)

// ChatMessage 一条对话消息。
// 携带 ToolCalls 时为 assistant 的工具调用意图；Role=tool 时须带 ToolCallID。
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolFunction 工具的函数定义（OpenAI 兼容格式）。
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool 传给模型的工具定义。
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolCall 模型返回的工具调用意图。
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolCallResponse 一次带工具定义的模型响应。
type ToolCallResponse struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// Service 抽象模型调用能力，便于在真实 API 与演示模式间切换。
type Service interface {
	ChatJSON(ctx context.Context, messages []ChatMessage) (string, error)
	ChatHTML(ctx context.Context, messages []ChatMessage) (string, error)
	ChatWithTools(ctx context.Context, messages []ChatMessage, tools []Tool, temperature float64, maxTokens int) (*ToolCallResponse, error)
}
