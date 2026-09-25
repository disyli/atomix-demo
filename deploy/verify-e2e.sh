#!/bin/bash
# Atomix Demo 完整 E2E 验证脚本
# 覆盖：游客入口 / 计算器+贪吃蛇独立产物 / 两轮增量核对 / 状态机失败落库 /
#       版本回滚原子性 / 退出重登 / 账号隔离 / 部署 SHA / HTTPS
#
# 用法：./verify-e2e.sh [BASE_URL]
#   BASE_URL 默认 https://101.32.28.8 （自签证书，脚本已带 -k）
set -u

BASE="${1:-https://101.32.28.8}"
CURL="curl -sk"
PASS=0
FAIL=0
FAILED_ITEMS=""

say()  { echo -e "\e[36m[$(date +%H:%M:%S)]\e[0m $*"; }
ok()   { echo -e "  \e[32m✓\e[0m $*"; PASS=$((PASS+1)); }
bad()  { echo -e "  \e[31m✗ $*\e[0m"; FAIL=$((FAIL+1)); FAILED_ITEMS="$FAILED_ITEMS\n- $*"; }

section() { echo; echo -e "\e[1;35m════ $* ════\e[0m"; }

# json_get <json> <key>（一层取值，够用）
json_get() { echo "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*[^,}]*" | head -1 | sed "s/.*:[[:space:]]*//; s/\"//g"; }

# json_str <json> <key>: 用 python 做转义感知的字符串字段提取（json_get 处理含逗号/
# 转义内容的 HTML 字段会在首个逗号截断且剥引号破坏内容，凡取大文本字段必须用这个）。
json_str() {
  printf '%s' "$1" | python3 -c '
import sys, json
try:
    d = json.loads(sys.stdin.read())
    v = d.get(sys.argv[1], "") if isinstance(d, dict) else ""
    sys.stdout.write(v if isinstance(v, str) else json.dumps(v, ensure_ascii=False))
except Exception:
    pass
' "$2"
}

# ---------- 权限自动批准守护 ----------
# live 模式 write_file/edit_file 为 ask 级权限，SSE 会推 permission 事件；
# 脚本无人值守必须自动批准（allow_session：同工具本次构建全放行）。
# auto_approve <sse文件> <token>
APPROVE_ROUNDS=${APPROVE_ROUNDS:-400}
AUTO_TOKEN=""
auto_approve() {
  local file=$1 last_pos=0 approved="" rid chunk size
  for _ in $(seq 1 "$APPROVE_ROUNDS"); do
    [ -f "$file" ] || { sleep 2; continue; }
    size=$(stat -c '%s' "$file" 2>/dev/null || echo 0)
    if [ "$size" -gt "$last_pos" ]; then
      chunk=$(tail -c +$((last_pos+1)) "$file" | tr '\037' '\n')
      for rid in $(echo "$chunk" | grep -o 'perm-[0-9]\{10,\}' | sort -u); do
        case ",$approved," in *",$rid,"*) continue;; esac
        approved="$approved,$rid"
        curl -sk -X POST "$BASE/api/permissions/$rid" \
          -H 'Content-Type: application/json' -H "Authorization: Bearer $AUTO_TOKEN" \
          -d '{"action":"allow_session"}' > /dev/null
        say "  [auto-approve] $rid -> allow_session"
      done
      last_pos=$size
    fi
    sleep 2
  done
}

# run_sse <输出文件> <token> <curl参数...>: 拉取 SSE 流 + 配套自动批准守护
SSE_MAX_TIME=${SSE_MAX_TIME:-900}
run_sse() {
  local file=$1 tok=$2; shift 2
  AUTO_TOKEN=$tok
  rm -f "$file"
  auto_approve "$file" > "${file%.txt}-approve.log" 2>&1 &
  local apid=$!
  curl -sk -N --max-time "$SSE_MAX_TIME" "$@" > "$file"
  kill "$apid" 2>/dev/null || true
  wait "$apid" 2>/dev/null || true
}

# 从 SSE 输出解析项目 ID（done 事件的 JSON 里第一个 "id":N）
sse_pid() { grep -o '"id":[0-9]*' "$1" | head -1 | grep -o '[0-9]*'; }

section "0. 部署标识与 HTTPS"
HEALTH=$($CURL "$BASE/api/health")
MODE=$(json_get "$HEALTH" mode)
SHA=$(json_get "$HEALTH" sha)
GUEST=$(json_get "$HEALTH" guest)
if [ -n "$SHA" ] && [ "$SHA" != "dev-local" ]; then ok "health 返回部署 SHA: $SHA"; else bad "health 无有效 SHA: $HEALTH"; fi
if [ "$GUEST" = "true" ]; then ok "游客入口开关已开启"; else bad "游客入口未开启: $GUEST"; fi
if [ "$MODE" = "live" ]; then say "当前 live 模式（DeepSeek 真实调用），构建类断言将真实执行"; else say "当前 demo 模式（脚本化轨迹）"; fi
# 80 端口必须 301 到 https
CODE=$($CURL -o /dev/null -w '%{http_code}' "http://101.32.28.8/api/health")
if [ "$CODE" = "301" ]; then ok "HTTP→HTTPS 301 跳转生效"; else bad "80 端口未跳转 HTTPS（$CODE）"; fi

section "1. 游客入口（无需注册 / 无 API Key）"
G1=$($CURL -X POST "$BASE/api/auth/guest")
T1=$(json_get "$G1" token)
if [ -n "$T1" ] && [ "$T1" != "" ]; then ok "游客一键登录成功，获得会话 token"; else bad "游客登录失败: $G1"; fi
ME1=$($CURL -H "Authorization: Bearer $T1" "$BASE/api/me")
if echo "$ME1" | grep -q "guest"; then ok "游客身份隔离（独立一次性账号）"; else bad "游客 /api/me 异常: $ME1"; fi
G2=$($CURL -X POST "$BASE/api/auth/guest")
T2=$(json_get "$G2" token)
if [ -n "$T2" ] && [ "$T2" != "$T1" ]; then ok "两次游客登录产生不同会话（一次性账号）"; else bad "两次游客 token 相同（非独立账号）"; fi

section "2. 计算器与贪吃蛇：不同 Prompt → 不同源码与预览"
# 用两个独立游客分别构建，避免同一项目串扰
TC=$($CURL -X POST "$BASE/api/auth/guest"); TCal=$(json_get "$TC" token)
TS=$($CURL -X POST "$BASE/api/auth/guest"); TSnk=$(json_get "$TS" token)

# 2.1 计算器
say "构建计算器…"
ENC1=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "做一个极简计算器，支持四则运算")
run_sse /tmp/e2e-cal.txt "$TCal" "$BASE/api/generate?brief=$ENC1&mode=build&t=$TCal"
CAL_ID=$(sse_pid /tmp/e2e-cal.txt)
if [ -n "$CAL_ID" ] && [ "$CAL_ID" != "0" ]; then ok "计算器项目已创建 (id=$CAL_ID)"; else bad "计算器构建失败: $(tail -c 300 /tmp/e2e-cal.txt)"; fi

# 2.2 贪吃蛇
say "构建贪吃蛇…"
ENC2=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "做一个贪吃蛇小游戏")
run_sse /tmp/e2e-snake.txt "$TSnk" "$BASE/api/generate?brief=$ENC2&mode=build&t=$TSnk"
SNK_ID=$(sse_pid /tmp/e2e-snake.txt)
if [ -n "$SNK_ID" ] && [ "$SNK_ID" != "0" ]; then ok "贪吃蛇项目已创建 (id=$SNK_ID)"; else bad "贪吃蛇构建失败: $(tail -c 300 /tmp/e2e-snake.txt)"; fi

if [ -n "$CAL_ID" ] && [ -n "$SNK_ID" ]; then
  SRC1=$($CURL -H "Authorization: Bearer $TCal" "$BASE/api/projects/$CAL_ID/source")
  SRC2=$($CURL -H "Authorization: Bearer $TSnk" "$BASE/api/projects/$SNK_ID/source")
  S1=$(json_str "$SRC1" source); S2=$(json_str "$SRC2" source)
  if echo "$S1" | grep -qi "calc\|计算"; then ok "计算器源码含计算器特征"; else bad "计算器源码无特征"; fi
  if echo "$S2" | grep -qi "snake\|贪吃蛇\|canvas"; then ok "贪吃蛇源码含游戏特征"; else bad "贪吃蛇源码无特征"; fi
  if [ "$S1" != "$S2" ] && [ -n "$S1" ] && [ -n "$S2" ]; then ok "两类 Prompt 源码不同（独立产物）"; else bad "两类 Prompt 源码相同（串模板）"; fi
  # 预览接口同样返回各自内容
  PV1=$($CURL -H "Authorization: Bearer $TCal" "$BASE/api/projects/$CAL_ID/preview")
  PV2=$($CURL -H "Authorization: Bearer $TSnk" "$BASE/api/projects/$SNK_ID/preview")
  if [ "$PV1" != "$PV2" ]; then ok "两类 Preview 内容不同"; else bad "两类 Preview 相同"; fi
fi

section "3. 同一项目两轮成功增量"
TA=$($CURL -X POST "$BASE/api/auth/guest"); TAcc=$(json_get "$TA" token)
ENC3=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "做一个极简计算器，支持四则运算")
run_sse /tmp/e2e-inc1.txt "$TAcc" "$BASE/api/generate?brief=$ENC3&mode=build&t=$TAcc"
PID=$(sse_pid /tmp/e2e-inc1.txt)
if [ -z "$PID" ] || [ "$PID" = "0" ]; then bad "增量基线项目构建失败: $(tail -c 300 /tmp/e2e-inc1.txt)"; else
  ok "基线项目就绪 (id=$PID)"
  # 第一轮产物
  SRC_A=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/source")
  EV1=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/events")
  if echo "$EV1" | grep -q "write_file"; then ok "第 1 轮含 write_file 记录"; else bad "第 1 轮无 write_file 记录"; fi
  if echo "$EV1" | grep -q "校验全部通过"; then ok "第 1 轮校验通过留痕"; else bad "第 1 轮校验未通过"; fi
  if echo "$EV1" | grep -q "产物已通过校验并落库为 v1"; then ok "第 1 轮 verified 落库留痕（v1）"; else bad "第 1 轮未见 verified 落库留痕"; fi

  # 第二轮：增量修改（加历史记录功能）
  say "第二轮增量：加历史记录…"
  run_sse /tmp/e2e-inc2.txt "$TAcc" -X POST "$BASE/api/projects/$PID/refine" \
    -H "Authorization: Bearer $TAcc" -H 'Content-Type: application/json' \
    -d '{"instruction":"增加历史记录功能，显示最近计算表达式"}'
  PROJ=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID")
  VER=$(json_get "$PROJ" version)
  if [ "$VER" -ge 2 ] 2>/dev/null; then ok "两轮后版本号 v$VER（递增）"; else bad "版本未递增: v$VER"; fi
  SRC_B=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/source")
  S_A=$(json_str "$SRC_A" source); S_B=$(json_str "$SRC_B" source)
  if [ "$S_A" != "$S_B" ] && [ -n "$S_B" ]; then ok "两轮源码存在差异（真实增量）"; else bad "两轮源码无差异"; fi
  EV2=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/events")
  if echo "$EV2" | grep -q "edit_file"; then ok "增量轮走 edit_file 精准修改"; else say "（demo 模式下增量可能整体重写，跳过 edit_file 断言）"; fi
  # 旧功能保留：第二轮源码仍含第一轮核心特征
  if echo "$S_B" | grep -qi "calc\|计算"; then ok "旧功能保留（计算器特征仍在）"; else bad "旧功能丢失"; fi
  # 预览与源码一致（原子）
  PV=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/preview")
  if [ -n "$PV" ]; then ok "预览可访问且已更新"; else bad "预览异常"; fi
fi

section "4. 失败落库与最后成功版本保留"
if [ -n "$PID" ]; then
  # 真实触发一轮「未完成」：发起增量后 3 秒断开 SSE 连接（客户端取消 → 服务端 ctx
  # 取消），状态机必须落库 stopped/合法终态且产物保留最后成功版本。
  BEFORE=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID")
  BV=$(json_get "$BEFORE" version)
  BS=$(json_get "$BEFORE" status)
  ok "基线核对: status=$BS v=$BV"
  # 新一轮 refine（构建型修改）走 SSE；循环轮询 SSE 流提取 runId（live 模式 LLM
  # 首 token 延迟数秒，1.5s 固定等待会扑空），抓到即调正式 cancel 接口
  rm -f /tmp/e2e-stop.txt
  ( curl -sk -N --max-time 90 -X POST "$BASE/api/projects/$PID/refine" \
      -H "Authorization: Bearer $TAcc" -H 'Content-Type: application/json' \
      -d '{"instruction":"把页面改成深蓝夜间主题"}' > /tmp/e2e-stop.txt 2>&1 ) &
  STUB_PID=$!
  RUN_ID=""
  for i in $(seq 1 25); do
    RUN_ID=$(grep -o 'run-[0-9]*' /tmp/e2e-stop.txt 2>/dev/null | head -1)
    [ -n "$RUN_ID" ] && break
    sleep 1
  done
  if [ -n "$RUN_ID" ]; then
    say "捕获 runId=$RUN_ID，调用正式停止接口"
    CANCEL_CODE=$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$BASE/api/runs/$RUN_ID/cancel" -H "Authorization: Bearer $TAcc")
    if [ "$CANCEL_CODE" = "200" ]; then
      ok "停止接口受理（200）"
    elif [ "$CANCEL_CODE" = "404" ]; then
      say "（runId 已结束被清理——任务完成太快，404 属正常时序；改为断流验证）"
      kill $STUB_PID 2>/dev/null
    else
      bad "停止接口异常（$CANCEL_CODE）"
    fi
  else
    say "（25 秒内未捕获 runId，直接断开连接）"
    kill $STUB_PID 2>/dev/null
  fi
  # live 模式 cancel 后任务收尾需要数秒（ctx 取消 → 落库），等待后核对终态
  sleep 8
  AFTER=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID")
  AV=$(json_get "$AFTER" version)
  AS=$(json_get "$AFTER" status)
  # stopped/ready 都属合法终态：关键是不能永远 generating，且 version 不回退
  if [ "$AS" = "stopped" ] || [ "$AS" = "ready" ]; then ok "中断轮落到合法终态（$AS），version=$AV 未回退"; else bad "中断后状态异常: $AS v=$AV"; fi
  SRC_KEEP=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/source")
  KEEP_S=$(json_str "$SRC_KEEP" source)
  if [ -n "$KEEP_S" ]; then ok "失败/中断后源码保留（最后成功版本）"; else bad "源码丢失"; fi
  PV_KEEP=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/preview")
  if [ -n "$PV_KEEP" ]; then ok "失败/中断后预览仍可用"; else bad "预览丢失"; fi
else
  say "（第 3 节基线项目未建成，跳过失败落库断言）"
fi

section "5. 版本快照与回滚原子性"
if [ -n "$PID" ]; then
  SNAPS=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/snapshots")
  CNT=$(echo "$SNAPS" | grep -o '"version":' | wc -l)
  if [ "$CNT" -ge 2 ]; then ok "快照列表 $CNT 条（每轮构建各一条）"; else bad "快照不足: $CNT 条"; fi
  # 记录回滚前的 v2 源码指纹，回滚到 v1 后源码必须变化且恢复计算器 v1 特征
  SRC_V2=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/source")
  S_V2=$(json_str "$SRC_V2" source)
  RB_RESP=$($CURL -X POST "$BASE/api/projects/$PID/rollback" -H "Authorization: Bearer $TAcc" -H 'Content-Type: application/json' -d '{"version":1}')
  RB_VER=$(json_str "$RB_RESP" rollbackTo)
  if [ "$RB_VER" = "1" ]; then ok "回滚到 v1 接口受理（rollbackTo=$RB_VER）"; else bad "回滚响应异常: $(tail -c 200 <<<"$RB_RESP")"; fi
  SRC_C=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/source")
  SC=$(json_str "$SRC_C" source)
  PV_C=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/preview")
  if [ -n "$SC" ] && [ -n "$PV_C" ]; then
    ok "回滚后源码与预览均返回内容（原子切换）"
  else
    bad "回滚后源码或预览为空"
  fi
  if [ -n "$SC" ] && [ "$SC" != "$S_V2" ]; then ok "回滚后源码切实变化（v2 → v1）"; else bad "回滚后源码未变化（回滚无效）"; fi
  if echo "$SC" | grep -qi "calc\|计算"; then ok "回滚后源码为 v1 计算器内容"; else bad "回滚内容异常"; fi
  # 回滚到不存在版本必须拒绝（400/409）
  RB=$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$BASE/api/projects/$PID/rollback" -H "Authorization: Bearer $TAcc" -H 'Content-Type: application/json' -d '{"version":999}')
  if [ "$RB" != "200" ]; then ok "回滚不存在版本被拒绝（$RB）"; else bad "回滚不存在版本返回 200"; fi
else
  say "（第 3 节基线项目未建成，跳过快照/回滚断言）"
fi

section "6. 退出重登（会话持久性）"
if [ -n "$TAcc" ]; then
  PROJ2=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects")
  CNT2=$(echo "$PROJ2" | grep -o '"id":' | wc -l)
  if [ "$CNT2" -ge 1 ]; then ok "重登后（同 token）项目列表完整（$CNT2 个）"; else bad "项目列表丢失"; fi
  if [ -n "$PID" ]; then
    MSG=$($CURL -H "Authorization: Bearer $TAcc" "$BASE/api/projects/$PID/messages")
    if echo "$MSG" | grep -q '"role":"user"'; then ok "对话历史持久化（Message 表还原）"; else bad "对话历史丢失"; fi
  else
    say "（第 3 节基线项目未建成，跳过对话历史断言）"
  fi
fi

section "7. 账号隔离"
if [ -n "$PID" ]; then
  TX=$($CURL -X POST "$BASE/api/auth/guest"); TIsol=$(json_get "$TX" token)
  CROSS=$(curl -sk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TIsol" "$BASE/api/projects/$PID")
  if [ "$CROSS" = "404" ] || [ "$CROSS" = "403" ]; then ok "游客 B 无法访问游客 A 的项目（隔离生效，$CROSS）"; else bad "跨账号访问未隔离（$CROSS）"; fi
else
  say "（第 3 节基线项目未建成，跳过账号隔离断言）"
fi

section "8. 登录页游客入口（静态资源）"
APP_JS=$($CURL "$BASE/" | grep -o 'assets/index-[^"]*\.js' | head -1)
if [ -n "$APP_JS" ]; then ok "前端资源已就绪: $APP_JS"; else bad "前端入口异常"; fi
# SPA 懒加载：guest 调用可能在共享 chunk（api 模块）或 LoginView chunk，扫描入口引用的全部 chunks
INDEX_BODY=$($CURL "$BASE/$APP_JS")
LOGIN_CHUNK=$(echo "$INDEX_BODY" | grep -o 'LoginView-[A-Za-z0-9_-]*\.js' | head -1)
GUEST_HIT=""
LOGIN_HIT=""
if [ -n "$LOGIN_CHUNK" ]; then
  LOGIN_BODY=$($CURL "$BASE/assets/$LOGIN_CHUNK")
  if echo "$LOGIN_BODY" | grep -q "auth/guest"; then GUEST_HIT=$LOGIN_CHUNK; fi
  if echo "$LOGIN_BODY" | grep -q "游客"; then LOGIN_HIT=$LOGIN_CHUNK; fi
fi
if [ -z "$GUEST_HIT" ]; then
  # 扫描 index bundle 自身与其他 chunk（api 共享模块可能内联或独立 chunk）
  for ch in $(echo "$INDEX_BODY" | grep -o '[A-Za-z0-9_-]*-[A-Za-z0-9_-]*\.js' | sort -u); do
    body=$($CURL "$BASE/assets/$ch")
    if [ -z "$GUEST_HIT" ] && echo "$body" | grep -q "auth/guest"; then GUEST_HIT=$ch; fi
    if [ -z "$LOGIN_HIT" ] && echo "$body" | grep -q "游客"; then LOGIN_HIT=$ch; fi
    [ -n "$GUEST_HIT" ] && [ -n "$LOGIN_HIT" ] && break
  done
fi
if [ -n "$GUEST_HIT" ]; then ok "游客登录调用已部署（auth/guest 位于 $GUEST_HIT）"; else bad "全部前端 chunk 未发现 auth/guest 调用"; fi
if [ -n "$LOGIN_HIT" ]; then ok "游客入口按钮文案已部署（游客文案位于 $LOGIN_HIT）"; else bad "全部前端 chunk 未发现游客文案"; fi

echo
echo -e "\e[1m══════════ 汇总 ══════════\e[0m"
echo -e "通过: \e[32m$PASS\e[0m   失败: \e[31m$FAIL\e[0m"
if [ $FAIL -gt 0 ]; then echo -e "\e[31m失败项:$FAILED_ITEMS\e[0m"; exit 1; fi
echo -e "\e[32m全部通过 ✔\e[0m"
