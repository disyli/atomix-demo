// Package llm 定义模型服务接口与 DeepSeek 实现。
package llm

import "context"

// ChatMessage 一条对话消息。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Service 抽象模型调用能力，便于在真实 API 与演示模式间切换。
type Service interface {
	ChatJSON(ctx context.Context, messages []ChatMessage) (string, error)
	ChatHTML(ctx context.Context, messages []ChatMessage) (string, error)
}
