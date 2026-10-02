#!/usr/bin/env bash
# KinoTV 备份：SQLite 在线快照 + 配置 + 用户资源。
#
# 每次运行产出一个"整份"目录，最后才写 done 标记：
#
#   <backup-dir>/20261003-033001/
#     open_ai_canvas.db
#     kinotv-auth.db
#     config/<local-model-config.json|plugin_registry.json|.settings-key>
#     media/resources.tar.gz
#     manifest.tsv     每行 <相对路径>\t<sha256>\t<字节数>\t<权限>
#     done             只有全部成功才出现
#
# 为什么按目录而不是平铺一堆带时间戳的文件：恢复时"哪几个文件属于同一次备份"必须
# 无从猜测。平铺布局下漏掉一两个文件也能"恢复成功"，而残缺的备份比没有备份更危险。
#
# done 标记是恢复侧的唯一准入条件：没有它，恢复脚本拒绝使用这份备份。
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

DATA_DIR="${KINOTV_DATA_DIR:-/opt/kinotv/data}"
BACKUP_DIR="${KINOTV_BACKUP_DIR:-/root/backups/kinotv}"
KEEP_DAYS="${KINOTV_BACKUP_KEEP_DAYS:-7}"
KEEP_MEDIA_DAYS="${KINOTV_BACKUP_KEEP_MEDIA_DAYS:-3}"
INCLUDE_MEDIA=1
QUIET=0

usage() {
    cat <<'USAGE'
用法: kinotv-backup.sh [选项]

  --data-dir DIR        数据目录，默认 /opt/kinotv/data
  --backup-dir DIR      备份根目录，默认 /root/backups/kinotv
  --keep-days N         数据库与配置保留天数，默认 7
  --keep-media-days N   用户资源保留天数，默认 3
  --skip-media          跳过 resources（用于高频轻量备份）
  --quiet               只写备份日志，不输出到 stdout

环境变量同名：KINOTV_DATA_DIR / KINOTV_BACKUP_DIR /
KINOTV_BACKUP_KEEP_DAYS / KINOTV_BACKUP_KEEP_MEDIA_DAYS
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --backup-dir) BACKUP_DIR="$2"; shift 2 ;;
        --keep-days) KEEP_DAYS="$2"; shift 2 ;;
        --keep-media-days) KEEP_MEDIA_DAYS="$2"; shift 2 ;;
        --skip-media) INCLUDE_MEDIA=0; shift ;;
        --quiet) QUIET=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

mkdir -p "$BACKUP_DIR"
LOG="$BACKUP_DIR/backup.log"
say() {
    kinotv_log "$*" >> "$LOG"
    [ "$QUIET" -eq 1 ] || kinotv_log "$*"
}

STAMP="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$BACKUP_DIR/$STAMP"
FAILED=0
ARTIFACT_COUNT=0

# 同一时刻只允许一个备份在跑：两个 .backup 同时写同一块盘会互相拖慢，
# 更糟的是并发裁剪可能删掉另一份刚写一半的产物。
LOCK_DIR="$BACKUP_DIR/.lock"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    LOCK_AGE=0
    if [ -d "$LOCK_DIR" ]; then
        LOCK_MTIME="$(kinotv_file_mtime "$LOCK_DIR" 2>/dev/null || echo 0)"
        LOCK_AGE=$(( $(date +%s) - LOCK_MTIME ))
    fi
    # 上个进程被 kill -9 会留下永久锁，超过 6 小时视为残留而不是并发。
    if [ "$LOCK_AGE" -gt 21600 ]; then
        say "发现超过 6 小时的陈旧锁，视为残留并接管：$LOCK_DIR"
        rm -rf "$LOCK_DIR" || true
        mkdir "$LOCK_DIR" 2>/dev/null || { say "无法取得备份锁，退出"; exit 1; }
    else
        say "已有备份在运行（锁 ${LOCK_DIR}，${LOCK_AGE}s），本次跳过"
        exit 0
    fi
fi
cleanup_lock() { rmdir "$LOCK_DIR" 2>/dev/null || true; }
trap cleanup_lock EXIT

mkdir -p "$RUN_DIR/config"
: > "$RUN_DIR/manifest.tsv"

record() {
    # 记录一个产物：相对路径、校验和、字节数、权限。
    local rel="$1" abs="$2"
    local sum size mode
    sum="$(kinotv_sha256 "$abs")"
    size="$(kinotv_file_size "$abs")"
    mode="$(kinotv_file_mode "$abs")"
    printf '%s\t%s\t%s\t%s\n' "$rel" "$sum" "$size" "$mode" >> "$RUN_DIR/manifest.tsv"
    ARTIFACT_COUNT=$((ARTIFACT_COUNT + 1))
    say "  $rel $(kinotv_human_size "$size")"
}

say "开始备份：data=$DATA_DIR backup=$BACKUP_DIR run=$STAMP"

# ---- 1. SQLite 在线快照 ----
# 必须用 .backup 而不是 cp：开启 WAL 时主库文件不含尚未 checkpoint 的事务，
# 直接拷出来的库看起来正常、实际上丢最近一段写入。
for DB in open_ai_canvas kinotv-auth; do
    SRC="$DATA_DIR/$DB.db"
    DST="$RUN_DIR/$DB.db"
    if [ ! -f "$SRC" ]; then
        say "备份失败：$SRC 不存在"
        FAILED=1
        continue
    fi
    rm -f "$DST"
    if ! sqlite3 "$SRC" ".backup '$DST'" 2>>"$LOG"; then
        say "备份失败：$DB sqlite3 .backup 报错"
        rm -f "$DST"
        FAILED=1
        continue
    fi
    # 把归档副本的日志模式归一成 delete：.backup 会连带复制源库的 WAL 设置，
    # 留下 -wal/-shm 边车文件。没有边车的单文件才方便异地存放、只读挂载和用任意
    # sqlite 版本打开——备份的可恢复性不该依赖"记得一起拷那两个文件"。
    sqlite3 "$DST" 'pragma journal_mode=delete;' >/dev/null 2>&1 || true
    SRC_SIZE="$(kinotv_file_size "$SRC")"
    DST_SIZE="$(kinotv_file_size "$DST")"
    INTEGRITY="$(sqlite3 "$DST" 'pragma integrity_check;' 2>/dev/null | head -1)"
    # 归一之后若还留着有内容的 -wal，说明 checkpoint 没做完，这份快照不能算数。
    WAL="$DST-wal"
    if [ -f "$WAL" ] && [ "$(kinotv_file_size "$WAL")" -gt 0 ]; then
        say "备份失败：$DB 的 WAL 未合并（$(kinotv_file_size "$WAL") 字节），快照不完整"
        FAILED=1
        continue
    fi
    rm -f "$WAL" "$DST-shm" 2>/dev/null || true
    if [ "$INTEGRITY" != "ok" ]; then
        say "备份失败：$DB integrity_check=$INTEGRITY"
        FAILED=1
        continue
    fi
    # 快照明显小于源库通常意味着源库此刻正在被写坏，或路径指向了错误文件。
    if [ "$DST_SIZE" -lt $((SRC_SIZE / 2)) ]; then
        say "备份可疑：$DB 快照 $DST_SIZE 字节，源库 $SRC_SIZE 字节"
        FAILED=1
        continue
    fi
    record "$DB.db" "$DST"
done

# ---- 2. 配置类文件 ----
# .settings-key 少了，库里的渠道密钥和资源签名全部解不开；它必须与库同批备份。
for F in local-model-config.json plugin_registry.json .settings-key; do
    SRC="$DATA_DIR/$F"
    [ -f "$SRC" ] || continue
    DST="$RUN_DIR/config/$F"
    cp -p "$SRC" "$DST"
    if [ "$F" = ".settings-key" ]; then
        MODE="$(kinotv_file_mode "$DST")"
        if [ "$MODE" != "600" ]; then
            say "备份失败：.settings-key 权限是 ${MODE}，应为 600"
            FAILED=1
            continue
        fi
    fi
    record "config/$F" "$DST"
done

# ---- 3. 用户资源 ----
# 生成产物是花钱跑出来的，丢了无法用数据库重建。
if [ "$INCLUDE_MEDIA" -eq 1 ] && [ -d "$DATA_DIR/resources" ]; then
    MEDIA="$RUN_DIR/media/resources.tar.gz"
    mkdir -p "$RUN_DIR/media"
    rm -f "$MEDIA"
    if tar czf "$MEDIA" -C "$DATA_DIR" resources 2>>"$LOG"; then
        # tar 写入中途失败也会留下一个能打开、但内容不全的包，必须做一次可读性校验。
        if ! tar tzf "$MEDIA" >/dev/null 2>&1; then
            say "备份失败：resources.tar.gz 无法完整读取"
            rm -f "$MEDIA"
            FAILED=1
        else
            record "media/resources.tar.gz" "$MEDIA"
        fi
    else
        say "备份失败：resources 打包失败"
        rm -f "$MEDIA"
        FAILED=1
    fi
elif [ "$INCLUDE_MEDIA" -eq 1 ]; then
    say "提示：$DATA_DIR/resources 不存在，跳过资源备份"
fi

# ---- 4. 收尾 ----
if [ "$FAILED" -ne 0 ]; then
    say "本次备份失败，丢弃 ${STAMP}（残缺目录不会被恢复侧使用，也不该占空间）"
    rm -rf "$RUN_DIR"
    exit 1
fi

{
    printf 'created_at=%s\n' "$(kinotv_iso_now)"
    printf 'run=%s\n' "$STAMP"
    printf 'data_dir=%s\n' "$DATA_DIR"
    printf 'artifacts=%s\n' "$ARTIFACT_COUNT"
    printf 'media=%s\n' "$INCLUDE_MEDIA"
} > "$RUN_DIR/manifest.meta"
: > "$RUN_DIR/done"

TOTAL="$(du -sh "$RUN_DIR" 2>/dev/null | awk '{print $1}')"
say "备份完成：$ARTIFACT_COUNT 个产物，共 ${TOTAL:-?}"

# ---- 5. 裁剪 ----
# 只删本脚本自己产出的目录结构。找不到 done 的旧目录按更短期限清掉：
# 它们要么是更早版本的残留，要么是失败留下的，留着只会让"最新备份"看起来比实际新。
prune() {
    local dir name mtime age
    for dir in "$BACKUP_DIR"/*/; do
        [ -d "$dir" ] || continue
        name="$(basename "$dir")"
        case "$name" in
            [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]) ;;
            *) continue ;;
        esac
        mtime="$(kinotv_file_mtime "$dir")"
        age=$(( ( $(date +%s) - mtime ) / 86400 ))
        if [ ! -f "$dir/done" ]; then
            if [ "$age" -ge 1 ]; then
                say "清理残缺备份：$name"
                rm -rf "$dir"
            fi
            continue
        fi
        # 资源包单独按更短的周期裁剪：它比数据库大一个数量级，用同一个保留期会把
        # 磁盘吃光，而"库还在但媒体没了"远好过"因为媒体占满盘所以什么都备不了"。
        if [ -d "$dir/media" ] && [ "$age" -ge "$KEEP_MEDIA_DAYS" ]; then
            say "清理过期资源包：${name}（${age} 天）"
            rm -rf "$dir/media"
            # 留个标记，恢复侧据此把"媒体已按策略清理"和"这次备份本来就没带媒体"区分开。
            : > "$dir/media-pruned"
        fi
        if [ "$age" -ge "$KEEP_DAYS" ]; then
            say "清理过期备份：${name}（${age} 天）"
            rm -rf "$dir"
        fi
    done
}
prune
exit 0
