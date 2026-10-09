#!/usr/bin/env bash
# KinoTV 发布备份清理：按"保留最近 N 份"裁掉发布目录下的手工备份。
#
# 为什么不并进 kinotv-backup.sh：那个管的是"数据备份"（库 / 配置 / 资源），有 done
# 标记和完整的恢复链路；这里管的是"发布回滚物"（前端目录、旧二进制、发布前的手工
# 快照）。它的生命周期跟"上次发布"绑定，而不是跟天数绑定——按份数保留比按天数更
# 贴合实际，也避免"半个月没发布，旧的又过了保留期被删，结果一次都回滚不了"。
#
# 安全边界：
#   * 只删名字匹配已知模式、且位于发布目录内的条目；认不出来的条目一律不碰。
#   * 每个类别强制至少保留 1 份，--keep-* 传 0 也不生效。
#   * 排序优先用文件名里的时间戳，取不到才退回 mtime。理由见 sort_key_of 的注释：
#     发布备份是 cp -a / mv 出来的，目录 mtime 继承自源目录，比"备份是什么时候做的"早得多。
#   * 首次上机先跑 --dry-run 看清单，确认无误再去掉它。
#
# 退出码：0 正常；1 有条目删不掉（其余照删）；2 参数错误。
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

RELEASE_DIR="${KINOTV_RELEASE_DIR:-/opt/kinotv}"
KEEP_RELEASES="${KINOTV_RELEASE_KEEP:-3}"
KEEP_DB="${KINOTV_RELEASE_KEEP_DB:-3}"
KEEP_MEDIA="${KINOTV_RELEASE_KEEP_MEDIA:-2}"
KEEP_SNAPSHOTS="${KINOTV_RELEASE_KEEP_SNAPSHOTS:-2}"
# 线上 web/static 里旧 chunk 的保留天数。0 = 关掉这条策略。
WEB_STALE_DAYS="${KINOTV_WEB_STALE_DAYS:-30}"
LOG="${KINOTV_PRUNE_LOG:-/var/log/kinotv-prune.log}"
DRY_RUN=0
QUIET=0

usage() {
    cat <<'USAGE'
用法: kinotv-prune-releases.sh [选项]

  --release-dir DIR        发布目录，默认 /opt/kinotv
  --keep-releases N        web.bak-* 与 kinotv-server.bak-* 保留份数，默认 3
  --keep-db N              *.db.bak-* 库快照保留份数，默认 3
  --keep-media N           resources.bak-*.tgz 保留份数，默认 2
  --keep-snapshots N       backups/ 下发布前手工快照保留份数，默认 2
  --web-stale-days N       线上 web/static 里旧 chunk 的保留天数，默认 30，0 表示关闭
  --log PATH               日志文件，默认 /var/log/kinotv-prune.log（不可写时只输出到 stdout）
  --dry-run                只打印将删除的清单，不实际删除
  --quiet                  只写日志，不输出到 stdout

环境变量同名：KINOTV_RELEASE_DIR / KINOTV_RELEASE_KEEP / KINOTV_RELEASE_KEEP_DB /
KINOTV_RELEASE_KEEP_MEDIA / KINOTV_RELEASE_KEEP_SNAPSHOTS / KINOTV_WEB_STALE_DAYS /
KINOTV_PRUNE_LOG
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --release-dir) RELEASE_DIR="$2"; shift 2 ;;
        --keep-releases) KEEP_RELEASES="$2"; shift 2 ;;
        --keep-db) KEEP_DB="$2"; shift 2 ;;
        --keep-media) KEEP_MEDIA="$2"; shift 2 ;;
        --keep-snapshots) KEEP_SNAPSHOTS="$2"; shift 2 ;;
        --web-stale-days) WEB_STALE_DAYS="$2"; shift 2 ;;
        --log) LOG="$2"; shift 2 ;;
        --dry-run) DRY_RUN=1; shift ;;
        --quiet) QUIET=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

# 发布目录不存在说明配置写错了。这里不能用 mkdir -p 兜底：真的建出一个空目录，
# 脚本会"成功清理 0 份"，而问题被静默吞掉。
if [ ! -d "$RELEASE_DIR" ]; then
    printf '发布目录不存在：%s\n' "$RELEASE_DIR" >&2
    exit 1
fi
case "$RELEASE_DIR" in
    /) printf '拒绝在根目录上运行\n' >&2; exit 1 ;;
esac

# 日志可能因为权限不可写（本地演练时 /var/log 就是这样），探一次不行就只走 stdout，
# 而不是让每天一次的定时任务自己抛权限错误。
if ! { printf '' >> "$LOG"; } 2>/dev/null; then
    LOG=""
fi

say() {
    local line
    line="$(kinotv_log "$*")"
    [ -z "$LOG" ] || printf '%s\n' "$line" >> "$LOG" 2>/dev/null || true
    [ "$QUIET" -eq 1 ] || printf '%s\n' "$line"
}

# 同一时刻只允许一个清理在跑。与备份脚本同理：两个进程同时按"保留 N 份"决策，
# 各自看到的清单不同，可能把对方刚判定要留下的那份删掉。
LOCK_DIR="$RELEASE_DIR/.prune.lock"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    LOCK_MTIME="$(kinotv_file_mtime "$LOCK_DIR" 2>/dev/null || echo 0)"
    LOCK_AGE=$(( $(date +%s) - LOCK_MTIME ))
    # 上个进程被 kill -9 会留下永久锁，超过 6 小时视为残留而不是并发。
    if [ "$LOCK_AGE" -gt 21600 ]; then
        say "发现超过 6 小时的陈旧锁，视为残留并接管：${LOCK_DIR}"
        rm -rf "$LOCK_DIR" || true
        mkdir "$LOCK_DIR" 2>/dev/null || { say "无法取得清理锁，退出"; exit 1; }
    else
        say "已有清理任务在运行（锁 ${LOCK_DIR}，${LOCK_AGE}s），本次跳过"
        exit 0
    fi
fi
cleanup_lock() { rmdir "$LOCK_DIR" 2>/dev/null || true; }
trap cleanup_lock EXIT

TOTAL_FILES=0
TOTAL_BYTES=0
FAILED=0

# 条目体积，只用于日志与汇总。目录走 du，文件走 du 也一样（du 对单文件返回自身大小）。
bytes_of() {
    local kb=""
    kb="$(du -sk "$1" 2>/dev/null | awk 'NR==1 {print $1}')" || kb=""
    printf '%s' $(( ${kb:-0} * 1024 ))
}

# 把 epoch 秒转成 YYYYMMDDHHMMSS，好和文件名里的时间戳直接比大小。
# GNU 的 date -r 是"读某个文件的 mtime"，BSD 的 date -r 才是"读 epoch"，两边都探一次。
stamp_of_epoch() {
    local epoch="$1" out=""
    out="$(date -r "$epoch" +%Y%m%d%H%M%S 2>/dev/null || true)"
    case "$out" in
        [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]) ;;
        *) out="$(date -d "@$epoch" +%Y%m%d%H%M%S 2>/dev/null || true)" ;;
    esac
    case "$out" in
        [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]) printf '%s' "$out" ;;
        *) printf '00000000000000' ;;
    esac
}

# 排序键：优先取文件名里的 yyyymmdd[-]hhmm[ss]，取不到再退回 mtime。
#
# 这里不能用 mtime 当主排序依据。发布备份是 cp -a / mv 出来的，目录 mtime 会继承源目录，
# 所以它记录的是"被备份的那份 web 目录最后改动的时间"，不是"备份是什么时候做的"。
# 实测 web.bak-20261005-1516 的名字是 10-05 15:16，目录 mtime 却停在当天 06:06；
# kinotv-server.bak-20261005-1516 更极端，mtime 是 10-04 20:58，比它自己早一天。
# 按 mtime 排会把最新的一份回滚物排到很旧的位置——真出事时恰好没有它。
# 文件名是发布时用 date 生成的，才是可靠的"备份时刻"。
sort_key_of() {
    local path="$1" base stamp=""
    base="$(basename "$path")"
    stamp="$(printf '%s' "$base" | sed -nE 's/.*(20[0-9]{6})-?([0-9]{2})([0-9]{2})([0-9]{2})?.*/\1\2\3\4/p' | head -1)"
    if [ "${#stamp}" -eq 12 ]; then
        stamp="${stamp}00"
    fi
    if [ "${#stamp}" -eq 14 ]; then
        printf '%s' "$stamp"
        return 0
    fi
    stamp_of_epoch "$(kinotv_file_mtime "$path" 2>/dev/null || echo 0)"
}

# 列出某类别下的候选条目，按备份时刻从新到旧。
list_newest_first() {
    local pattern="$1" p
    local -a matches=()
    shopt -s nullglob
    for p in "$RELEASE_DIR"/$pattern; do
        [ -e "$p" ] || continue
        matches+=("$p")
    done
    shopt -u nullglob
    [ "${#matches[@]}" -gt 0 ] || return 0
    for p in "${matches[@]}"; do
        printf '%s\t%s\n' "$(sort_key_of "$p")" "$p"
    done | sort -rn -k1,1 | cut -f2-
}

# 裁掉一个类别里排在保留份数之后的条目。
prune_category() {
    local label="$1" keep="$2" pattern="$3"
    local -a items=()
    local path size index kept removed freed
    # 至少留 1 份：把"全删光"这种后果交给人工决定，脚本不提供这个开关。
    case "$keep" in
        ''|*[!0-9]*) keep=1 ;;
    esac
    [ "$keep" -ge 1 ] || keep=1

    while IFS= read -r path; do
        [ -n "$path" ] || continue
        items+=("$path")
    done < <(list_newest_first "$pattern")

    if [ "${#items[@]}" -eq 0 ]; then
        say "  ${label}：没有可清理的条目"
        return 0
    fi

    index=0; kept=0; removed=0; freed=0
    for path in "${items[@]}"; do
        index=$((index + 1))
        if [ "$index" -le "$keep" ]; then
            kept=$((kept + 1))
            continue
        fi
        size="$(bytes_of "$path")"
        if [ "$DRY_RUN" -eq 1 ]; then
            say "  [dry-run] 将删除 ${label}：$(basename "$path")（$(kinotv_human_size "$size")）"
        elif rm -rf -- "$path" 2>/dev/null; then
            say "  删除 ${label}：$(basename "$path")（$(kinotv_human_size "$size")）"
        else
            say "  删除失败 ${label}：$(basename "$path")"
            FAILED=1
            continue
        fi
        removed=$((removed + 1))
        freed=$((freed + size))
    done

    TOTAL_FILES=$((TOTAL_FILES + removed))
    TOTAL_BYTES=$((TOTAL_BYTES + freed))
    say "  ${label}：保留 ${kept} 份，清理 ${removed} 份（$(kinotv_human_size "$freed")）"
}

# 线上 web/static 里的旧 chunk：发布时是故意留下来的，按文件年龄淘汰。
#
# 为什么不并进上面的"按份数保留"：那几类是发布回滚物，生命周期跟着"上次发布"走；
# 这些旧 chunk 是给"还没刷新的标签页"用的备份通道，生命周期跟着"用户标签页能活多久"走。
# 按份数算的话，一天连发三次就把两天前的 chunk 删光了，正好是长开标签页的那批人受害。
# 所以按天数，而且默认 30 天——比任何人开着不刷新的标签页都长。
#
# 安全边界：
#   * 只在 $RELEASE_DIR/web/static 里面找，活目录的入口文件（index.html）不在这个范围。
#   * 只删普通文件，不删目录；判定用 mtime，tar 解包会保留构建时间，所以新旧可分。
#   * 一次删不掉就记失败并继续，不因为一个文件让整轮清理半途而废。
prune_stale_web_assets() {
    local dir="$RELEASE_DIR/web/static"
    local days="$WEB_STALE_DAYS"
    local path size removed=0 freed=0

    case "$days" in
        ''|*[!0-9]*) days=30 ;;
    esac
    if [ "$days" -le 0 ]; then
        say "  线上静态残留：已关闭（--web-stale-days 0）"
        return 0
    fi
    if [ ! -d "$dir" ]; then
        say "  线上静态残留：没有 ${dir}，跳过"
        return 0
    fi

    while IFS= read -r path; do
        [ -n "$path" ] || continue
        size="$(bytes_of "$path")"
        if [ "$DRY_RUN" -eq 1 ]; then
            say "  [dry-run] 将删除 线上静态残留：${path#"$dir"/}（$(kinotv_human_size "$size")）"
        elif rm -f -- "$path" 2>/dev/null; then
            say "  删除 线上静态残留：${path#"$dir"/}（$(kinotv_human_size "$size")）"
        else
            say "  删除失败 线上静态残留：${path#"$dir"/}"
            FAILED=1
            continue
        fi
        removed=$((removed + 1))
        freed=$((freed + size))
    done < <(find "$dir" -type f -mtime "+${days}" -print 2>/dev/null)

    TOTAL_FILES=$((TOTAL_FILES + removed))
    TOTAL_BYTES=$((TOTAL_BYTES + freed))
    say "  线上静态残留：清理 ${removed} 个文件（$(kinotv_human_size "$freed")），保留 ${days} 天内的"
}

MODE="正式删除"
[ "$DRY_RUN" -eq 1 ] && MODE="dry-run"
say "线上静态残留保留天数：${WEB_STALE_DAYS}（0 表示关闭）"
say "开始清理发布备份：dir=${RELEASE_DIR} mode=${MODE}"
say "保留份数：前端/二进制=${KEEP_RELEASES} 库快照=${KEEP_DB} 资源归档=${KEEP_MEDIA} 手工快照=${KEEP_SNAPSHOTS}"

prune_category "前端发布备份" "$KEEP_RELEASES" 'web.bak-*'
prune_category "后端发布备份" "$KEEP_RELEASES" 'kinotv-server.bak-*'
# 两个库的快照分开成两个类别：合成一类的话，一次发布只产出一对，"保留 3 份"
# 实际只会留下 1.5 轮，而画布库与账号库的回滚价值是各自独立的。
prune_category "画布库快照" "$KEEP_DB" 'open_ai_canvas.db.bak-*'
prune_category "账号库快照" "$KEEP_DB" 'kinotv-auth.db.bak-*'
prune_category "资源归档" "$KEEP_MEDIA" 'resources.bak-*.tgz'
prune_category "发布前手工快照" "$KEEP_SNAPSHOTS" 'backups/*'
prune_stale_web_assets

if [ "$FAILED" -ne 0 ]; then
    say "清理完成但有删除失败项：共处理 ${TOTAL_FILES} 份，约 $(kinotv_human_size "$TOTAL_BYTES")"
    exit 1
fi
say "清理完成：共处理 ${TOTAL_FILES} 份，约 $(kinotv_human_size "$TOTAL_BYTES")"
exit 0
