#!/bin/bash
# cleanup-verify-data.sh — 清理线上验证产生的测试数据（verify_* 用户及其项目/消息）与临时脚本
# SQL 字符串一律用单引号，避免传输链转义问题
set -e
rm -f /tmp/verify-persist.sh /tmp/verify-e2e-v4.sh /tmp/verify-e2e-final.sh /tmp/verify-e2e-v5.sh /tmp/verify-e2e-v6.sh \
      /tmp/verify-e2e-run.log /tmp/verify-e2e-run2.log /tmp/verify-e2e-run3.log /tmp/smoke-source.sh /tmp/debug-source.sh /tmp/smoke-dl.html
docker run --rm -v /opt/atomix-data:/data alpine sh -c '
apk add -q sqlite >/dev/null 2>&1
sqlite3 /data/atomix.db "DELETE FROM messages WHERE user_id IN (SELECT id FROM users WHERE email LIKE '\''verify_%'\'');"
sqlite3 /data/atomix.db "DELETE FROM events WHERE project_id IN (SELECT id FROM projects WHERE user_id IN (SELECT id FROM users WHERE email LIKE '\''verify_%'\''));"
sqlite3 /data/atomix.db "DELETE FROM projects WHERE user_id IN (SELECT id FROM users WHERE email LIKE '\''verify_%'\'');"
sqlite3 /data/atomix.db "DELETE FROM projects WHERE name = '\'''\'';"
sqlite3 /data/atomix.db "DELETE FROM users WHERE email LIKE '\''verify_%'\'';"
echo "DB cleanup done"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM users;"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM projects;"
'
echo "ALL CLEANUP DONE"
