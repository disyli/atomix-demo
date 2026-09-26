package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"atomix-demo/server/internal/llm"
	"atomix-demo/server/internal/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------- 测试基础设施 ----------

// scriptLLM 按预设脚本驱动 ReAct 循环的假模型：每次 ChatWithTools 吐出脚本中的
// 下一步工具调用，用于单测中模拟真实模型的各种行为序列（写入/校验/编辑/收尾/失败）。
type scriptLLM struct {
	steps []llm.ToolCall
	calls int
}

func (s *scriptLLM) ChatJSON(ctx context.Context, m []llm.ChatMessage) (string, error) {
	return `{"intent":"build","brief":"测试需求"}`, nil
}
func (s *scriptLLM) ChatHTML(ctx context.Context, m []llm.ChatMessage) (string, error) {
	return "<!DOCTYPE html></html>", nil
}
func (s *scriptLLM) ChatWithTools(ctx context.Context, m []llm.ChatMessage, tools []llm.Tool, temp float64, maxTok int) (*llm.ToolCallResponse, error) {
	if s.calls >= len(s.steps) {
		// 脚本耗尽：直接给出文本回答退出循环
		return &llm.ToolCallResponse{Content: "脚本结束"}, nil
	}
	tc := s.steps[s.calls]
	s.calls++
	return &llm.ToolCallResponse{Content: "step", ToolCalls: []llm.ToolCall{tc}}, nil
}

func mkTool(name string, args interface{}) llm.ToolCall {
	b, _ := json.Marshal(args)
	tc := llm.ToolCall{ID: fmt.Sprintf("call-%s-%d", name, mkToolSeq()), Type: "function"}
	tc.Function.Name = name
	tc.Function.Arguments = string(b)
	return tc
}

var toolSeq = 100

func mkToolSeq() int {
	toolSeq++
	return toolSeq
}

// validHTML 通过 checkProduct 与浏览器实测的最小合法产物：
// ≥1000 字符、含 localStorage + script + button（交互元素）、body 文本充足（防白屏误判）、
// 无 document.cookie、无占位符关键词（"todo："等）、脚本运行零异常零 console error。
const validHTML = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>测试应用</title>
<style>body{font-family:sans-serif;margin:40px;background:#f5f5f5}</style></head>
<body><h1>欢迎使用本应用</h1><p>这是一个用于自动化测试的完整单文件应用。</p>
<button id="b">点我保存记录</button><button id="c">清空记录</button><div id="list"></div>
<p id="hint">点击按钮后数据将写入本地存储并在刷新后保留。</p>
<script>
(function () {
  var KEY = 'items';
  function read() {
    try { return JSON.parse(localStorage.getItem(KEY) || '[]'); } catch (e) { return []; }
  }
  function render() {
    var arr = read();
    var el = document.getElementById('list');
    el.textContent = arr.length ? ('已保存 ' + arr.length + ' 条记录') : '暂无记录，点击上方按钮新增';
  }
  document.getElementById('b').addEventListener('click', function () {
    var arr = read();
    arr.push({ text: 'record-' + (arr.length + 1), at: Date.now() });
    localStorage.setItem(KEY, JSON.stringify(arr));
    render();
  });
  document.getElementById('c').addEventListener('click', function () {
    localStorage.removeItem(KEY);
    render();
  });
  render();
})();
</script></body></html>`

// v2Suffix 迭代轮的新增功能片段（edit_file 注入在 </body> 前）。
const v2Suffix = "<!-- v2 feature --></body>"

// v2HTML 首轮产物 + 迭代新增功能后的期望产物（旧功能保留，新内容追加）。
var v2HTML = strings.Replace(validHTML, "</body>", v2Suffix, 1)

// brokenHTML 含 document.cookie 的坏产物（校验必失败）。
// 注意长度须 ≥200 字符，否则会被 toolWrite 的"内容过短"护栏拒绝写入，坏产物根本进不了校验环节。
const brokenHTML = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>坏产物</title>
<style>body{font-family:sans-serif;margin:40px}</style></head>
<body><h1>这是一个含安全问题的坏产物页面</h1><p>本页面使用 document.cookie 存储数据，在沙箱环境下会抛出安全异常。</p>
<div id="box">页面内容区块，用于撑起足够的字符体积，让写入护栏放行。</div>
<script>
document.cookie = 'demo=1';
document.getElementById('box').textContent = 'cookie 已写入';
</script></body></html>`

// openTestDB 打开临时 SQLite 并注册到全局 store.DB。
func openTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&store.User{}, &store.Project{}, &store.Event{}, &store.Attachment{}, &store.Message{}, &store.Snapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := store.DB
	store.DB = db
	t.Cleanup(func() { store.DB = prev })
	return dir
}

// newTestUser 创建测试用户，返回 UID。
func newTestUser(t *testing.T) uint {
	t.Helper()
	u := &store.User{Email: fmt.Sprintf("sm_%d@t.test", os.Getpid()), PasswordHash: "x", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

// newTestAgent 用脚本模型构造 Agent（OnPermission 置空 → 权限自动放行路径）。
func newTestAgent(script []llm.ToolCall) *Agent {
	return &Agent{
		LLM:          &scriptLLM{steps: script},
		UseMock:      false,
		PermRegistry: NewPermRegistry(),
		Runs:         NewRunRegistry(),
	}
}

// ---------- 状态机单测 ----------

// TestFinishRejectedWithoutChecks 模型写入后跳过 run_checks 直接 finish：
// finish 必须被拒绝，最终项目状态 failed（完成门控生效）。
func TestFinishRejectedWithoutChecks(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	script := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("finish", finishArgs{Summary: "跳过校验直接收尾"}),
	}
	ag := newTestAgent(script)
	_, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err == nil {
		t.Fatal("跳过校验的构建必须失败")
	}
	var p store.Project
	store.DB.Order("id DESC").First(&p)
	if p.Status != "failed" {
		t.Fatalf("跳过校验时项目状态应为 failed，实际 %s", p.Status)
	}
	if p.HTML != "" || p.Version != 0 {
		t.Fatalf("未通过校验的产物不应落库：HTML=%d字符 Version=%d", len(p.HTML), p.Version)
	}
	// Message 落库：1 user + 1 failed run
	var users, runs int64
	store.DB.Model(&store.Message{}).Where("project_id = ? AND role = ?", p.ID, "user").Count(&users)
	store.DB.Model(&store.Message{}).Where("project_id = ? AND role = ? AND status = ?", p.ID, "assistant", "failed").Count(&runs)
	if users != 1 || runs != 1 {
		t.Fatalf("失败轮 Message 落库异常: user=%d failedRun=%d", users, runs)
	}
}

// TestHappyPathCreatesSnapshot 正常全链路：写入 → 校验 → finish，
// 项目 ready、version=1、快照存在、HTML 落库。
func TestHappyPathCreatesSnapshot(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	script := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "完成"}),
	}
	ag := newTestAgent(script)
	p, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("正常构建应成功: %v", err)
	}
	if p.Status != "ready" || p.Version != 1 {
		t.Fatalf("应 ready v1，实际 status=%s version=%d", p.Status, p.Version)
	}
	if p.HTML != validHTML || p.LastGoodHTML != validHTML {
		t.Fatal("HTML 与 LastGoodHTML 均应落库")
	}
	var snaps []store.Snapshot
	store.DB.Where("project_id = ?", p.ID).Find(&snaps)
	if len(snaps) != 1 || snaps[0].Version != 1 {
		t.Fatalf("应有 1 条 v1 快照，实际 %d 条", len(snaps))
	}
	var users, doneRuns int64
	store.DB.Model(&store.Message{}).Where("project_id = ? AND role = ?", p.ID, "user").Count(&users)
	store.DB.Model(&store.Message{}).Where("project_id = ? AND role = ? AND status = ?", p.ID, "assistant", "done").Count(&doneRuns)
	if users != 1 || doneRuns != 1 {
		t.Fatalf("成功轮 Message 异常: user=%d doneRun=%d", users, doneRuns)
	}
}

// TestEditInvalidatesChecks 校验通过后再编辑（不重新校验）：
// checksPassed 必须被重置，finish 拒绝，项目 failed。
func TestEditInvalidatesChecks(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	script := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("run_checks", map[string]string{}),
		mkTool("edit_file", editArgs{OldString: "</body>", NewString: "<!-- extra edit --></body>"}),
		mkTool("finish", finishArgs{Summary: "编辑后未重新校验"}),
	}
	ag := newTestAgent(script)
	_, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err == nil {
		t.Fatal("编辑后未重新校验必须失败")
	}
	var p store.Project
	store.DB.Order("id DESC").First(&p)
	if p.Status != "failed" || p.Version != 0 {
		t.Fatalf("应为 failed v0，实际 %s v%d", p.Status, p.Version)
	}
	if p.HTML != "" {
		t.Fatal("未通过校验的编辑产物不应落库")
	}
}

// TestRefineFailureKeepsLastGood 迭代失败保留旧版本：
// 首轮成功（v1）后，第二轮 refine 写入坏产物且校验失败 →
// 项目 failed 但 HTML/预览仍为 v1 内容，LastGoodHTML 不变。
func TestRefineFailureKeepsLastGood(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	// 首轮成功
	script1 := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "完成"}),
	}
	ag := newTestAgent(script1)
	p1, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err != nil || p1.Version != 1 {
		t.Fatalf("首轮构建应成功 v1: %v", err)
	}

	// 第二轮 refine：模型读旧产物 → 整体重写为坏产物 → 校验失败 → 修复耗尽（脚本结束）
	script2 := []llm.ToolCall{
		mkTool("read_file", pathArgs{Path: "index.html"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: brokenHTML}),
		mkTool("run_checks", map[string]string{}),
	}
	ag2 := newTestAgent(script2)
	_, err = ag2.Refine(context.Background(), uid, p1.ID, "改成坏的", nil, PipelineEvents{})
	if err == nil {
		t.Fatal("坏产物 refine 应失败")
	}
	var p store.Project
	store.DB.Where("id = ?", p1.ID).First(&p)
	if p.Status != "failed" {
		t.Fatalf("失败后状态应 failed，实际 %s", p.Status)
	}
	if p.HTML != validHTML {
		t.Fatal("失败后 HTML 应回退保留最后成功版本（v1 内容）")
	}
	if p.LastGoodHTML != validHTML || p.Version != 1 {
		t.Fatalf("LastGoodHTML 应保留 v1 内容且版本不回退：ver=%d", p.Version)
	}
	// 快照仍只有 v1 一条（失败轮不建快照）
	var snaps []store.Snapshot
	store.DB.Where("project_id = ?", p1.ID).Find(&snaps)
	if len(snaps) != 1 {
		t.Fatalf("失败轮不应创建快照，应有 1 条，实际 %d", len(snaps))
	}
	// Message：2 user + 1 done + 1 failed
	var users, doneRuns, failedRuns int64
	store.DB.Model(&store.Message{}).Where("project_id = ? AND role = ?", p1.ID, "user").Count(&users)
	store.DB.Model(&store.Message{}).Where("project_id = ? AND status = ?", p1.ID, "done").Count(&doneRuns)
	store.DB.Model(&store.Message{}).Where("project_id = ? AND status = ?", p1.ID, "failed").Count(&failedRuns)
	if users != 2 || doneRuns != 1 || failedRuns != 1 {
		t.Fatalf("Message 终态计数异常: user=%d done=%d failed=%d", users, doneRuns, failedRuns)
	}
}

// TestRefineSuccessIncrementsVersion 两轮成功 refine：
// 每轮 version 递增、快照追加、旧功能保留（HTML 累积变化）。
func TestRefineSuccessIncrementsVersion(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	script1 := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "完成"}),
	}
	ag := newTestAgent(script1)
	p1, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("首轮构建应成功: %v", err)
	}
	if p1.Version != 1 {
		t.Fatalf("首轮应 v1，实际 v%d", p1.Version)
	}

	script2 := []llm.ToolCall{
		mkTool("read_file", pathArgs{Path: "index.html"}),
		mkTool("edit_file", editArgs{OldString: "</body>", NewString: v2Suffix}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "v2 完成"}),
	}
	ag2 := newTestAgent(script2)
	p2, err := ag2.Refine(context.Background(), uid, p1.ID, "加功能A", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("第二轮 refine 应成功: %v", err)
	}
	if p2.Version != 2 || p2.Status != "ready" {
		t.Fatalf("第二轮应 ready v2，实际 %s v%d", p2.Status, p2.Version)
	}
	if p2.HTML != v2HTML {
		t.Fatal("第二轮产物应为旧功能 + 新增内容（旧功能保留）")
	}
	var snaps []store.Snapshot
	store.DB.Where("project_id = ?", p1.ID).Order("version ASC").Find(&snaps)
	if len(snaps) != 2 || snaps[0].Version != 1 || snaps[1].Version != 2 {
		t.Fatalf("应有 v1+v2 两条快照，实际 %d 条", len(snaps))
	}
	if snaps[1].HTML != v2HTML || snaps[0].HTML != validHTML {
		t.Fatal("快照内容应与各轮产物一致")
	}
}

// TestRollbackAtomicity 回滚原子性：
// v1 → v2 → 回滚到 v1 后，HTML/LastGoodHTML/Version/预览内容全部一致指向 v1 源码，
// 快照追加 v3（回滚快照），再次回滚到 v2 也成立。
func TestRollbackAtomicity(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	script1 := []llm.ToolCall{
		mkTool("plan_app", planArgs{AppName: "测试应用", Template: "todo"}),
		mkTool("write_file", writeArgs{Path: "index.html", Content: validHTML}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "v1"}),
	}
	ag := newTestAgent(script1)
	p1, err := ag.Run(context.Background(), uid, "做一个待办清单", "build", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("首轮构建应成功: %v", err)
	}

	script2 := []llm.ToolCall{
		mkTool("edit_file", editArgs{OldString: "</body>", NewString: v2Suffix}),
		mkTool("run_checks", map[string]string{}),
		mkTool("finish", finishArgs{Summary: "v2"}),
	}
	ag2 := newTestAgent(script2)
	p2, err := ag2.Refine(context.Background(), uid, p1.ID, "加功能", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("第二轮 refine 应成功: %v", err)
	}
	if p2.Version != 2 {
		t.Fatalf("第二轮应 v2，实际 v%d", p2.Version)
	}

	// 回滚到 v1
	p3, snap, err := store.RollbackSnapshot(p1.ID, 1)
	if err != nil {
		t.Fatalf("回滚失败: %v", err)
	}
	if p3.HTML != validHTML || p3.LastGoodHTML != validHTML {
		t.Fatal("回滚后 HTML/LastGoodHTML 应指向 v1 源码")
	}
	if p3.Version != 3 {
		t.Fatalf("回滚应生成 v3 快照，实际 v%d", p3.Version)
	}
	if snap.Label != "回滚至 v1" {
		t.Fatalf("回滚快照 label 异常: %s", snap.Label)
	}
	// 再回滚到 v2（v2 快照仍存在）
	p4, _, err := store.RollbackSnapshot(p1.ID, 2)
	if err != nil {
		t.Fatalf("二次回滚失败: %v", err)
	}
	if p4.HTML != v2HTML || p4.Version != 4 {
		t.Fatalf("二次回滚后应为 v2 内容 v4 版本号：ver=%d", p4.Version)
	}
	// 回滚到不存在的版本必须失败
	if _, _, err := store.RollbackSnapshot(p1.ID, 99); err == nil {
		t.Fatal("回滚到不存在版本应报错")
	}
}

// TestDemoModeStateMachine demo 模式（脚本化轨迹）同样走 verified 状态机：
// 完成后 ready + v1 + 快照存在。
func TestDemoModeStateMachine(t *testing.T) {
	openTestDB(t)
	uid := newTestUser(t)
	ag := &Agent{
		LLM:          &scriptLLM{},
		UseMock:      true,
		PermRegistry: NewPermRegistry(),
		Runs:         NewRunRegistry(),
	}
	p, err := ag.Run(context.Background(), uid, "做一个待办清单，支持勾选完成", "build", nil, PipelineEvents{})
	if err != nil {
		t.Fatalf("demo 模式构建应成功: %v", err)
	}
	if p.Status != "ready" || p.Version != 1 {
		t.Fatalf("demo 模式应 ready v1，实际 %s v%d", p.Status, p.Version)
	}
	var snaps []store.Snapshot
	store.DB.Where("project_id = ?", p.ID).Find(&snaps)
	if len(snaps) != 1 {
		t.Fatalf("demo 模式应有 v1 快照，实际 %d", len(snaps))
	}
}
