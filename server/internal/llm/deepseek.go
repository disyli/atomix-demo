package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Options DeepSeek 连接配置。
type Options struct {
	APIKey  string
	BaseURL string
	Model   string
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
	Tools       []Tool        `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// DeepSeekService 真实模型服务实现。
type DeepSeekService struct {
	opts Options
	hc   *http.Client
}

// NewDeepSeek 创建 DeepSeek 服务。
func NewDeepSeek(opts Options) *DeepSeekService {
	return &DeepSeekService{opts: opts, hc: &http.Client{Timeout: 180 * time.Second}}
}

func (s *DeepSeekService) callRaw(ctx context.Context, req chatRequest) (*chatResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.opts.APIKey)

	resp, err := s.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek http %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("deepseek api: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("deepseek empty choices")
	}
	return &out, nil
}

func (s *DeepSeekService) call(ctx context.Context, messages []ChatMessage, temperature float64, maxTokens int) (string, error) {
	out, err := s.callRaw(ctx, chatRequest{
		Model:       s.opts.Model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	})
	if err != nil {
		return "", err
	}
	return out.Choices[0].Message.Content, nil
}

// ChatJSON 请求模型输出一个 JSON 对象。
func (s *DeepSeekService) ChatJSON(ctx context.Context, messages []ChatMessage) (string, error) {
	text, err := s.call(ctx, messages, 0.3, 2048)
	if err != nil {
		return "", err
	}
	return extractJSON(text), nil
}

// ChatHTML 请求模型输出一段完整的 HTML 应用。
func (s *DeepSeekService) ChatHTML(ctx context.Context, messages []ChatMessage) (string, error) {
	text, err := s.call(ctx, messages, 0.7, 8192)
	if err != nil {
		return "", err
	}
	return extractHTML(text), nil
}

// ChatWithTools 执行一次带工具定义的对话，返回内容与工具调用意图（ReAct 循环的核心调用）。
func (s *DeepSeekService) ChatWithTools(ctx context.Context, messages []ChatMessage, tools []Tool, temperature float64, maxTokens int) (*ToolCallResponse, error) {
	out, err := s.callRaw(ctx, chatRequest{
		Model:       s.opts.Model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Tools:       tools,
	})
	if err != nil {
		return nil, err
	}
	choice := out.Choices[0]
	return &ToolCallResponse{
		Content:      choice.Message.Content,
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// extractJSON 从模型回复中剥离出 JSON 主体。
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		if i := strings.LastIndex(text, "```"); i >= 0 {
			text = text[:i]
		}
	}
	return strings.TrimSpace(text)
}

// extractHTML 从模型回复中剥离出 HTML 文档主体。
func extractHTML(text string) string {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "<!doctype html")
	if idx < 0 {
		idx = strings.Index(lower, "<html")
	}
	if idx < 0 {
		return text
	}
	text = text[idx:]
	lower = strings.ToLower(text)
	if i := strings.LastIndex(lower, "</html>"); i >= 0 {
		text = text[:i+len("</html>")]
	}
	return text
}
