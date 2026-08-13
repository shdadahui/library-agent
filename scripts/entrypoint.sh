#!/bin/sh
# 容器入口：幂等 seed（建表 + 演示数据，已存在则跳过）→ 启动服务
set -e

MYSQL_DSN="library:libpass@tcp(mysql:3306)/library?parseTime=true&charset=utf8mb4&loc=Local"

echo "[entrypoint] 初始化/校验数据库数据…"
/app/seed -driver mysql -dsn "$MYSQL_DSN"

echo "[entrypoint] 启动服务…"
exec /app/server -config /app/config.docker.json
