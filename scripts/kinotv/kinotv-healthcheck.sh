#!/usr/bin/env bash
# KinoTV 健康巡检：服务能不能用、备份是不是还在、恢复演练是不是还在按期做。
#
# 与 /root/check-server.sh 的分工：那个脚本看"机器和进程还在不在"，这个看"服务真的能
# 应答吗、出事时能不能恢复"。端口开着不等于 HTTP 能应答——进程活着但数据库锁死、
# 迁移卡住、插件加载失败，netstat 一样显示端口在听。
#
# 告警去重：只在 OK→ALERT 时发，持续 ALERT 每 N 小时重发一次，ALERT→OK 发恢复通知。
# 没有这一层，5 分钟一次的定时任务会把告警出口刷爆，最后所有人把它静音。
#
# 退出码：0 正常；1 有异常（同时已发出告警）；2 参数错误。
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

BASE_URL="${KINOTV_HEALTH_BASE_URL:-http://127.0.0.1:8090}"
PROBE_PATH="${KINOTV_HEALTH_PROBE_PATH:-/api/health/live}"
READY_PATH="${KINOTV_HEALTH_READY_PATH:-/api/health/ready}"
# 降级探活：/api/health/live 的匿名白名单是随某个版本才加的，在那之前它返回 401。
# 这时若直接判 ALERT，等于让"还没升级二进制"伪装成"服务挂了"，巡检会立刻失去可信度。
# 退到一个本来就匿名的接口，仍能证明 HTTP 栈与账号库可用。
FALLBACK_PATH="${KINOTV_HEALTH_FALLBACK_PATH:-/api/auth/agreements}"
BACKUP_DIR="${KINOTV_BACKUP_DIR:-/root/backups/kinotv}"
DISK_PATH="${KINOTV_HEALTH_DISK_PATH:-/}"
BACKUP_MAX_AGE_HOURS="${KINOTV_HEALTH_BACKUP_MAX_AGE_HOURS:-26}"
VERIFY_MAX_AGE_DAYS="${KINOTV_HEALTH_VERIFY_MAX_AGE_DAYS:-8}"
DISK_MAX_PERCENT="${KINOTV_HEALTH_DISK_MAX_PERCENT:-90}"
RENOTIFY_HOURS="${KINOTV_HEALTH_RENOTIFY_HOURS:-6}"
STATE_DIR="${KINOTV_HEALTH_STATE_DIR:-/var/lib/kinotv}"
LOG="${KINOTV_HEALTH_LOG:-/var/log/kinotv-health.log}"
ALERT_CMD="${KINOTV_ALERT_CMD:-$SCRIPT_DIR/kinotv-alert.py}"
ENABLE_ALERT=1

usage() {
    cat <<'USAGE'
用法: kinotv-healthcheck.sh [选项]

  --base-url URL            后端地址，默认 http://127.0.0.1:8090
  --backup-dir DIR          备份根目录，默认 /root/backups/kinotv
  --disk-path PATH          要检查的挂载点，默认 /
  --backup-max-age-hours N  备份最大年龄，默认 26（每天一次，留 2 小时余量）
  --verify-max-age-days N   恢复演练最大间隔，默认 8
  --disk-max-percent N      磁盘使用率上限，默认 90
  --renotify-hours N        持续异常时重发间隔，默认 6
  --state-dir DIR           状态文件目录，默认 /var/lib/kinotv
  --log PATH                巡检日志路径，默认 /var/log/kinotv-health.log
  --no-alert                只巡检不告警（手工排查时用）
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --base-url) BASE_URL="$2"; shift 2 ;;
        --backup-dir) BACKUP_DIR="$2"; shift 2 ;;
        --disk-path) DISK_PATH="$2"; shift 2 ;;
        --backup-max-age-hours) BACKUP_MAX_AGE_HOURS="$2"; shift 2 ;;
        --verify-max-age-days) VERIFY_MAX_AGE_DAYS="$2"; shift 2 ;;
        --disk-max-percent) DISK_MAX_PERCENT="$2"; shift 2 ;;
        --renotify-hours) RENOTIFY_HOURS="$2"; shift 2 ;;
        --state-dir) STATE_DIR="$2"; shift 2 ;;
        --log) LOG="$2"; shift 2 ;;
        --no-alert) ENABLE_ALERT=0; shift ;;
        -h|--help) usage; exit 0 ;;
        *) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

mkdir -p "$STATE_DIR" 2>/dev/null || true

# 日志与状态目录都可能因为权限不可写（本地演练时 /var/log 就是这样）。先探一次，
# 不行就退到临时目录，而不是让一个巡检脚本自己在 stderr 上抛权限错误。
if ! { printf '' >> "$LOG"; } 2>/dev/null; then
    LOG="${TMPDIR:-/tmp}/kinotv-health.log"
fi
if ! { printf '' > "$STATE_DIR/.write-probe"; } 2>/dev/null; then
    STATE_DIR="${TMPDIR:-/tmp}"
fi
rm -f "$STATE_DIR/.write-probe" 2>/dev/null || true

PROBLEMS=()

record_ok()   { printf '  OK    %-16s %s\n' "$1" "${2:-}"; }
record_bad()  { printf '  ALERT %-16s %s\n' "$1" "${2:-}"; PROBLEMS+=("$1：$2"); }

# ---- 1. 存活与就绪 ----
# 分开看：进程活着但依赖没就绪（数据库连不上、迁移没跑完）是两种不同的故障。
http_code_of() {
    local url="$1" code=""
    if command -v curl >/dev/null 2>&1; then
        # curl 自己在连不上时也会经 -w 输出 000，所以不能再叠加一个兜底 echo，
        # 否则会拼出 000000 这种把"没连上"和"状态码"混在一起的读数。
        code="$(curl -s -o /dev/null -w '%{http_code}' -m 8 "$url" 2>/dev/null || true)"
    fi
    printf '%s' "${code:-000}"
}
live_code="$(http_code_of "$BASE_URL$PROBE_PATH")"
DEGRADED=""
if [ "$live_code" = "401" ] && [ -n "$FALLBACK_PATH" ]; then
    if [ "$(http_code_of "$BASE_URL$FALLBACK_PATH")" = "200" ]; then
        DEGRADED="$FALLBACK_PATH"
        live_code=200
    fi
fi
ready_code="$(http_code_of "$BASE_URL$READY_PATH")"

if [ "$live_code" = "200" ] && [ -n "$DEGRADED" ]; then
    record_ok "存活探针" "http=200（降级：$PROBE_PATH 返回 401，改用 ${DEGRADED}）"
elif [ "$live_code" = "200" ]; then
    record_ok "存活探针" "http=$live_code"
else
    record_bad "存活探针" "http=${live_code}（进程可能已挂）"
fi
if [ "$ready_code" = "200" ]; then
    record_ok "就绪探针" "http=$ready_code"
elif [ "$ready_code" = "503" ]; then
    record_bad "就绪探针" "http=503（进程在跑但依赖未就绪）"
elif [ "$ready_code" = "401" ]; then
    # 同上：这是"还没升级二进制"，不是"服务病了"。记下来但不判故障。
    record_ok "就绪探针" "http=401（当前二进制未开放匿名就绪探针，跳过）"
else
    record_bad "就绪探针" "http=$ready_code"
fi

# ---- 2. 备份新鲜度 ----
LATEST_RUN=""
LATEST_RUN_MTIME=0
if [ -d "$BACKUP_DIR" ]; then
    while IFS= read -r dir; do
        [ -n "$dir" ] || continue
        [ -f "$dir/done" ] || continue
        mtime="$(kinotv_file_mtime "$dir")"
        if [ "$mtime" -gt "$LATEST_RUN_MTIME" ]; then
            LATEST_RUN_MTIME="$mtime"
            LATEST_RUN="$dir"
        fi
    done < <(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d 2>/dev/null)
fi
if [ -z "$LATEST_RUN" ]; then
    record_bad "备份新鲜度" "找不到任何完成的备份（${BACKUP_DIR}）"
else
    age_hours=$(( ( $(date +%s) - LATEST_RUN_MTIME ) / 3600 ))
    if [ "$age_hours" -le "$BACKUP_MAX_AGE_HOURS" ]; then
        record_ok "备份新鲜度" "$(basename "$LATEST_RUN") ${age_hours}h 前"
    else
        record_bad "备份新鲜度" "$(basename "$LATEST_RUN") 已是 ${age_hours}h 前（上限 ${BACKUP_MAX_AGE_HOURS}h）"
    fi
fi

# ---- 3. 恢复演练新鲜度 ----
VERIFY_FILE="$BACKUP_DIR/last-verify"
verify_age_days=""
if [ -f "$VERIFY_FILE" ]; then
    verify_age_days="$(python3 - "$VERIFY_FILE" <<'PY' 2>/dev/null || true
import re, sys, time
try:
    text = open(sys.argv[1]).read()
    match = re.search(r'verified_at=(\S+)', text)
    if not match:
        raise ValueError('missing')
    stamp = match.group(1)
    # Python 3.6 的 fromisoformat 不认 '+08:00' 这种偏移，手工归一。
    stamp = re.sub(r'([+-]\d{2}):(\d{2})$', r'\1\2', stamp)
    parsed = time.mktime(time.strptime(stamp[:19], '%Y-%m-%dT%H:%M:%S'))
    offset = 0
    m = re.search(r'([+-])(\d{2})(\d{2})$', stamp)
    if m:
        offset = (int(m.group(2)) * 3600 + int(m.group(3)) * 60) * (1 if m.group(1) == '+' else -1)
    print(int((time.time() - (parsed - offset)) // 86400))
except Exception:
    pass
PY
)"
fi
if [ -z "$verify_age_days" ]; then
    record_bad "恢复演练" "没有可用的演练记录（缺 ${VERIFY_FILE}）"
elif [ "$verify_age_days" -le "$VERIFY_MAX_AGE_DAYS" ]; then
    record_ok "恢复演练" "${verify_age_days} 天前验证过"
else
    record_bad "恢复演练" "上次验证在 ${verify_age_days} 天前（上限 ${VERIFY_MAX_AGE_DAYS} 天）"
fi

# ---- 4. 磁盘 ----
if df -P "$DISK_PATH" >/dev/null 2>&1; then
    disk_used="$(df -P "$DISK_PATH" | awk 'NR==2 {gsub(/%/,"",$5); print $5}')"
    if [ -n "$disk_used" ] && [ "$disk_used" -le "$DISK_MAX_PERCENT" ]; then
        record_ok "磁盘" "${disk_used}%（上限 ${DISK_MAX_PERCENT}%）"
    else
        record_bad "磁盘" "${disk_used}% 超过上限 ${DISK_MAX_PERCENT}%"
    fi
else
    record_bad "磁盘" "无法读取 $DISK_PATH 的用量"
fi

# ---- 汇总与告警 ----
STAMP="$(kinotv_iso_now)"
if [ "${#PROBLEMS[@]}" -eq 0 ]; then
    STATUS=OK
    SUMMARY="正常"
else
    STATUS=ALERT
    SUMMARY=""
    for item in "${PROBLEMS[@]}"; do
        if [ -z "$SUMMARY" ]; then SUMMARY="$item"; else SUMMARY="$SUMMARY; $item"; fi
    done
fi
LINE="[$STAMP] $STATUS $SUMMARY"
{
    printf '%s\n' "$LINE"
    printf '  live=%s ready=%s disk=%s%% backup=%s verify=%s\n' \
        "$live_code${DEGRADED:+ (degraded)}" "$ready_code" "${disk_used:-?}" \
        "$([ -n "$LATEST_RUN" ] && basename "$LATEST_RUN" || echo none)" "${verify_age_days:-none}"
} >> "$LOG" 2>/dev/null || printf '%s\n' "$LINE"

STATE_FILE="$STATE_DIR/health.state"
LAST_ALERT_FILE="$STATE_DIR/health.lastalert"
LAST_STATUS="$(cat "$STATE_FILE" 2>/dev/null || echo OK)"

if [ "$ENABLE_ALERT" -eq 1 ]; then
    if [ ! -f "$ALERT_CMD" ]; then
        # 找不到告警出口时必须让人看见：静默丢弃告警是这类脚本最危险的失败方式。
        printf '告警脚本不存在，本次异常未外发：%s\n' "$ALERT_CMD" >&2
    else
        NOW="$(date +%s)"
        if [ "$STATUS" = "ALERT" ]; then
            LAST_ALERT_AT="$(cat "$LAST_ALERT_FILE" 2>/dev/null || echo 0)"
            if [ "$LAST_STATUS" != "ALERT" ] || [ $((NOW - LAST_ALERT_AT)) -gt $((RENOTIFY_HOURS * 3600)) ]; then
                python3 "$ALERT_CMD" "[KinoTV 健康巡检异常]
$LINE" ALERT >/dev/null 2>&1 || printf '告警发送失败，请检查 %s\n' "$ALERT_CMD" >&2
                printf '%s\n' "$NOW" > "$LAST_ALERT_FILE" 2>/dev/null || true
            fi
        elif [ "$LAST_STATUS" = "ALERT" ]; then
            python3 "$ALERT_CMD" "[KinoTV 健康巡检已恢复]
$LINE" RECOVERED >/dev/null 2>&1 || true
        fi
    fi
fi
printf '%s\n' "$STATUS" > "$STATE_FILE" 2>/dev/null || true

printf '\n%s\n' "$LINE"
if [ "$STATUS" = "ALERT" ]; then
    exit 1
fi
exit 0
