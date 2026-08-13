#!/usr/bin/env bash
# 一键部署脚本（Linux VPS / Docker Compose 环境）
# 用法:
#   curl -sL https://raw.githubusercontent.com/shdadahui/library-agent/main/scripts/deploy.sh | bash
#   或
#   bash scripts/deploy.sh [仓库地址] [部署目录]
set -euo pipefail

REPO="${1:-https://github.com/shdadahui/library-agent.git}"
APP_DIR="${2:-$HOME/library-agent}"
COMPOSE_FILE="docker-compose.yml"

echo "==> [1/5] 环境检查：docker / docker compose"
command -v docker >/dev/null || { echo "缺少 docker，请先安装：https://docs.docker.com/engine/install/"; exit 1; }
docker compose version >/dev/null 2>&1 || docker-compose version >/dev/null 2>&1 || { echo "缺少 docker compose 插件"; exit 1; }

echo "==> [2/5] 拉取代码 → $APP_DIR"
if [ -d "$APP_DIR/.git" ]; then
  cd "$APP_DIR" && git pull --ff-only
else
  git clone --depth 1 "$REPO" "$APP_DIR" && cd "$APP_DIR"
fi

echo "==> [3/5] 配置 DEEPSEEK_API_KEY"
if ! grep -q "DEEPSEEK_API_KEY" .env 2>/dev/null; then
  echo "DEEPSEEK_API_KEY=${DEEPSEEK_API_KEY:-}" >> .env
fi
# 也可手动编辑：nano $APP_DIR/.env

echo "==> [4/5] 启动服务（mysql + redis + app + nginx）"
docker compose up -d --build

echo "==> [5/5] 健康检查"
for i in $(seq 1 30); do
  if curl -sf http://localhost:8642/api/health >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
curl -s http://localhost:8642/api/health && echo && echo "✅ 部署完成！"
echo "   前端:   http://<服务器公网IP>"
echo "   健康:   http://<服务器公网IP>/api/health"
echo "   演示账号: alice/alice123、bob/bob123、admin/admin123（首次启动自动 seed 数据）"
echo "   注意: 若使用云安全组，请放行 80/443 端口"
