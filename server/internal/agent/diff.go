package agent

import (
	"fmt"
	"strings"
)

// ---------- 行级 diff（权限确认卡片的内容级审查） ----------

// diffMarker diff 正文与摘要之间的分隔标记，前端按此切分渲染（摘要在上、diff 行在下）。
const diffMarker = "---- DIFF ----"

// diff 展示参数：write_file 展示全文 diff（上限小），edit_file 展示上下文 diff（上限大）。
const (
	writeDiffCap = 60
	editDiffCap  = 80
	// maxDiffDP LCS 动态规划的行数规模上限，超出后降级为"整段替换"式 diff，避免 O(n²) 内存膨胀。
	maxDiffDP = 1200
)

// diffLine 单行差异：Op 为 ' '（上下文不变）/ '-'（删除）/ '+'（新增）。
type diffLine struct {
	Op   byte
	Text string
}

// splitLines 按行切分文本（统一换行符；空文本返回 nil）。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// lineDiff 计算旧文本 → 新文本的行级差异（LCS 动态规划，产出最小编辑脚本）。
// 任一侧超过 maxDiffDP 行时降级为整段替换式 diff（全部删 + 全部增），保证耗时与内存可控。
func lineDiff(oldText, newText string) []diffLine {
	o, n := splitLines(oldText), splitLines(newText)
	if len(o) > maxDiffDP || len(n) > maxDiffDP {
		out := make([]diffLine, 0, len(o)+len(n))
		for _, l := range o {
			out = append(out, diffLine{'-', l})
		}
		for _, l := range n {
			out = append(out, diffLine{'+', l})
		}
		return out
	}

	m, k := len(o), len(n)
	// dp[i][j] = o[i:] 与 n[j:] 的最长公共子序列长度
	dp := make([][]int16, m+1)
	for i := range dp {
		dp[i] = make([]int16, k+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := k - 1; j >= 0; j-- {
			if o[i] == n[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	out := make([]diffLine, 0, m+k)
	i, j := 0, 0
	for i < m && j < k {
		switch {
		case o[i] == n[j]:
			out = append(out, diffLine{' ', o[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, diffLine{'-', o[i]})
			i++
		default:
			out = append(out, diffLine{'+', n[j]})
			j++
		}
	}
	for ; i < m; i++ {
		out = append(out, diffLine{'-', o[i]})
	}
	for ; j < k; j++ {
		out = append(out, diffLine{'+', n[j]})
	}
	return out
}

// diffStats 统计差异行数（新增 / 删除 / 上下文）。
func diffStats(lines []diffLine) (adds, dels, ctx int) {
	for _, l := range lines {
		switch l.Op {
		case '+':
			adds++
		case '-':
			dels++
		default:
			ctx++
		}
	}
	return
}

// renderDiffLine 渲染单行差异为带前缀的统一 diff 文本。
func renderDiffLine(l diffLine) string {
	switch l.Op {
	case '+':
		return "+ " + l.Text
	case '-':
		return "- " + l.Text
	default:
		return "  " + l.Text
	}
}

// renderDiff 渲染差异序列；超过 maxLines 时保留首尾，中间以省略标记注明行数。
func renderDiff(lines []diffLine, maxLines int) string {
	if len(lines) == 0 {
		return "（无文本差异）"
	}
	parts := make([]string, 0, len(lines))
	if len(lines) > maxLines {
		keepHead := maxLines * 2 / 3
		keepTail := maxLines - keepHead
		for _, l := range lines[:keepHead] {
			parts = append(parts, renderDiffLine(l))
		}
		parts = append(parts, fmt.Sprintf("……（中间省略 %d 行差异）……", len(lines)-maxLines))
		for _, l := range lines[len(lines)-keepTail:] {
			parts = append(parts, renderDiffLine(l))
		}
	} else {
		for _, l := range lines {
			parts = append(parts, renderDiffLine(l))
		}
	}
	return strings.Join(parts, "\n")
}
