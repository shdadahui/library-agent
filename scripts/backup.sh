#!/usr/bin/env bash
# MySQL 定时备份脚本：docker exec mysqldump + gzip + 保留最近 7 天。
# 用法: bash scripts/backup.sh [备份目录]   （默认 data/backups）
# 建议 cron: 0 2 * * * cd /path/to/library-agent && bash scripts/backup.sh >> data/backups/backup.log 2>&1
set -euo pipefail

BACKUP_DIR="${1:-data/backups}"
MYSQL_CONTAINER="library-mysql"
DB_USER="library"
DB_PASS="libpass"
DB_NAME="library"
KEEP_DAYS=7

mkdir -p "$BACKUP_DIR"
STAMP=$(date +%Y%m%d-%H%M%S)
OUT="$BACKUP_DIR/library-${STAMP}.sql.gz"

echo "[$(date '+%F %T')] 开始备份 → $OUT"

docker exec "$MYSQL_CONTAINER" mysqldump \
  -u"$DB_USER" -p"$DB_PASS" \
  --single-transaction --routines --triggers \
  "$DB_NAME" 2>/dev/null | gzip > "$OUT"

# 备份文件大小
SIZE=$(du -h "$OUT" | cut -f1)
echo "[$(date '+%F %T')] 备份完成（$SIZE）：$OUT"

# 轮转：删除 N 天前的备份
find "$BACKUP_DIR" -name 'library-*.sql.gz' -mtime +$KEEP_DAYS -delete
echo "[$(date '+%F %T')] 已清理 ${KEEP_DAYS} 天前的备份"
