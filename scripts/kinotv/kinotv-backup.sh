#!/usr/bin/env bash
# KinoTV 备份：SQLite 在线快照 + 配置 + 用户资源。
#
# 每次运行产出一个目录，最后才写 done 标记：
#
#   <backup-dir>/20261003-033001/
#     open_ai_canvas.db
#     kinotv-auth.db
#     config/<local-model-config.json|plugin_registry.json|.settings-key>
#     media/resources/...            用户素材，与上一份共用硬链接
#     manifest.tsv     每行 <相对路径>\t<sha256>\t<字节数>\t<权限>
#     done             只有全部成功才出现
#
# 为什么按目录而不是平铺一堆带时间戳的文件：恢复时"哪几个文件属于同一次备份"必须
# 无从猜测。平铺布局下漏掉一两个文件也能"恢复成功"，而残缺的备份比没有备份更危险。
#
# 素材为什么是硬链接树而不是一个 tar：素材只增不减，整份打包等于每天复制一遍全部
# 素材。素材 5G 时，保留 3 天就是 15G 备份，而每天真正新增的可能只有几十兆——磁盘
# 会先被备份副本撑满，而不是被素材本身。改成 rsync --link-dest 之后，没变动的文件
# 与上一份共用同一个 inode，备份体积只按"真正变了多少"增长。
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
MEDIA_FILES=0
MEDIA_LINKED=0
MEDIA_NEW_BYTES=0
MEDIA_TOTAL_BYTES=0

# 素材增量依赖 rsync。缺了要在动手之前说清楚，而不是备到一半才发现——
# 那时候 RUN_DIR 已经建出来、库快照也做完了一半。
if [ "$INCLUDE_MEDIA" -eq 1 ] && [ -d "$DATA_DIR/resources" ] && ! command -v rsync >/dev/null 2>&1; then
    say "备份失败：没有找到 rsync，素材增量备份无法进行"
    exit 1
fi

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
#
# 整树逐文件进清单：媒体是这里唯一会到 GB 级的部分，只记一个包级别的校验和，
# 一旦包坏了就没法知道坏在哪几个文件、能不能只丢一小部分。逐文件记录换来的是
# "恢复前就能点名到具体哪个素材对不上"。
if [ "$INCLUDE_MEDIA" -eq 1 ] && [ -d "$DATA_DIR/resources" ]; then
    MEDIA_DIR="$RUN_DIR/media"
    MEDIA_TREE="$MEDIA_DIR/resources"
    mkdir -p "$MEDIA_DIR"
    # 上一份还带着素材的备份，它的树拿来当 --link-dest：内容一样的文件直接共用 inode，
    # 不占新增空间。素材被裁掉的旧备份不会被选中（test -d 过滤掉了）。
    PREV_MEDIA="$(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-*' \
        ! -path "$RUN_DIR" -exec test -d '{}/media/resources' \; -print 2>/dev/null | sort | tail -1)"
    RSYNC_ARGS=(-a)
    [ -n "$PREV_MEDIA" ] && RSYNC_ARGS+=("--link-dest=$PREV_MEDIA/media/resources")
    if ! rsync "${RSYNC_ARGS[@]}" "$DATA_DIR/resources/" "$MEDIA_TREE/" >>"$LOG" 2>&1; then
        say "备份失败：素材同步失败"
        FAILED=1
    else
        # 上一份里留着、这一份源里已经没有的素材要清掉——否则"恢复出来比现在多"
        # 同样说不清。必须先清再进清单，不然清单会点到已经不存在的文件。
        while IFS= read -r -d '' path; do
            rel="${path#"$MEDIA_TREE"/}"
            [ -e "$DATA_DIR/resources/$rel" ] || rm -f -- "$path"
        done < <(find "$MEDIA_TREE" -type f -print0)
        while IFS= read -r -d '' path; do
            rel="${path#"$MEDIA_TREE"/}"
            sum="$(kinotv_sha256 "$path")"
            size="$(kinotv_file_size "$path")"
            mode="$(kinotv_file_mode "$path")"
            printf 'media/resources/%s\t%s\t%s\t%s\n' "$rel" "$sum" "$size" "$mode" >> "$RUN_DIR/manifest.tsv"
            ARTIFACT_COUNT=$((ARTIFACT_COUNT + 1))
            MEDIA_FILES=$((MEDIA_FILES + 1))
            MEDIA_TOTAL_BYTES=$((MEDIA_TOTAL_BYTES + size))
            # 和上一份的同一个文件 inode 相同 ⇒ 这一份没有为它多占一个字节。
            if [ -n "$PREV_MEDIA" ] && [ -f "$PREV_MEDIA/media/resources/$rel" ] \
                && [ "$(kinotv_file_inode "$path")" = "$(kinotv_file_inode "$PREV_MEDIA/media/resources/$rel")" ]; then
                MEDIA_LINKED=$((MEDIA_LINKED + 1))
            else
                MEDIA_NEW_BYTES=$((MEDIA_NEW_BYTES + size))
            fi
        done < <(find "$MEDIA_TREE" -type f -print0)
        SRW="$(find "$DATA_DIR/resources" -type f | wc -l | tr -d ' ')"
        if [ "$MEDIA_FILES" != "$SRW" ]; then
            say "备份失败：素材文件数不符（源 ${SRW}，备份 ${MEDIA_FILES}）"
            FAILED=1
        else
            say "  media/resources/ $MEDIA_FILES 个文件共 $(kinotv_human_size "$MEDIA_TOTAL_BYTES")，其中 $MEDIA_LINKED 个与上一份共用，本次新增写入 $(kinotv_human_size "$MEDIA_NEW_BYTES")"
        fi
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

# 不报 du 出来的目录大小：素材是硬链接，du 会把共用 inode 也算进来，
# 那个数字会让人误以为备份又涨了一份。
if [ "$INCLUDE_MEDIA" -eq 1 ] && [ "$MEDIA_FILES" -gt 0 ]; then
    say "备份完成：$ARTIFACT_COUNT 个产物，其中素材 $MEDIA_FILES 个、本次新增写入 $(kinotv_human_size "$MEDIA_NEW_BYTES")"
else
    say "备份完成：$ARTIFACT_COUNT 个产物"
fi

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
