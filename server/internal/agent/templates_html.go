package agent

import (
	"html/template"
	"strings"
)

// PreviewMeta 预览页信息。
type PreviewMeta struct {
	AppName string
	HTML    string
}

// RenderTemplate 返回模板 ID 对应的完整单文件应用 HTML。
func RenderTemplate(id, appName string) (TplInfo, string, error) {
	info, ok := Get(id)
	if !ok {
		info, _ = Get("todo")
	}
	if appName == "" {
		appName = info.Name
	}
	tpl, ok := htmlTemplates[id]
	if !ok {
		tpl = htmlTemplates["todo"]
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, PreviewMeta{AppName: appName}); err != nil {
		return info, "", err
	}
	return info, sb.String(), nil
}

var htmlTemplates = map[string]*template.Template{
	"todo":   template.Must(template.New("todo").Parse(todoHTML)),
	"notes":  template.Must(template.New("notes").Parse(notesHTML)),
	"kanban": template.Must(template.New("kanban").Parse(kanbanHTML)),
}

const todoHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.AppName}}</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, "PingFang SC", sans-serif; background: linear-gradient(135deg, #eef2ff, #fdf2f8); min-height: 100vh; padding: 40px 16px; }
.app { max-width: 520px; margin: 0 auto; }
h1 { font-size: 26px; color: #1e1b4b; margin-bottom: 20px; }
.card { background: #fff; border-radius: 16px; box-shadow: 0 10px 30px rgba(30,27,75,.08); padding: 20px; }
.input-row { display: flex; gap: 8px; margin-bottom: 14px; }
input[type=text] { flex: 1; border: 1.5px solid #e2e8f0; border-radius: 10px; padding: 11px 14px; font-size: 15px; outline: none; }
input[type=text]:focus { border-color: #6366f1; }
button { cursor: pointer; border: none; border-radius: 10px; font-size: 14px; }
.add { background: #6366f1; color: #fff; padding: 0 20px; font-weight: 600; }
.filters { display: flex; gap: 6px; margin-bottom: 12px; }
.filters button { background: #f1f5f9; color: #475569; padding: 6px 14px; border-radius: 999px; }
.filters button.active { background: #6366f1; color: #fff; }
ul { list-style: none; }
li { display: flex; align-items: center; gap: 10px; padding: 12px 4px; border-bottom: 1px solid #f1f5f9; }
li.done .txt { text-decoration: line-through; color: #94a3b8; }
.chk { width: 20px; height: 20px; accent-color: #6366f1; }
.txt { flex: 1; font-size: 15px; color: #334155; }
.del { background: none; color: #cbd5e1; font-size: 17px; }
.del:hover { color: #ef4444; }
.stats { margin-top: 14px; font-size: 13px; color: #64748b; display: flex; justify-content: space-between; align-items: center; }
.bar { height: 6px; background: #e2e8f0; border-radius: 3px; overflow: hidden; margin-top: 8px; }
.bar > div { height: 100%; background: linear-gradient(90deg, #6366f1, #a855f7); transition: width .3s; }
.clear { background: none; color: #94a3b8; font-size: 12px; }
.clear:hover { color: #ef4444; }
.empty { text-align: center; color: #94a3b8; padding: 30px 0; font-size: 14px; }
</style>
</head>
<body>
<div class="app">
  <h1>✅ {{.AppName}}</h1>
  <div class="card">
    <div class="input-row">
      <input type="text" id="inp" placeholder="想做什么？回车添加…" maxlength="50">
      <button class="add" onclick="add()">添加</button>
    </div>
    <div class="filters" id="filters">
      <button data-f="all" class="active">全部</button>
      <button data-f="active">未完成</button>
      <button data-f="done">已完成</button>
    </div>
    <ul id="list"></ul>
    <div class="stats">
      <span id="stat"></span>
      <button class="clear" onclick="clearDone()">清空已完成</button>
    </div>
    <div class="bar"><div id="bar" style="width:0%"></div></div>
  </div>
</div>
<script>
const KEY = "atomix_todos";
let todos = JSON.parse(localStorage.getItem(KEY) || "[]");
let filter = "all";
const $ = id => document.getElementById(id);
function save() { localStorage.setItem(KEY, JSON.stringify(todos)); }
function add() {
  const v = $("inp").value.trim();
  if (!v) return;
  todos.unshift({ id: Date.now(), text: v, done: false });
  $("inp").value = ""; save(); render();
}
function toggle(id) { todos = todos.map(t => t.id === id ? { ...t, done: !t.done } : t); save(); render(); }
function del(id) { todos = todos.filter(t => t.id !== id); save(); render(); }
function clearDone() { todos = todos.filter(t => !t.done); save(); render(); }
document.querySelectorAll("#filters button").forEach(b => b.onclick = () => {
  filter = b.dataset.f;
  document.querySelectorAll("#filters button").forEach(x => x.classList.toggle("active", x === b));
  render();
});
$("inp").addEventListener("keydown", e => { if (e.key === "Enter") add(); });
function render() {
  const shown = todos.filter(t => filter === "all" || (filter === "done" ? t.done : !t.done));
  $("list").innerHTML = shown.length ? shown.map(t => '<li class="' + (t.done ? "done" : "") + '"><input type="checkbox" class="chk" ' + (t.done ? "checked" : "") + ' onchange="toggle(' + t.id + ')"><span class="txt">' + t.text.replace(/</g, "&lt;") + '</span><button class="del" onclick="del(' + t.id + ')">✕</button></li>').join("") : '<div class="empty">暂无任务，添加一个吧</div>';
  const done = todos.filter(t => t.done).length;
  $("stat").textContent = "共 " + todos.length + " 项 · 已完成 " + done + " 项";
  $("bar").style.width = (todos.length ? done / todos.length * 100 : 0) + "%";
}
render();
</script>
</body>
</html>`

const notesHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.AppName}}</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, "PingFang SC", sans-serif; background: #f8fafc; min-height: 100vh; padding: 40px 20px; }
.app { max-width: 960px; margin: 0 auto; }
.head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
h1 { font-size: 26px; color: #0f172a; }
input[type=text] { border: 1.5px solid #e2e8f0; border-radius: 10px; padding: 10px 14px; font-size: 14px; outline: none; width: 220px; }
input[type=text]:focus { border-color: #f59e0b; }
.add { background: #f59e0b; color: #fff; border: none; border-radius: 10px; padding: 10px 20px; font-weight: 600; cursor: pointer; }
.wall { columns: 3 240px; column-gap: 14px; }
.note { break-inside: avoid; border-radius: 14px; padding: 16px; margin-bottom: 14px; box-shadow: 0 4px 14px rgba(15,23,42,.07); position: relative; transition: transform .15s; }
.note:hover { transform: translateY(-2px); }
.note h3 { font-size: 15px; margin-bottom: 8px; color: #1e293b; word-break: break-all; }
.note p { font-size: 13px; color: #475569; line-height: 1.6; white-space: pre-wrap; word-break: break-all; }
.note .meta { margin-top: 10px; font-size: 11px; opacity: .65; display: flex; justify-content: space-between; }
.note .ops { position: absolute; top: 10px; right: 10px; display: flex; gap: 6px; opacity: 0; transition: opacity .15s; }
.note:hover .ops { opacity: 1; }
.ops button { border: none; background: rgba(255,255,255,.85); border-radius: 6px; width: 24px; height: 24px; cursor: pointer; font-size: 12px; }
.pin { color: #b45309; }
.del { color: #dc2626; }
.c0 { background: #fef3c7; } .c1 { background: #dbeafe; } .c2 { background: #dcfce7; }
.c3 { background: #fce7f3; } .c4 { background: #ede9fe; } .c5 { background: #f1f5f9; }
.count { text-align: center; color: #94a3b8; font-size: 13px; margin-top: 8px; }
.empty { text-align: center; color: #94a3b8; padding: 60px 0; }
</style>
</head>
<body>
<div class="app">
  <div class="head">
    <h1>🗒️ {{.AppName}}</h1>
    <div style="display:flex;gap:8px">
      <input type="text" id="q" placeholder="搜索便签…">
      <button class="add" onclick="newNote()">+ 新便签</button>
    </div>
  </div>
  <div class="wall" id="wall"></div>
  <div class="count" id="count"></div>
</div>
<script>
const KEY = "atomix_notes";
let notes = JSON.parse(localStorage.getItem(KEY) || "[]");
const colors = 6;
const $ = id => document.getElementById(id);
function save() { localStorage.setItem(KEY, JSON.stringify(notes)); }
function esc(s) { return (s || "").replace(/</g, "&lt;"); }
function newNote() {
  const title = prompt("便签标题："); if (!title) return;
  const content = prompt("便签内容：") || "";
  notes.unshift({ id: Date.now(), title, content, color: Math.floor(Math.random() * colors), pinned: false, ts: Date.now() });
  save(); render();
}
function del(id) { notes = notes.filter(n => n.id !== id); save(); render(); }
function pin(id) { notes = notes.map(n => n.id === id ? { ...n, pinned: !n.pinned } : n); save(); render(); }
$("q").addEventListener("input", render);
function render() {
  const q = $("q").value.trim().toLowerCase();
  let shown = notes.filter(n => !q || (n.title + n.content).toLowerCase().includes(q));
  shown = shown.sort((a, b) => (b.pinned - a.pinned) || (b.ts - a.ts));
  $("wall").innerHTML = shown.length ? shown.map(n =>
    '<div class="note c' + n.color + '"><div class="ops"><button class="pin" title="置顶" onclick="pin(' + n.id + ')">' + (n.pinned ? "📌" : "📍") + '</button><button class="del" title="删除" onclick="del(' + n.id + ')">✕</button></div><h3>' + esc(n.title) + '</h3><p>' + esc(n.content) + '</p><div class="meta"><span>' + new Date(n.ts).toLocaleDateString() + '</span><span>' + n.content.length + ' 字</span></div></div>'
  ).join("") : '<div class="empty">还没有便签，点击右上角新建</div>';
  $("count").textContent = "共 " + notes.length + " 张便签";
}
render();
</script>
</body>
</html>`

const kanbanHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.AppName}}</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, "PingFang SC", sans-serif; background: linear-gradient(160deg, #0f172a, #1e293b); min-height: 100vh; padding: 36px 20px; color: #e2e8f0; }
.app { max-width: 1000px; margin: 0 auto; }
.head { text-align: center; margin-bottom: 22px; }
h1 { font-size: 26px; margin-bottom: 6px; }
.sub { color: #94a3b8; font-size: 13px; }
.addrow { display: flex; gap: 8px; max-width: 620px; margin: 0 auto 24px; }
input, select { border: 1.5px solid #334155; background: #1e293b; color: #e2e8f0; border-radius: 10px; padding: 10px 14px; font-size: 14px; outline: none; }
input { flex: 1; }
input:focus, select:focus { border-color: #38bdf8; }
.add { background: #38bdf8; color: #06121f; border: none; border-radius: 10px; padding: 0 22px; font-weight: 700; cursor: pointer; }
.board { display: grid; grid-template-columns: repeat(3, 1fr); gap: 14px; }
.col { background: rgba(30,41,59,.75); border: 1px solid #334155; border-radius: 14px; padding: 14px; min-height: 300px; }
.col h2 { font-size: 14px; margin-bottom: 12px; display: flex; justify-content: space-between; color: #cbd5e1; }
.badge { background: #334155; border-radius: 999px; padding: 1px 9px; font-size: 12px; }
.cards { display: flex; flex-direction: column; gap: 8px; }
.card { background: #0f172a; border: 1px solid #334155; border-radius: 10px; padding: 12px; }
.card h3 { font-size: 14px; color: #f1f5f9; margin-bottom: 4px; }
.card p { font-size: 12px; color: #94a3b8; line-height: 1.5; white-space: pre-wrap; word-break: break-all; }
.card .ops { display: flex; gap: 6px; margin-top: 10px; }
.card .ops button { border: none; background: #1e293b; color: #94a3b8; border-radius: 6px; padding: 4px 10px; font-size: 12px; cursor: pointer; }
.card .ops button:hover { color: #38bdf8; }
.card .ops .d:hover { color: #f87171; }
.empty { text-align: center; color: #64748b; font-size: 13px; padding: 30px 0; }
.bar { height: 6px; background: #1e293b; border-radius: 3px; overflow: hidden; margin-top: 22px; }
.bar > div { height: 100%; background: linear-gradient(90deg, #38bdf8, #818cf8); transition: width .3s; }
</style>
</head>
<body>
<div class="app">
  <div class="head">
    <h1>📋 {{.AppName}}</h1>
    <div class="sub">跨列移动卡片，追踪每个任务所处阶段 · 数据自动保存</div>
  </div>
  <div class="addrow">
    <input type="text" id="t" placeholder="卡片标题…" maxlength="30">
    <input type="text" id="d" placeholder="描述（可选）" maxlength="80">
    <select id="c"><option value="0">待办</option><option value="1">进行中</option><option value="2">已完成</option></select>
    <button class="add" onclick="addCard()">添加</button>
  </div>
  <div class="board" id="board"></div>
  <div class="bar"><div id="bar" style="width:0%"></div></div>
</div>
<script>
const KEY = "atomix_kanban";
const COLS = ["待办", "进行中", "已完成"];
let cards = JSON.parse(localStorage.getItem(KEY) || "[]");
const $ = id => document.getElementById(id);
function save() { localStorage.setItem(KEY, JSON.stringify(cards)); }
function esc(s) { return (s || "").replace(/</g, "&lt;"); }
function addCard() {
  const t = $("t").value.trim(); if (!t) return;
  cards.push({ id: Date.now(), title: t, desc: $("d").value.trim(), col: +$("c").value, ts: Date.now() });
  $("t").value = ""; $("d").value = ""; save(); render();
}
function del(id) { cards = cards.filter(c => c.id !== id); save(); render(); }
function move(id, dir) { cards = cards.map(c => c.id === id ? { ...c, col: Math.min(2, Math.max(0, c.col + dir)) } : c); save(); render(); }
function render() {
  for (let i = 0; i < 3; i++) {
    const list = cards.filter(c => c.col === i);
    document.getElementById("col" + i).innerHTML =
      "<h2>" + COLS[i] + ' <span class="badge">' + list.length + "</span></h2>" +
      '<div class="cards">' + (list.length ? list.map(c =>
        '<div class="card"><h3>' + esc(c.title) + "</h3>" + (c.desc ? "<p>" + esc(c.desc) + "</p>" : "") +
        '<div class="ops">' + (i > 0 ? '<button onclick="move(' + c.id + ',-1)">← 左移</button>' : "") +
        (i < 2 ? '<button onclick="move(' + c.id + ',1)">右移 →</button>' : "") +
        '<button class="d" onclick="del(' + c.id + ')">删除</button></div></div>'
      ).join("") : '<div class="empty">暂无卡片</div>') + "</div>";
  }
  const done = cards.filter(c => c.col === 2).length;
  $("bar").style.width = (cards.length ? done / cards.length * 100 : 0) + "%";
}
render();
</script>
</body>
</html>`
