#!/usr/bin/env bash
# 集成测试：起一个一次性 Postgres 容器，串行跑全量测试，结束即销毁。
#
# 为什么需要它：db / agent / server / evidence 等包的测试都连真实 Postgres，
# 且共用一个库。`go test ./...` 默认并行执行多个包，包间互相看到对方写入的
# 公司/任务/配置，导致偶发失败（企业归因串号、任务被别的包 reload 等）。
# 这里用全新空库 + `-p 1` 串行，保证结果确定、可复现。
#
# 用法：
#   ./scripts/test-integration.sh                # 全量
#   ./scripts/test-integration.sh ./db ./agent   # 指定包
#   KEEP_PG=1 ./scripts/test-integration.sh      # 保留容器便于排查（端口 55432）
set -euo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"

NAME="artex-test-pg"
PORT="55432"
PW="testpw"

cleanup() {
  if [ "${KEEP_PG:-0}" != 1 ]; then
    docker rm -f "$NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" -p 127.0.0.1:${PORT}:5432 -e POSTGRES_PASSWORD="$PW" postgres:16-alpine >/dev/null
for i in $(seq 1 30); do
  docker exec "$NAME" pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done

export ARTEX_PG_DSN="postgres://postgres:${PW}@127.0.0.1:${PORT}/postgres?sslmode=disable"
export GOTOOLCHAIN=local

# -p 1：包级串行，杜绝跨包并发写同一个测试库。
go test -p 1 -count=1 -timeout 1200s "${@:-./...}"
