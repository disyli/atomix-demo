#!/bin/bash
set -e
BASE="http://127.0.0.1/api"
TS=$(date +%s)
EMAIL="react_e2e_$TS@atomix.test"
PASS="test123456"

echo "=== 1. register+login ==="
curl -s -m 10 -X POST "$BASE/auth/register" -H "Content-Type: application/json" -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" > /dev/null
TOKEN=$(curl -s -m 10 -X POST "$BASE/auth/login" -H "Content-Type: application/json" -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
echo "token ok: ${#TOKEN} chars"

echo "=== 2. generate via ReAct (SSE event sequence) ==="
curl -s -m 60 -N "$BASE/generate?brief=make%20a%20todo%20list%20app" -H "Authorization: Bearer $TOKEN" > /tmp/gen_sse.txt
echo "--- event types observed ---"
grep -o '^event:[a-z]*' /tmp/gen_sse.txt | sort | uniq -c
echo "--- first 12 event payloads (stage field) ---"
grep '^data:' /tmp/gen_sse.txt | head -12 | cut -c1-130
echo "--- done event present? ---"
grep -c '^event:done' /tmp/gen_sse.txt || echo 0

echo "=== 3. verify project persisted with correct events ==="
PID=$(curl -s -m 10 "$BASE/projects" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;ps=json.load(sys.stdin);print(ps[0]['id'] if ps else '')")
echo "project id: $PID"
curl -s -m 10 "$BASE/projects/$PID" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;p=json.load(sys.stdin);print('status:',p['status'],'| template:',p['template'],'| name:',p['name'])"
echo "--- DB event stages (should include plan/build/verify/done, on THIS project) ---"
curl -s -m 10 "$BASE/projects/$PID/events" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;es=json.load(sys.stdin);print(len(es),'events');[print(' ',e['stage'],'|',e['level'],'|',e['message'][:70]) for e in es]"

echo "=== 4. refine (iterative edit via same loop) ==="
curl -s -m 60 -N -X POST "$BASE/projects/$PID/refine" -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" -d '{"instruction":"把标题改成深色模式"}' > /tmp/refine_sse.txt
grep -o '^event:[a-z]*' /tmp/refine_sse.txt | sort | uniq -c
curl -s -m 10 "$BASE/projects/$PID" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;p=json.load(sys.stdin);print('after refine status:',p['status'])"
echo "REACT_E2E_DONE"
