#!/usr/bin/env bash
set -euo pipefail
umask 077

# 用法：ARTEX_PG_DSN=... ARTEX_DATA_DIR=... ARTEX_STOPPED=1 ./scripts/backup-artex.sh create <新备份目录>
# 恢复：ARTEX_RESTORE_CONFIRM_DB=<目标数据库名> ARTEX_STOPPED=1 ./scripts/backup-artex.sh restore <备份目录>

# 输出安全失败信息并以非零状态结束脚本。
fail() {
  printf '[artex-backup] %s\n' "$1" >&2
  exit 1
}

mode="${1:-}"
bundle_arg="${2:-}"
[[ "$mode" == "create" || "$mode" == "restore" ]] || fail "用法：$0 create|restore 备份目录"
[[ -n "$bundle_arg" ]] || fail "必须指定备份目录"
[[ -n "${ARTEX_PG_DSN:-}" ]] || fail "请通过 ARTEX_PG_DSN 指定 PostgreSQL 连接串"
[[ "${ARTEX_STOPPED:-}" == "1" ]] || fail "请先停止 ARTEX 服务，并设置 ARTEX_STOPPED=1 确认数据目录不会变化"
command -v psql >/dev/null 2>&1 || fail "缺少 psql"

data_arg="${ARTEX_DATA_DIR:-./data}"
data_parent_arg="$(dirname "$data_arg")"
data_name="$(basename "$data_arg")"
[[ -d "$data_parent_arg" ]] || fail "数据目录父路径不存在：$data_parent_arg"
data_parent="$(cd -P "$data_parent_arg" && pwd)"
data_dir="$data_parent/$data_name"
[[ -d "$data_dir" && ! -L "$data_dir" ]] || fail "数据目录必须存在且不能是符号链接：$data_dir"

bundle_parent_arg="$(dirname "$bundle_arg")"
mkdir -p "$bundle_parent_arg"
bundle_parent="$(cd -P "$bundle_parent_arg" && pwd)"
bundle="$bundle_parent/$(basename "$bundle_arg")"
case "$bundle/" in
  "$data_dir/"*) fail "备份目录不能放在 ARTEX 数据目录中" ;;
esac

if [[ "$mode" == "create" ]]; then
  command -v pg_dump >/dev/null 2>&1 || fail "缺少 pg_dump"
  [[ ! -e "$bundle" && ! -L "$bundle" ]] || fail "备份目录已存在：$bundle"
  tmp="$(mktemp -d "$bundle.tmp.XXXXXX")"
  trap 'rm -rf "$tmp"' EXIT
  database="$(psql "$ARTEX_PG_DSN" -Atqc 'SELECT current_database()')" || fail "无法连接 PostgreSQL"
  [[ -n "$database" ]] || fail "无法读取当前数据库名称"
  pg_dump --format=custom --no-owner --file "$tmp/postgres.dump" "$ARTEX_PG_DSN"
  tar -czf "$tmp/data.tar.gz" -C "$data_parent" "$data_name"
  {
    printf 'format=artex-full-backup-v1\n'
    printf 'created_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'database=%s\n' "$database"
    printf 'data_directory=%s\n' "$data_dir"
  } > "$tmp/manifest.txt"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && sha256sum postgres.dump data.tar.gz > SHA256SUMS)
  else
    command -v shasum >/dev/null 2>&1 || fail "缺少 sha256sum 或 shasum"
    (cd "$tmp" && shasum -a 256 postgres.dump data.tar.gz > SHA256SUMS)
  fi
  mv "$tmp" "$bundle"
  trap - EXIT
  printf '[artex-backup] 已创建整库备份：%s\n' "$bundle"
  exit 0
fi

command -v pg_restore >/dev/null 2>&1 || fail "缺少 pg_restore"
[[ -d "$bundle" && ! -L "$bundle" ]] || fail "备份目录不存在或是符号链接：$bundle"
[[ -f "$bundle/manifest.txt" && -f "$bundle/SHA256SUMS" && -f "$bundle/postgres.dump" && -f "$bundle/data.tar.gz" ]] || fail "备份目录缺少必要文件"
grep -qx 'format=artex-full-backup-v1' "$bundle/manifest.txt" || fail "不支持的备份格式"
backup_database="$(sed -n 's/^database=//p' "$bundle/manifest.txt")"
backup_data_dir="$(sed -n 's/^data_directory=//p' "$bundle/manifest.txt")"
current_database="$(psql "$ARTEX_PG_DSN" -Atqc 'SELECT current_database()')" || fail "无法连接 PostgreSQL"
[[ -n "$current_database" && "$current_database" == "$backup_database" ]] || fail "目标数据库与备份数据库不一致"
[[ "${ARTEX_RESTORE_CONFIRM_DB:-}" == "$current_database" ]] || fail "恢复会覆盖数据库；请设置 ARTEX_RESTORE_CONFIRM_DB=$current_database 明确确认"
[[ "$data_dir" == "$backup_data_dir" ]] || fail "ARTEX_DATA_DIR 与备份记录的路径不一致"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$bundle" && sha256sum -c SHA256SUMS) || fail "备份校验和不匹配"
else
  (cd "$bundle" && shasum -a 256 -c SHA256SUMS) || fail "备份校验和不匹配"
fi

stage="$(mktemp -d "$data_parent/.artex-restore.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
tar -tzf "$bundle/data.tar.gz" > "$stage/archive-files.txt" || fail "无法读取数据目录归档"
while IFS= read -r entry; do
  case "$entry" in
    /*|..|../*|*/../*|*/..) fail "备份中存在不安全路径：$entry" ;;
  esac
done < "$stage/archive-files.txt"
tar -xzf "$bundle/data.tar.gz" -C "$stage"
restored_data="$stage/$data_name"
[[ -d "$restored_data" && ! -L "$restored_data" ]] || fail "备份中找不到预期的数据目录"

timestamp="$(date -u '+%Y%m%dT%H%M%SZ')"
previous_data="$data_dir.pre-restore.$timestamp"
mv "$data_dir" "$previous_data" || fail "无法暂存当前数据目录"
if ! mv "$restored_data" "$data_dir"; then
  mv "$previous_data" "$data_dir" || fail "替换数据目录失败，且无法恢复原目录：$previous_data"
  fail "无法安装备份数据目录"
fi
if ! pg_restore --clean --if-exists --no-owner --exit-on-error --single-transaction --dbname "$ARTEX_PG_DSN" "$bundle/postgres.dump"; then
  rm -rf "$data_dir"
  if ! mv "$previous_data" "$data_dir"; then
    fail "数据库恢复失败，且无法恢复原数据目录：$previous_data"
  fi
  fail "PostgreSQL 恢复失败；数据库事务已回滚，原数据目录已恢复"
fi
rm -rf "$stage"
trap - EXIT
printf '[artex-backup] 恢复完成；原数据目录保留在：%s\n' "$previous_data"
