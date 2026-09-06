#!/bin/bash
# cleanup-legacy-test-users.sh — 清理历史验证脚本残留账号（保留 1/25/26/27 真实用户及其数据）
set -e
docker run --rm -v /opt/atomix-data:/data alpine sh -c '
apk add -q sqlite >/dev/null 2>&1
sqlite3 /data/atomix.db "DELETE FROM messages WHERE user_id NOT IN (1,25,26,27);"
sqlite3 /data/atomix.db "DELETE FROM events WHERE project_id IN (SELECT id FROM projects WHERE user_id NOT IN (1,25,26,27));"
sqlite3 /data/atomix.db "DELETE FROM projects WHERE user_id NOT IN (1,25,26,27);"
sqlite3 /data/atomix.db "DELETE FROM attachments WHERE user_id NOT IN (1,25,26,27);"
sqlite3 /data/atomix.db "DELETE FROM users WHERE id NOT IN (1,25,26,27);"
echo "--- after cleanup ---"
sqlite3 /data/atomix.db "SELECT id, email FROM users;"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM projects;"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM messages;"
'
echo "LEGACY TEST DATA CLEANED"
