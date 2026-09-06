#!/bin/bash
# verify-db.sh — 复核清理后 DB 状态：用户/项目/消息/事件分布（只读）
set -e
docker run --rm -v /opt/atomix-data:/data alpine sh -c '
apk add -q sqlite >/dev/null 2>&1
echo "users:"; sqlite3 /data/atomix.db "SELECT COUNT(*) FROM users;"
echo "projects:"; sqlite3 /data/atomix.db "SELECT COUNT(*) FROM projects;"
echo "projects by user:"; sqlite3 /data/atomix.db "SELECT user_id, COUNT(*) FROM projects GROUP BY user_id;"
echo "messages:"; sqlite3 /data/atomix.db "SELECT COUNT(*) FROM messages;"
echo "events:"; sqlite3 /data/atomix.db "SELECT COUNT(*) FROM events;"
echo "sample project:"; sqlite3 /data/atomix.db "SELECT id, name, status, length(html) FROM projects WHERE user_id=1 LIMIT 3;"
'
