#!/bin/bash
# Atomix Demo 数据库每日备份：SQLite 安全快照 → 保留最近 14 份
# 由 deploy.sh 首次部署时注册到 /etc/cron.d/atomix-backup（每天 04:30），也可手动执行
set -euo pipefail

DATA_DIR=${ATOMIX_DATA_DIR:-/opt/atomix-data}
BACKUP_DIR=${ATOMIX_BACKUP_DIR:-/opt/atomix-backup}
KEEP=14

[ -f "$DATA_DIR/atomix.db" ] || { echo "!! 数据库不存在: $DATA_DIR/atomix.db"; exit 1; }
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

TS=$(date +%Y%m%d-%H%M%S)
DEST="$BACKUP_DIR/atomix-$TS.db"

# .backup 是 SQLite 在线备份 API：即使写入中也能得到一致性快照，比直接 cp 文件安全
docker exec atomix-demo sh -c "echo '.backup /tmp/backup.db' | /app/atomix -q 2>/dev/null || true" >/dev/null 2>&1 || true
# 容器内无 sqlite3 CLI 时回落宿主机 python3（Ubuntu 24.04 自带 sqlite3 模块）
if docker exec atomix-demo sh -c "command -v sqlite3" >/dev/null 2>&1; then
  docker exec atomix-demo sqlite3 /app/data/atomix.db ".backup /tmp/backup.db"
  docker cp atomix-demo:/tmp/backup.db "$DEST"
  docker exec atomix-demo rm -f /tmp/backup.db
else
  python3 - "$DATA_DIR/atomix.db" "$DEST" <<'PY'
import sqlite3, sys
src, dst = sys.argv[1], sys.argv[2]
s = sqlite3.connect(src)
d = sqlite3.connect(dst)
with d:
    s.backup(d)
d.close(); s.close()
PY
fi

chmod 600 "$DEST"
echo "==> 备份完成: $DEST ($(du -h "$DEST" | cut -f1))"

# 滚动保留最近 $KEEP 份
ls -1t "$BACKUP_DIR"/atomix-*.db 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f
echo "==> 当前保留 $(ls -1 "$BACKUP_DIR"/atomix-*.db 2>/dev/null | wc -l) 份备份"
