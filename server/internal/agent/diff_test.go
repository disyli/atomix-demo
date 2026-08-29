package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineDiffBasic(t *testing.T) {
	got := lineDiff("a\nb\nc", "a\nX\nc")
	want := []diffLine{
		{' ', "a"}, {'-', "b"}, {'+', "X"}, {' ', "c"},
	}
	if len(got) != len(want) {
		t.Fatalf("行数不符：got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 行不符：got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLineDiffEmptyOld(t *testing.T) {
	got := lineDiff("", "x\ny")
	if len(got) != 2 || got[0].Op != '+' || got[1].Op != '+' {
		t.Fatalf("空旧文应为全增：got %v", got)
	}
}

func TestLineDiffNoChange(t *testing.T) {
	got := lineDiff("same\ntext", "same\ntext")
	for _, l := range got {
		if l.Op != ' ' {
			t.Fatalf("相同文本不应有差异行：%v", got)
		}
	}
}

func TestLineDiffHugeFallback(t *testing.T) {
	old := strings.Repeat("o\n", maxDiffDP+10)
	nw := strings.Repeat("n\n", maxDiffDP+10)
	got := lineDiff(old, nw)
	// 降级：全部删 + 全部增（Repeat("o\n", n) 按行切分含尾空行，实际 n+1 行）
	if len(got) != 2*(maxDiffDP+11) {
		t.Fatalf("降级 diff 行数不符：%d", len(got))
	}
	if got[0].Op != '-' || got[len(got)-1].Op != '+' {
		t.Fatalf("降级 diff 应先删后增：%v %v", got[0], got[len(got)-1])
	}
}

func TestRenderDiffCap(t *testing.T) {
	lines := make([]diffLine, 0, 100)
	for i := 0; i < 100; i++ {
		lines = append(lines, diffLine{'+', "line"})
	}
	out := renderDiff(lines, 10)
	if !strings.Contains(out, "省略") {
		t.Fatalf("超限时应包含省略标记：%s", out)
	}
	if strings.Count(out, "\n") > 12 {
		t.Fatalf("输出行数应接近上限：%s", out)
	}
}

func TestPermissionDetailEditFileWithDiff(t *testing.T) {
	rt := &reactSession{html: "<h1>标题</h1>\n<p>旧内容</p>"}
	args, _ := json.Marshal(editArgs{OldString: "<p>旧内容</p>", NewString: "<p>新内容</p>"})
	detail := permissionDetail(rt, "edit_file", string(args))
	if !strings.Contains(detail, diffMarker) {
		t.Fatalf("edit_file 详情应包含 diff 标记：%s", detail)
	}
	if !strings.Contains(detail, "- <p>旧内容</p>") || !strings.Contains(detail, "+ <p>新内容</p>") {
		t.Fatalf("edit_file 详情应包含删/增行：%s", detail)
	}
}

func TestPermissionDetailWriteFileFirstWrite(t *testing.T) {
	rt := &reactSession{}
	args, _ := json.Marshal(writeArgs{Path: "index.html", Content: "<!DOCTYPE html>\n<html></html>"})
	detail := permissionDetail(rt, "write_file", string(args))
	if !strings.Contains(detail, "首次写入") {
		t.Fatalf("首写详情应说明为首次写入：%s", detail)
	}
	if !strings.Contains(detail, "+ <!DOCTYPE html>") {
		t.Fatalf("首写详情应为全增 diff：%s", detail)
	}
}
