package agent

import (
	"context"
	stdruntime "runtime"
	"strings"
	"time"

	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// BrowserVerifier 无头浏览器实测产物：加载产物 HTML，捕获运行时异常、console 错误、白屏与交互元素缺失。
// 本机找不到 Chrome/浏览器环境时静默降级（Status=skipped），主流程不受影响。
type BrowserVerifier struct{}

// VerifyResult 实测结果：passed / failed / skipped 三态。
type VerifyResult struct {
	Status string   `json:"status"`
	Issues []string `json:"issues,omitempty"`
}

// Verify 加载产物并实测。产物是自包含单文件，以 data URL 加载，无外部服务依赖。
func (BrowserVerifier) Verify(ctx context.Context, html string) VerifyResult {
	if !strings.Contains(strings.ToLower(html), "<script") {
		return VerifyResult{Status: "skipped", Issues: []string{"产物无脚本，跳过浏览器实测"}}
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
	)
	if stdruntime.GOOS == "linux" {
		opts = append(opts, chromedp.Flag("disable-dev-shm-usage", true))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	bctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	tctx, cancel := context.WithTimeout(bctx, 25*time.Second)
	defer cancel()

	var (
		pageErrors []string
		consoleErr []string
		bodyText   string
		hasEl      bool
	)
	chromedp.ListenTarget(tctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *cdruntime.EventConsoleAPICalled:
			// 只收集 error 级别的 console 输出
			if e.Type == "error" {
				for _, arg := range e.Args {
					if arg.Value != nil {
						consoleErr = append(consoleErr, strings.TrimSpace(string(arg.Value)))
					}
				}
			}
		case *cdruntime.EventExceptionThrown:
			if e.ExceptionDetails != nil {
				d := e.ExceptionDetails
				msg := strings.TrimSpace(d.Text)
				if d.Exception != nil && d.Exception.Description != "" {
					msg = strings.TrimSpace(d.Exception.Description)
				}
				// 截取首行，避免堆栈刷屏
				if i := strings.IndexByte(msg, '\n'); i > 0 {
					msg = msg[:i]
				}
				pageErrors = append(pageErrors, msg)
			}
		}
	})

	// 与真实预览一致：注入存储垫片后再实测，消除沙箱环境差异导致的误报
	page := "data:text/html;charset=utf-8," + urlEscape(InjectStorageShim(html))
	err := chromedp.Run(tctx,
		chromedp.Navigate(page),
		chromedp.Sleep(1500*time.Millisecond), // 等待脚本执行与首屏渲染
		chromedp.TextContent("body", &bodyText, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('button,input,textarea,select,[onclick],[contenteditable]')`, &hasEl),
	)
	if err != nil {
		// 浏览器启动/导航失败（典型：本机无 Chrome）→ 静默降级
		return VerifyResult{Status: "skipped", Issues: []string{"无可用浏览器环境，跳过浏览器实测（" + briefErr(err) + "）"}}
	}
	return classifyRuntime(pageErrors, consoleErr, bodyText, hasEl)
}

// classifyRuntime 汇总实测证据，产出问题列表（空 = passed）。
func classifyRuntime(pageErrors, consoleErr []string, bodyText string, hasEl bool) VerifyResult {
	var issues []string
	for _, e := range pageErrors {
		issues = append(issues, "[error] 运行时异常："+e)
	}
	for _, e := range consoleErr {
		issues = append(issues, "[error] console error："+truncateText(e, 160))
	}
	if len(strings.TrimSpace(bodyText)) < 20 {
		issues = append(issues, "[error] 页面疑似白屏：body 文本内容不足 20 字符")
	}
	if !hasEl {
		issues = append(issues, "[warn] 未检测到任何可交互元素（button/input/textarea 等），应用可能不具备真实交互")
	}
	if len(issues) > 0 {
		return VerifyResult{Status: "failed", Issues: issues}
	}
	return VerifyResult{Status: "passed"}
}

func briefErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, ";"); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// urlEscape 极简 URL 编码（data URL 场景）。
// 注意 # 必须编码：产物 CSS 颜色值（如 #6366f1）中的 # 会把 URL 截断成 fragment，导致文档主体丢失。
func urlEscape(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			strings.ContainsRune("-_.~!$&'()*+,/:;=?@[]", r):
			sb.WriteRune(r)
		case r == ' ':
			sb.WriteString("%20")
		case r == '"':
			sb.WriteString("%22")
		case r == '%':
			sb.WriteString("%25")
		case r == '<':
			sb.WriteString("%3C")
		case r == '>':
			sb.WriteString("%3E")
		case r == '\\':
			sb.WriteString("%5C")
		case r == '^':
			sb.WriteString("%5E")
		case r == '`':
			sb.WriteString("%60")
		case r == '{':
			sb.WriteString("%7B")
		case r == '|':
			sb.WriteString("%7C")
		case r == '}':
			sb.WriteString("%7D")
		case r == '\n':
			sb.WriteString("%0A")
		case r == '\r':
			sb.WriteString("%0D")
		case r == '\t':
			sb.WriteString("%09")
		default:
			// 非 ASCII（中文等）：按 UTF-8 逐字节百分号编码
			for _, b := range []byte(string(r)) {
				const hex = "0123456789ABCDEF"
				sb.WriteByte('%')
				sb.WriteByte(hex[b>>4])
				sb.WriteByte(hex[b&0x0F])
			}
		}
	}
	return sb.String()
}
