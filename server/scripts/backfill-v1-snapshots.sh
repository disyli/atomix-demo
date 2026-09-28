#!/bin/bash
# backfill-v1-snapshots.sh — 一次性数据修复：为历史遗留的、没有任何快照的 ready 项目
# 补建 v1 快照（内容取自 projects.html，与项目当前源码一致），使其具备可回滚数据源。
# 背景：CommitSuccess 事务上线前，早期项目走的是旧的 CreateSnapshot 调用点缺失或
# 中途变更，导致部分 status=ready 的项目在 snapshots 表里没有对应记录。
# 幂等：只对 snapshots 表里完全没有记录的项目补建，重复执行不会产生重复快照
# （WHERE 子查询已排除有记录的项目；对已补建过的项目第二次运行不会再匹配到）。
set -e
docker run --rm -v /opt/atomix-data:/data alpine sh -c '
apk add -q sqlite >/dev/null 2>&1
echo "修复前：无快照的 ready 项目数"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM projects WHERE status = '"'"'ready'"'"' AND id NOT IN (SELECT DISTINCT project_id FROM snapshots);"

sqlite3 /data/atomix.db "
INSERT INTO snapshots (project_id, version, html, label, status, created_at_ms)
SELECT id, 1, html, '"'"'历史数据修复：补建 v1 快照'"'"', '"'"'done'"'"', updated_at_ms
FROM projects
WHERE status = '"'"'ready'"'"'
  AND id NOT IN (SELECT DISTINCT project_id FROM snapshots)
  AND html != '"'"''"'"';
"

echo "修复后：无快照的 ready 项目数（应为 0，html 为空的极端情况除外）"
sqlite3 /data/atomix.db "SELECT COUNT(*) FROM projects WHERE status = '"'"'ready'"'"' AND id NOT IN (SELECT DISTINCT project_id FROM snapshots) AND html != '"'"''"'"';"

echo "同步 project.version：老项目从未走过快照逻辑时 version 仍为 0，补建 v1 快照后需对齐为 1，"
echo "否则版本面板会显示当前版本不在快照列表里（current 判断失真，但不影响回滚可用性）"
sqlite3 /data/atomix.db "
UPDATE projects SET version = 1
WHERE version = 0
  AND id IN (SELECT project_id FROM snapshots WHERE version = 1 AND label = '"'"'历史数据修复：补建 v1 快照'"'"');
"

echo "受影响项目校验：version 与快照最大 version 应一致（若原 version 已 >1 需人工检查）"
sqlite3 /data/atomix.db "
SELECT p.id, p.version AS project_version, MAX(s.version) AS max_snapshot_version
FROM projects p JOIN snapshots s ON s.project_id = p.id
GROUP BY p.id
HAVING p.version != MAX(s.version);
"
'
