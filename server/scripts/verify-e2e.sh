#!/usr/bin/env bash
# verify-e2e.sh — atomix-demo 数据持久化端到端回归验证
#
# 用法（在能访问服务 API 的机器上执行，服务端容器无需改动）:
#   BASE=http://127.0.0.1 bash server/scripts/verify-e2e.sh
#   BRIEF_REQ="做一个计算器" BASE=http://127.0.0.1:8080 bash server/scripts/verify-e2e.sh
#
# 前置依赖: curl、python3、stat、grep
#
# 验证项（对应笔试"数据持久化"需求）:
#   A. build + 2 轮 refine 后，同一 project 行复用（项目列表仅 1 个）
#   B. 每轮对话保存为新 Message 记录（3 条 user + 3 条 run，逐轮成对）
#   C. 两轮 refine 的产物落地（预览 HTML 含修改关键词）
#   D. chat 闲聊消息也挂到同一 project
#
# 说明: live 模式下 ReAct 循环的 write_file 会触发权限确认（SSE permission 事件），
#       本脚本内置自动批准守护（allow_session，一次批准同工具全放行），
#       因此无需人工干预即可跑完；mock 模式无权限事件，守护自动空转。
set -u

BASE="${BASE:-http://127.0.0.1}"
BRIEF_REQ="${BRIEF_REQ:-做一个极简待办清单}"
SSE_MAX_TIME="${SSE_MAX_TIME:-900}"   # 单次 SSE 最长等待秒数
APPROVE_ROUNDS="${APPROVE_ROUNDS:-400}" # 自动批准守护轮询次数（每轮 sleep 2s）

PASS=0; FAIL=0
ok()  { echo "  PASS: $1"; PASS=$((PASS+1)); }
bad() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

EMAIL="verify_$(date +%s)@atomix.test"
PASSWD="verify123456"
TMPDIR_E2E=$(mktemp -d /tmp/atomix-e2e.XXXXXX)
trap 'rm -rf "$TMPDIR_E2E"' EXIT

echo "=== 1. 注册测试用户（$EMAIL）==="
REG=$(curl -s -X POST "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWD\"}")
TOKEN=$(echo "$REG" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])' 2>/dev/null)
if [ -z "$TOKEN" ]; then echo "FATAL: 注册失败或返回异常: $REG"; exit 1; fi
echo "  token OK"

# auto_approve <sse输出文件>: 增量跟踪 SSE 文件，发现权限请求即自动 allow_session
# SSE 事件格式: event:permission + data:perm-<id>\x1f<tool>\x1fb64:<detail>（\x1f 为真实控制字符）
auto_approve() {
  local file=$1 last_pos=0 approved="" rid chunk size
  for _ in $(seq 1 "$APPROVE_ROUNDS"); do
    [ -f "$file" ] || { sleep 2; continue; }
    size=$(stat -c '%s' "$file")
    if [ "$size" -gt "$last_pos" ]; then
      chunk=$(tail -c +$((last_pos+1)) "$file" | tr '\037' '\n')
      for rid in $(echo "$chunk" | grep -o 'perm-[0-9]\{10,\}' | sort -u); do
        case ",$approved," in *",$rid,"*) continue;; esac
        approved="$approved,$rid"
        curl -s -X POST "$BASE/api/permissions/$rid" \
          -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
          -d '{"action":"allow_session"}' > /dev/null
        echo "  [auto-approve] $rid -> allow_session"
      done
      last_pos=$size
    fi
    sleep 2
  done
}

# run_sse <输出文件> <curl参数...>: 拉取 SSE 流并配套自动批准守护
run_sse() {
  local file=$1; shift
  rm -f "$file"
  auto_approve "$file" > "${file%.txt}-approve.log" 2>&1 &
  local apid=$!
  curl -s -N --max-time "$SSE_MAX_TIME" "$@" > "$file"
  kill "$apid" 2>/dev/null || true
}

echo "=== 2. chat 意图（build）==="
CHAT=$(curl -s -X POST "$BASE/api/chat" -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d "{\"message\":\"$BRIEF_REQ\",\"mode\":\"build\",\"projectId\":0}")
BRIEF=$(echo "$CHAT" | python3 -c 'import sys,json;print(json.load(sys.stdin)["brief"])' 2>/dev/null)
if [ -z "$BRIEF" ]; then echo "FATAL: chat 未返回 brief: $CHAT"; exit 1; fi
echo "  brief: $BRIEF"

echo "=== 3. 生成流水线（SSE + 自动批准）==="
ENCODED=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$BRIEF")
run_sse "$TMPDIR_E2E/sse_gen.txt" "$BASE/api/generate?brief=$ENCODED&mode=build&t=$TOKEN"
PROJECT_ID=$(grep -o '"id":[0-9]*' "$TMPDIR_E2E/sse_gen.txt" | head -1 | grep -o '[0-9]*')
if [ -z "$PROJECT_ID" ] || [ "$PROJECT_ID" = "0" ]; then
  echo "FATAL: 生成完成但未解析到 project id（SSE 尾部如下）"
  tail -c 400 "$TMPDIR_E2E/sse_gen.txt"; exit 1
fi
echo "  PROJECT_ID=$PROJECT_ID"

echo "=== 4. 第一轮 refine（深色模式）==="
run_sse "$TMPDIR_E2E/sse_ref1.txt" -X POST "$BASE/api/projects/$PROJECT_ID/refine" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d '{"instruction":"加上深色模式切换按钮"}'

echo "=== 5. 第二轮 refine（分类筛选）==="
run_sse "$TMPDIR_E2E/sse_ref2.txt" -X POST "$BASE/api/projects/$PROJECT_ID/refine" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d '{"instruction":"添加任务分类筛选"}'

echo "=== 断言 A: 同一 project 行复用（列表仅 1 个）==="
PROJ_COUNT=$(curl -s "$BASE/api/projects" -H "Authorization: Bearer $TOKEN" \
  | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')
echo "  projects count = $PROJ_COUNT (expect 1)"
[ "$PROJ_COUNT" = "1" ] && ok "同一 project 行复用" || bad "项目列表数量异常: $PROJ_COUNT"

echo "=== 断言 B: 每轮对话保存为新 Message 记录 ==="
curl -s "$BASE/api/projects/$PROJECT_ID/messages" -H "Authorization: Bearer $TOKEN" > "$TMPDIR_E2E/msgs.json"
python3 - "$TMPDIR_E2E/msgs.json" <<'PYEOF'
import sys, json
ms = json.load(open(sys.argv[1]))
users = [m for m in ms if m['role'] == 'user']
runs  = [m for m in ms if m['role'] == 'assistant' and m.get('kind') == 'run']
print('  total messages = %d | user = %d | run = %d' % (len(ms), len(users), len(runs)))
for m in ms:
    print('   ', m['role'], m.get('kind', ''), m.get('status', ''), (m.get('text') or '')[:40].replace('\n', ' '))
sys.exit(0 if (len(users) == 3 and len(runs) == 3) else 1)
PYEOF
[ $? -eq 0 ] && ok "每轮对话保存为新 Message（3 user + 3 run）" || bad "Message 记录数量不符合预期（详见上方列表）"

echo "=== 断言 C: 两轮 refine 产物落地（预览含修改关键词）==="
PREVIEW=$(curl -s "$BASE/api/projects/$PROJECT_ID/preview?t=$TOKEN")
if echo "$PREVIEW" | grep -qi 'dark'; then ok "预览包含深色模式相关内容"; else bad "预览未发现 dark 关键词"; fi
if echo "$PREVIEW" | grep -qiE 'filter|筛选|分类'; then ok "预览包含分类筛选相关内容"; else bad "预览未发现 filter/筛选/分类 关键词"; fi

echo "=== 断言 D: chat 消息挂到同一 project ==="
curl -s -X POST "$BASE/api/chat" -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d "{\"message\":\"你好呀\",\"mode\":\"build\",\"projectId\":$PROJECT_ID}" > "$TMPDIR_E2E/chat2.json"
CHAT_REPLY=$(python3 -c 'import json;d=json.load(open("'$TMPDIR_E2E/chat2.json'"));print(d.get("reply","") or d.get("brief",""))' 2>/dev/null)
[ -n "$CHAT_REPLY" ] && ok "chat 返回 reply 并落库" || bad "chat 无 reply: $(head -c 120 "$TMPDIR_E2E/chat2.json")"
curl -s "$BASE/api/projects/$PROJECT_ID/messages" -H "Authorization: Bearer $TOKEN" > "$TMPDIR_E2E/msgs2.json"
python3 - "$TMPDIR_E2E/msgs2.json" <<'PYEOF'
import sys, json
ms = json.load(open(sys.argv[1]))
users = [m for m in ms if m['role'] == 'user' and m.get('kind') == 'text']
texts = [m for m in ms if m['role'] == 'assistant' and m.get('kind') == 'text']
print('  chat 后: user(text)=%d assistant(text)=%d' % (len(users), len(texts)))
sys.exit(0 if (len(users) == 4 and len(texts) >= 1) else 1)
PYEOF
[ $? -eq 0 ] && ok "chat 对话已挂到同一 project（user=4, 有回复）" || bad "chat 消息未正确挂载（详见上方计数）"

echo ""
echo "=========================================="
echo "结果汇总: PASS=$PASS FAIL=$FAIL  (测试用户: $EMAIL)"
[ "$FAIL" = "0" ] && echo "ALL CHECKS PASSED" || { echo "SOME CHECKS FAILED"; exit 1; }
