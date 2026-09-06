#!/bin/bash
# smoke-source.sh — 源码接口快速冒烟：用回归脚本早前创建的项目验证 source API
# 用法: BASE=http://127.0.0.1 TOKEN=<token> PROJECT_ID=<id> bash smoke-source.sh
set -u
BASE="${BASE:-http://127.0.0.1}"
TOKEN="${TOKEN:?need TOKEN}"
PROJECT_ID="${PROJECT_ID:?need PROJECT_ID}"

echo "--- 1. source JSON ---"
curl -s "$BASE/api/projects/$PROJECT_ID/source" -H "Authorization: Bearer $TOKEN" | python3 -c '
import sys, json
d = json.load(sys.stdin)
ok = bool(d.get("source")) and d.get("lines", 0) > 50 and "<html" in d["source"].lower() and d.get("filename","").endswith(".html")
print("name=%s filename=%s lines=%s size=%s" % (d.get("name"), d.get("filename"), d.get("lines"), d.get("size")))
print("SOURCE_JSON_OK" if ok else "SOURCE_JSON_BAD")
'
echo "--- 2. download headers ---"
curl -s -o /tmp/smoke-dl.html -D - "$BASE/api/projects/$PROJECT_ID/source?download=1&t=$TOKEN" | tr -d '\r' | grep -iE 'content-disposition|content-type|HTTP/'
echo "downloaded_bytes=$(stat -c '%s' /tmp/smoke-dl.html)"
echo "--- 3. downloaded content sanity ---"
head -c 60 /tmp/smoke-dl.html | tr '\n' ' '; echo ""
grep -qi '<html' /tmp/smoke-dl.html && echo "DOWNLOAD_CONTENT_OK" || echo "DOWNLOAD_CONTENT_BAD"
rm -f /tmp/smoke-dl.html
