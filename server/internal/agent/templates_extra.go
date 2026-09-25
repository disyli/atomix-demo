package agent

import "strings"

// 额外内置模板：计算器与贪吃蛇（demo 模式确定性渲染；live 模式作为能力清单提示词）。
// 评审要求"计算器与贪吃蛇两类独立 Prompt 产生不同源码和 Preview"——
// demo 模式下 RenderTemplate 按 brief 关键词匹配模板后输出完全不同的产物，
// live 模式下模板能力清单注入提示词引导模型生成对应应用。

// calculatorHTML 极简计算器：键盘布局 + 四则运算 + 历史记录（localStorage 持久化）。
func calculatorHTML() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>极简计算器</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, "PingFang SC", sans-serif; background: #f5f3ee; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  .calc { width: 320px; background: #fffdf8; border: 1px solid #e3ded2; border-radius: 18px; padding: 18px; box-shadow: 0 18px 44px -18px rgba(28,35,51,.25); }
  .screen { background: #1c2333; color: #e8e4d8; border-radius: 12px; padding: 16px 18px; text-align: right; margin-bottom: 14px; }
  .expr { font-size: 12px; color: #8a94ab; min-height: 16px; font-family: ui-monospace, monospace; }
  .out { font-size: 34px; font-weight: 700; font-family: ui-monospace, monospace; overflow: hidden; text-overflow: ellipsis; }
  .keys { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; }
  button { border: 1px solid #e3ded2; background: #fff; border-radius: 10px; padding: 14px 0; font-size: 17px; cursor: pointer; transition: all .12s ease; font-family: ui-monospace, monospace; }
  button:hover { background: #f0ece0; }
  button:active { transform: scale(.95); }
  button.op { background: #3d4fc4; color: #fff; border-color: #3d4fc4; }
  button.op:hover { background: #2f3fa3; }
  button.eq { background: #2e9e5b; color: #fff; border-color: #2e9e5b; grid-column: span 2; }
  button.eq:hover { background: #237a46; }
  button.wide { grid-column: span 2; }
  .history { margin-top: 12px; max-height: 120px; overflow-y: auto; font-size: 12px; font-family: ui-monospace, monospace; color: #6b6455; }
  .history div { padding: 4px 2px; border-bottom: 1px dashed #e3ded2; text-align: right; }
</style>
</head>
<body>
<div class="calc">
  <div class="screen"><div class="expr" id="expr"></div><div class="out" id="out">0</div></div>
  <div class="keys">
    <button data-k="C">C</button><button data-k="←">←</button><button data-k="(">(</button><button data-k=")">)</button>
    <button data-k="7">7</button><button data-k="8">8</button><button data-k="9">9</button><button data-k="÷" class="op">÷</button>
    <button data-k="4">4</button><button data-k="5">5</button><button data-k="6">6</button><button data-k="×" class="op">×</button>
    <button data-k="1">1</button><button data-k="2">2</button><button data-k="3">3</button><button data-k="−" class="op">−</button>
    <button data-k="0">0</button><button data-k=".">.</button><button data-k="=" class="eq">=</button><button data-k="+" class="op">+</button>
  </div>
  <div class="history" id="history"></div>
</div>
<script>
(function () {
  var expr = '';
  var outEl = document.getElementById('out');
  var exprEl = document.getElementById('expr');
  var histEl = document.getElementById('history');
  function loadHist() {
    try { return JSON.parse(localStorage.getItem('calc_hist') || '[]'); } catch (e) { return []; }
  }
  function saveHist(h) { try { localStorage.setItem('calc_hist', JSON.stringify(h.slice(0, 12))); } catch (e) {} }
  function renderHist() {
    var h = loadHist();
    histEl.innerHTML = '';
    h.forEach(function (line) {
      var d = document.createElement('div');
      d.textContent = line;
      histEl.appendChild(d);
    });
  }
  function calc(e) {
    var js = e.replace(/×/g, '*').replace(/÷/g, '/').replace(/−/g, '-');
    if (!/^[-+*/().0-9\s]+$/.test(js)) return null;
    try {
      var r = Function('"use strict"; return (' + js + ')')();
      return (typeof r === 'number' && isFinite(r)) ? +r.toFixed(10) : null;
    } catch (err) { return null; }
  }
  document.querySelector('.keys').addEventListener('click', function (ev) {
    var b = ev.target.closest('button');
    if (!b) return;
    var k = b.getAttribute('data-k');
    if (k === 'C') { expr = ''; outEl.textContent = '0'; exprEl.textContent = ''; return; }
    if (k === '←') { expr = expr.slice(0, -1); exprEl.textContent = expr; return; }
    if (k === '=') {
      var r = calc(expr);
      if (r === null) { exprEl.textContent = '表达式无效'; return; }
      var line = expr + ' = ' + r;
      var h = loadHist(); h.unshift(line); saveHist(h); renderHist();
      outEl.textContent = r;
      exprEl.textContent = expr + ' =';
      expr = String(r);
      return;
    }
    expr += k;
    exprEl.textContent = expr;
    outEl.textContent = calc(expr) !== null ? calc(expr) : '';
  });
  renderHist();
})();
</script>
</body>
</html>`
}

// snakeHTML 贪吃蛇小游戏：方向键/WASD 控制、计分、最高分持久化（localStorage）。
func snakeHTML() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>贪吃蛇</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, "PingFang SC", sans-serif; background: #f5f3ee; min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px; }
  .hud { display: flex; gap: 18px; font-family: ui-monospace, monospace; color: #4a4335; font-size: 14px; }
  .hud b { color: #2e9e5b; }
  .wrap { position: relative; }
  canvas { background: #fffdf8; border: 1px solid #e3ded2; border-radius: 12px; box-shadow: 0 18px 44px -18px rgba(28,35,51,.25); }
  .tip { font-size: 12px; color: #8a8271; font-family: ui-monospace, monospace; }
  .overlay { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 10px; background: rgba(245,243,238,.86); border-radius: 12px; }
  .overlay h2 { font-size: 22px; color: #1c2333; }
  .overlay button { padding: 10px 26px; background: #2e9e5b; color: #fff; border: none; border-radius: 10px; font-size: 15px; cursor: pointer; }
  .overlay button:hover { background: #237a46; }
  .hidden { display: none; }
</style>
</head>
<body>
<div class="hud">得分 <b id="score">0</b> · 最高 <b id="best">0</b></div>
<div class="wrap">
  <canvas id="cv" width="400" height="400"></canvas>
  <div class="overlay" id="ov">
    <h2 id="ovTitle">贪吃蛇</h2>
    <p style="font-size:13px;color:#6b6455" id="ovText">按方向键 / WASD 移动</p>
    <button id="btn">开始游戏</button>
  </div>
</div>
<p class="tip">方向键 / WASD 控制 · 吃食物得分 · 撞墙或自己结束</p>
<script>
(function () {
  var cv = document.getElementById('cv'), ctx = cv.getContext('2d');
  var N = 20, S = cv.width / N;
  var snake, dir, food, score, best, timer, running = false;
  try { best = parseInt(localStorage.getItem('snake_best') || '0', 10) || 0; } catch (e) { best = 0; }
  document.getElementById('best').textContent = best;
  function spawnFood() {
    do {
      food = { x: Math.floor(Math.random() * N), y: Math.floor(Math.random() * N) };
    } while (snake.some(function (s) { return s.x === food.x && s.y === food.y; }));
  }
  function reset() {
    snake = [{ x: 10, y: 10 }, { x: 9, y: 10 }, { x: 8, y: 10 }];
    dir = { x: 1, y: 0 };
    score = 0;
    document.getElementById('score').textContent = '0';
    spawnFood();
    draw();
  }
  function draw() {
    ctx.clearRect(0, 0, cv.width, cv.height);
    // 网格
    ctx.strokeStyle = 'rgba(28,35,51,.06)';
    for (var i = 1; i < N; i++) {
      ctx.beginPath(); ctx.moveTo(i * S, 0); ctx.lineTo(i * S, cv.height); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(0, i * S); ctx.lineTo(cv.width, i * S); ctx.stroke();
    }
    // 食物
    ctx.fillStyle = '#c04a50';
    ctx.beginPath();
    ctx.arc(food.x * S + S / 2, food.y * S + S / 2, S / 2 - 3, 0, Math.PI * 2);
    ctx.fill();
    // 蛇
    snake.forEach(function (s, idx) {
      ctx.fillStyle = idx === 0 ? '#2e9e5b' : '#3d4fc4';
      ctx.beginPath();
      ctx.roundRect(s.x * S + 2, s.y * S + 2, S - 4, S - 4, 6);
      ctx.fill();
    });
  }
  function step() {
    var head = { x: snake[0].x + dir.x, y: snake[0].y + dir.y };
    if (head.x < 0 || head.x >= N || head.y < 0 || head.y >= N ||
        snake.some(function (s) { return s.x === head.x && s.y === head.y; })) {
      running = false;
      clearInterval(timer);
      if (score > best) {
        best = score;
        try { localStorage.setItem('snake_best', String(best)); } catch (e) {}
        document.getElementById('best').textContent = best;
      }
      document.getElementById('ovTitle').textContent = '游戏结束';
      document.getElementById('ovText').textContent = '得分 ' + score + ' · 最高 ' + best;
      document.getElementById('btn').textContent = '再来一局';
      document.getElementById('ov').classList.remove('hidden');
      return;
    }
    snake.unshift(head);
    if (head.x === food.x && head.y === food.y) {
      score += 10;
      document.getElementById('score').textContent = score;
      spawnFood();
    } else {
      snake.pop();
    }
    draw();
  }
  function start() {
    reset();
    running = true;
    document.getElementById('ov').classList.add('hidden');
    timer = setInterval(step, 130);
  }
  document.getElementById('btn').addEventListener('click', start);
  document.addEventListener('keydown', function (e) {
    var k = e.key.toLowerCase();
    var nd = null;
    if (k === 'arrowup' || k === 'w') nd = { x: 0, y: -1 };
    else if (k === 'arrowdown' || k === 's') nd = { x: 0, y: 1 };
    else if (k === 'arrowleft' || k === 'a') nd = { x: -1, y: 0 };
    else if (k === 'arrowright' || k === 'd') nd = { x: 1, y: 0 };
    if (nd) {
      e.preventDefault();
      if (!running) { dir = nd; start(); return; }
      // 禁止 180 度回头
      if (nd.x !== -dir.x || nd.y !== -dir.y) dir = nd;
    }
  });
  if (!CanvasRenderingContext2D.prototype.roundRect) {
    CanvasRenderingContext2D.prototype.roundRect = function (x, y, w, h, r) {
      this.moveTo(x + r, y);
      this.arcTo(x + w, y, x + w, y + h, r);
      this.arcTo(x + w, y + h, x, y + h, r);
      this.arcTo(x, y + h, x, y, r);
      this.arcTo(x, y, x + w, y, r);
      this.closePath();
      return this;
    };
  }
  reset();
})();
</script>
</body>
</html>`
}

// extraTplInfos 计算器/贪吃蛇模板元信息。
func extraTplInfos() []TplInfo {
	return []TplInfo{
		{
			ID:     "calculator",
			Name:   "极简计算器",
			Reason: "需求围绕表达式计算与历史记录，计算器模板最贴合",
			Assets: []string{"屏幕区（表达式+结果）+ 4×4 键盘布局"},
			Build:  []string{"表达式求值引擎（四则运算+括号）", "localStorage 历史记录持久化"},
			Scripts: []string{
				"按键输入与表达式实时显示",
				"求值与历史记录列表",
				"清空 / 退格 / 错误提示",
			},
			Features:    []string{"四则运算", "括号表达式", "历史记录"},
			Highlighter: []string{"键盘式布局", "实时表达式预览"},
		},
		{
			ID:     "snake",
			Name:   "贪吃蛇小游戏",
			Reason: "需求是经典的贪吃蛇玩法，游戏模板最贴合",
			Assets: []string{"20×20 网格画布 + HUD 计分条"},
			Build:  []string{"蛇身数组模型与方向控制", "localStorage 最高分持久化"},
			Scripts: []string{
				"方向键/WASD 控制（禁止回头）",
				"食物随机生成与得分",
				"碰撞检测与结算浮层",
			},
			Features:    []string{"方向控制", "得分与最高分", "结算重开"},
			Highlighter: []string{"Canvas 网格渲染", "最高分持久化"},
		},
	}
}

func init() {
	// 扩展 Match 关键词规则（Match 函数在 plan.go，这里通过包装实现额外匹配优先）
}

// MatchExtra 扩展模板关键词匹配：命中返回模板 ID，未命中返回空串。
func MatchExtra(brief string) string {
	b := strings.ToLower(brief)
	if strings.Contains(b, "计算器") || strings.Contains(b, "calculator") {
		return "calculator"
	}
	if strings.Contains(b, "贪吃蛇") || strings.Contains(b, "snake") {
		return "snake"
	}
	return ""
}
