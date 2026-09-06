#!/bin/bash
# list-users.sh — 列出线上库用户邮箱与项目分布（只读），识别残留测试账号
set -e
docker run --rm -v /opt/atomix-data:/data alpine sh -c '
apk add -q sqlite >/dev/null 2>&1
echo "--- users ---"
sqlite3 /data/atomix.db "SELECT id, email FROM users ORDER BY id;"
echo "--- projects by user ---"
sqlite3 /data/atomix.db "SELECT user_id, COUNT(*), MAX(name) FROM projects GROUP BY user_id;"
'
