// Package llm 定义模型服务接口与 DeepSeek 实现。
package llm

import (
	"context"
	"encoding/json"
)

// ChatMessage 一条对话消息。
// 携带 ToolCalls 时为 assistant 的工具调用意图；Role=tool 时须带 ToolCallID。
// ContentParts 非空时按 OpenAI 多模态格式序列化（文本 + image_url），供 vision 模型识图。
type ChatMessage struct {
	Role         string        `json:"role"`
	Content      string        `json:"content,omitempty"`
	ContentParts []ContentPart `json:"-"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
}

// ContentPart 多模态消息片段。
type ContentPart struct {
	Type     string `json:"type"` // text | image_url
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// MarshalJSON 多模态序列化：ContentParts 非空时输出 parts 数组，否则输出纯文本。
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type alias struct {
		Role       string     `json:"role"`
		Content    any        `json:"content,omitempty"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	a := alias{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	if len(m.ContentParts) > 0 {
		parts := make([]any, 0, len(m.ContentParts))
		for _, p := range m.ContentParts {
			if p.Type == "image_url" {
				parts = append(parts, struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				}{Type: "image_url", ImageURL: struct {
					URL string `json:"url"`
				}{URL: p.ImageURL.URL}})
			} else {
				parts = append(parts, struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}{Type: "text", Text: p.Text})
			}
		}
		a.Content = parts
	} else {
		a.Content = m.Content
	}
	return json.Marshal(a)
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
