package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"atomix-demo/server/internal/llm"
)

// IntentResult 意图识别结果。
// Intent 取值：chat（闲聊）、clarify（需求模糊需澄清）、build（明确构建需求）。
type IntentResult struct {
	Intent string
	Reply  string // chat / clarify 时的回复文本
	Brief  string // build 时的规范化需求描述
}

// buildVerbRe 常见构建动词，作为 LLM 失败时的启发式兜底信号。
var buildVerbRe = regexp.MustCompile(`(做一个|做个|生成|创建|开发|写一个|写个|帮我做|来一个|设计|实现|搭建|制作)`)

// concreteAppRe 常见具体应用名词：出现即认为需求已可构建。
var concreteAppRe = regexp.MustCompile("(番茄钟|计时器|倒计时|清单|看板|笔记|便签|记账|账本|日历|日程|待办|游戏|计算器|画板|绘图|白板|音乐|播放器|天气|时钟|表单|问卷|投票|聊天|论坛|博客|商城|购物|词典|翻译|打卡|习惯|阅读|书签|收藏|相册|图库|编辑器|转换器|生成器|爬虫|贪吃蛇|扫雷|俄罗斯方块|五子棋)")

// refineIntent 确定性兜底：LLM 判为 chat 但消息含构建动词时强制纠正，
// 含具体应用名词直接 build，否则降级为 clarify 追问。
func refineIntent(text string, r IntentResult) IntentResult {
	if r.Intent != "chat" || !buildVerbRe.MatchString(text) {
		return r
	}
	if concreteAppRe.MatchString(text) {
		return IntentResult{Intent: "build", Brief: text}
	}
	return IntentResult{Intent: "clarify", Reply: "好的！你想做哪种类型的应用？比如待办清单、番茄钟、记账本，说说想实现的具体功能就行。"}
}

// ClassifyIntent 识别用户消息意图，只做分类与回复，不触发构建流程。
// 演示模式（UseMock）或 LLM 失败时退化为关键词启发式。
func (a *Agent) ClassifyIntent(ctx context.Context, text string) IntentResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return IntentResult{Intent: "chat", Reply: "请告诉我你想聊点什么，或者想构建一个什么样的应用。"}
	}
	if a.UseMock {
		return heuristicClassify(text)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	prompt := fmt.Sprintf(`你是 Atomix 平台的对话入口 Agent。Atomix 是一个"通过对话生成网页小应用"的 AI 构建平台，用户输入一句话需求后，平台会自动完成规划、编码、自检并交付一个单文件网页应用。
请判断用户最新一条消息的意图，只输出一个 JSON 对象（不要 markdown 代码块），格式：
{"intent":"chat|clarify|build","reply":"...","brief":"..."}

判定规则（严格遵守）：
- chat：仅当消息与构建应用完全无关时才判 chat（打招呼、闲聊、问你是谁/你能做什么）。reply 用友好简洁的中文回复（1-3 句），介绍自己是 Atomix 并自然引导用户描述想做的应用。
- clarify：用户表达了构建意愿，但连"做什么应用"都说不清（如"帮我做个网站""做个好玩的"）。reply 提出一个最关键的问题帮助澄清需求（做什么功能、给谁用）。
- build：只要用户给出了具体的应用类型或功能点（哪怕只有一句话、一个功能），就必须判 build，绝不能判 chat。brief 用一句话规范化重述需求（保留用户原意，补全省略的主语，不要添加用户没提的功能）。

判定示例：
- "你好 你是谁" → {"intent":"chat","reply":"...","brief":""}
- "你能做什么" → {"intent":"chat","reply":"...","brief":""}
- "帮我做个网站" → {"intent":"clarify","reply":"...","brief":""}
- "做个好玩的" → {"intent":"clarify","reply":"...","brief":""}
- "做一个番茄钟" → {"intent":"build","brief":"做一个番茄钟计时器应用，包含倒计时与暂停功能"}
- "做个记账本" → {"intent":"build","brief":"做一个记账本应用，可记录收支条目并查看汇总"}
- "做一个番茄钟计时器，25分钟工作加5分钟休息" → {"intent":"build","brief":"做一个番茄钟计时器，25分钟工作加5分钟休息"}

用户消息："""%s"""`, text)
	resp, err := a.LLM.ChatJSON(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是 Atomix 平台的意图识别 Agent，只输出 JSON。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return heuristicClassify(text)
	}
	var r IntentResult
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		return heuristicClassify(text)
	}
	switch r.Intent {
	case "chat", "clarify":
		if strings.TrimSpace(r.Reply) == "" {
			return heuristicClassify(text)
		}
		return refineIntent(text, IntentResult{Intent: r.Intent, Reply: strings.TrimSpace(r.Reply)})
	case "build":
		brief := strings.TrimSpace(r.Brief)
		if brief == "" {
			brief = text
		}
		return IntentResult{Intent: "build", Brief: brief}
	default:
		return heuristicClassify(text)
	}
}

// heuristicClassify 关键词启发式：仅在演示模式或 LLM 调用失败时兜底。
func heuristicClassify(text string) IntentResult {
	runes := len([]rune(text))
	if buildVerbRe.MatchString(text) && runes >= 6 {
		return IntentResult{Intent: "build", Brief: text}
	}
	if runes > 20 {
		// 超过 20 字的长描述大概率是需求
		return IntentResult{Intent: "build", Brief: text}
	}
	return IntentResult{Intent: "chat", Reply: "你好！我是 Atomix，可以通过对话把你的想法变成可用的网页小应用。告诉我你想做一个什么应用吧，比如：做一个番茄钟计时器。"}
}
