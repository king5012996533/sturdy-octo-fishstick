#!/usr/bin/env bash
# KinoTV 恢复与恢复演练。
#
#   kinotv-restore.sh latest --target /tmp/kinotv-restore       # 只恢复并校验
#   kinotv-restore.sh latest --target /tmp/kinotv-restore \
#       --drill --binary /opt/kinotv/kinotv-server             # 恢复后真起一次服务
#
# 备份有没有用，只有在"真的恢复出来并跑起来"之后才知道。这个脚本存在的意义就是把
# 那句话变成一条可以定时执行、失败会告警的命令。
#
# 安全边界：目标目录绝不允许是线上数据目录，脚本会直接拒绝。演练只写恢复出来的副本。
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

BACKUP_DIR="${KINOTV_BACKUP_DIR:-/root/backups/kinotv}"
TARGET=""
WITH_MEDIA=0
DRILL=0
BINARY="${KINOTV_BINARY:-/opt/kinotv/kinotv-server}"
PORT="${KINOTV_DRILL_PORT:-18099}"
LIVE_DATA_DIR="${KINOTV_DATA_DIR:-/opt/kinotv/data}"
# 与巡检共用同一组探针路径：匿名白名单是随版本才加的，旧二进制上探针返回 401。
# 演练要回答的是"恢复出来的实例能不能正常服务"，不是"二进制是哪一版"，所以同样降级。
PROBE_PATH="${KINOTV_HEALTH_PROBE_PATH:-/api/health/live}"
READY_PATH="${KINOTV_HEALTH_READY_PATH:-/api/health/ready}"
FALLBACK_PATH="${KINOTV_HEALTH_FALLBACK_PATH:-/api/auth/agreements}"
FORCE=0
RUN_ARG=""

usage() {
    cat <<'USAGE'
用法: kinotv-restore.sh <时间戳|latest> --target DIR [选项]

  --target DIR         恢复到哪个目录（必填，且不能是线上数据目录）
  --backup-dir DIR     备份根目录，默认 /root/backups/kinotv
  --with-media         一并恢复 resources（默认只恢复数据库与配置）
  --drill              恢复后启动一个临时实例做真实演练
  --binary PATH        演练用的后端二进制，默认 /opt/kinotv/kinotv-server
  --port PORT          演练实例监听端口，默认 18099
  --force              目标目录非空时先清空
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --target) TARGET="$2"; shift 2 ;;
        --backup-dir) BACKUP_DIR="$2"; shift 2 ;;
        --with-media) WITH_MEDIA=1; shift ;;
        --drill) DRILL=1; shift ;;
        --binary) BINARY="$2"; shift 2 ;;
        --port) PORT="$2"; shift 2 ;;
        --force) FORCE=1; shift ;;
        -h|--help) usage; exit 0 ;;
        -*) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
        *) RUN_ARG="$1"; shift ;;
    esac
done

[ -n "$RUN_ARG" ] || { usage >&2; exit 2; }
[ -n "$TARGET" ] || { printf '必须指定 --target\n' >&2; exit 2; }

FAILED=0
fail() { printf '  FAIL  %s\n' "$*"; FAILED=1; }
pass() { printf '  PASS  %s\n' "$*"; }
info() { printf '        %s\n' "$*"; }

# ---- 定位备份 ----
if [ "$RUN_ARG" = "latest" ]; then
    RUN_DIR="$(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '*-*' \
        -exec test -f '{}/done' \; -print 2>/dev/null | sort | tail -1)"
    [ -n "$RUN_DIR" ] || { printf '在 %s 下找不到任何完成的备份\n' "$BACKUP_DIR" >&2; exit 1; }
else
    RUN_DIR="$BACKUP_DIR/$RUN_ARG"
    [ -d "$RUN_DIR" ] || RUN_DIR="$RUN_ARG"
fi

printf '恢复源：%s\n' "$RUN_DIR"
if [ ! -f "$RUN_DIR/done" ]; then
    # 没有 done 的目录意味着上次备份中途失败或被杀，里面的文件"看着齐全"但不保证一致。
    printf '拒绝使用未完成的备份：%s 缺少 done 标记\n' "$RUN_DIR" >&2
    exit 1
fi

# ---- 目标目录准入 ----
TARGET="$(cd "$(dirname "$TARGET")" 2>/dev/null && pwd)/$(basename "$TARGET")" || {
    printf '无法解析目标目录\n' >&2; exit 2; }
LIVE_DATA_DIR="$(cd "$LIVE_DATA_DIR" 2>/dev/null && pwd || printf '%s' "$LIVE_DATA_DIR")"
if [ "$TARGET" = "$LIVE_DATA_DIR" ]; then
    printf '拒绝把恢复写到线上数据目录：%s\n' "$TARGET" >&2
    exit 2
fi
case "$TARGET/" in
    "$LIVE_DATA_DIR"/*) printf '拒绝把恢复写到线上数据目录内部：%s\n' "$TARGET" >&2; exit 2 ;;
esac

if [ -d "$TARGET" ] && [ -n "$(ls -A "$TARGET" 2>/dev/null)" ]; then
    if [ "$FORCE" -eq 1 ]; then
        rm -rf "$TARGET"
    else
        printf '目标目录非空，加 --force 清空后再恢复：%s\n' "$TARGET" >&2
        exit 2
    fi
fi
mkdir -p "$TARGET/config"

# ---- 1. 校验清单 ----
# 校验在恢复之前做：备份盘损坏时，先发现问题比恢复一半再失败要省事得多。
printf '\n[1/4] 校验备份清单\n'
MANIFEST="$RUN_DIR/manifest.tsv"
if [ ! -f "$MANIFEST" ]; then
    fail "缺少 manifest.tsv"
    exit 1
fi
MEDIA_PRUNED=0
[ -f "$RUN_DIR/media-pruned" ] && MEDIA_PRUNED=1

VERIFIED=0
while IFS=$'\t' read -r rel sum size mode; do
    [ -n "$rel" ] || continue
    src="$RUN_DIR/$rel"
    if [ ! -f "$src" ]; then
        case "$rel" in
            media/*)
                if [ "$MEDIA_PRUNED" -eq 1 ]; then
                    info "资源已按保留策略清理，跳过：$rel"
                    continue
                fi
                ;;
        esac
        fail "备份缺少文件：$rel"
        continue
    fi
    actual_sum="$(kinotv_sha256 "$src")"
    if [ "$actual_sum" != "$sum" ]; then
        fail "校验和不符：$rel"
        continue
    fi
    actual_size="$(kinotv_file_size "$src")"
    if [ "$actual_size" != "$size" ]; then
        fail "大小不符：${rel}（清单 ${size}，实际 ${actual_size}）"
        continue
    fi
    VERIFIED=$((VERIFIED + 1))
done < "$MANIFEST"
if [ "$FAILED" -eq 0 ] && [ "$VERIFIED" -gt 0 ]; then
    pass "清单里 $VERIFIED 个文件全部校验通过"
fi
[ "$FAILED" -eq 0 ] || { printf '\n备份不可用，终止恢复。\n'; exit 1; }

# ---- 2. 落盘 ----
printf '\n[2/4] 恢复到 %s\n' "$TARGET"
cp -p "$RUN_DIR"/*.db "$TARGET/" 2>/dev/null || true
if [ -d "$RUN_DIR/config" ]; then
    # 必须用 find 而不是 config/*：.settings-key 是点文件，shell 通配符不匹配，
    # 而少了它整个库里的渠道密钥都解不开——这类"少一个文件也算恢复成功"的 bug
    # 正是恢复演练要抓的，不能靠通配符自觉。
    find "$RUN_DIR/config" -mindepth 1 -maxdepth 1 -type f -exec cp -p {} "$TARGET/config/" \;
    # 服务读的是数据目录根下的这几个文件，config/ 只是备份里的分组。
    find "$TARGET/config" -mindepth 1 -maxdepth 1 -type f -exec cp -p {} "$TARGET/" \;
fi
if [ "$WITH_MEDIA" -eq 1 ]; then
    if [ -d "$RUN_DIR/media/resources" ]; then
        mkdir -p "$TARGET/resources"
        # 必须复制而不是沿用硬链接：恢复出来的这份要能独立于备份目录存在，
        # 否则以后裁掉旧备份会把"已经恢复好的数据"一起带走。
        if cp -a "$RUN_DIR/media/resources/." "$TARGET/resources/"; then
            MEDIA_TOTAL="$(find "$TARGET/resources" -type f | wc -l | tr -d ' ')"
            pass "资源已恢复（$MEDIA_TOTAL 个文件全部落盘）"
        else
            fail "资源复制失败"
        fi
    else
        info "本次备份没有资源（已清理或本来就没备份），跳过"
    fi
fi
if [ -f "$TARGET/.settings-key" ]; then
    chmod 600 "$TARGET/.settings-key"
    [ "$(kinotv_file_mode "$TARGET/.settings-key")" = "600" ] \
        && pass ".settings-key 已恢复且权限为 600" \
        || fail ".settings-key 权限异常：$(kinotv_file_mode "$TARGET/.settings-key")"
elif [ -f "$RUN_DIR/config/.settings-key" ]; then
    fail "备份里有 .settings-key，但恢复后不见了"
else
    info "本次备份没有 .settings-key（本地模式数据可能没有）"
fi

# 完整性兜底：清单里记过的每个文件都必须真的落到目标目录，且内容一致。
# 上面每步各自"尽力而为"，这条是唯一能保证"不会静默少一个文件"的检查。
MISSING=0
while IFS=$'\t' read -r rel sum size mode; do
    [ -n "$rel" ] || continue
    case "$rel" in
        media/resources/*)
            # 没勾 --with-media 时素材本来就不恢复；按策略裁过的也一样，都不算失败。
            [ "$WITH_MEDIA" -eq 1 ] || continue
            [ "$MEDIA_PRUNED" -eq 1 ] && continue
            out="$TARGET/resources/${rel#media/resources/}" ;;
        media/*) continue ;;
        config/*) out="$TARGET/${rel#config/}" ;;
        *) out="$TARGET/$rel" ;;
    esac
    if [ ! -f "$out" ]; then
        fail "恢复后缺少文件：$rel"
        MISSING=$((MISSING + 1))
        continue
    fi
    case "$rel" in
        media/resources/*)
            # 内容校验第 1 步已经对着备份本体做过一遍，这里只确认落盘完整（大小一致）。
            # 素材是这里唯一会到 GB 级的部分，一次恢复把它读两遍不值得。
            [ "$(kinotv_file_size "$out")" = "$size" ] || {
                fail "恢复后大小不符：$rel"
                MISSING=$((MISSING + 1))
            }
            continue
            ;;
    esac
    if [ "$(kinotv_sha256 "$out")" != "$sum" ]; then
        fail "恢复后内容与备份不符：$rel"
        MISSING=$((MISSING + 1))
    fi
done < "$MANIFEST"
[ "$MISSING" -eq 0 ] && pass "恢复出的文件与备份清单逐一比对一致"

# ---- 3. 恢复后的数据库自检 ----
printf '\n[3/4] 恢复结果自检\n'
for DB in open_ai_canvas kinotv-auth; do
    [ -f "$TARGET/$DB.db" ] || continue
    result="$(sqlite3 "$TARGET/$DB.db" 'pragma integrity_check;' 2>&1 | head -1)"
    if [ "$result" = "ok" ]; then
        pass "$DB.db integrity_check=ok"
    else
        fail "$DB.db integrity_check=$result"
    fi
done
if [ -f "$TARGET/open_ai_canvas.db" ]; then
    canvases="$(sqlite3 "$TARGET/open_ai_canvas.db" 'select count(*) from canvas_projects;' 2>/dev/null || echo '?')"
    snapshots="$(sqlite3 "$TARGET/open_ai_canvas.db" 'select count(*) from canvas_snapshots;' 2>/dev/null || echo '?')"
    info "云端画布：$canvases 个，画布快照：$snapshots 个"
fi
if [ -f "$TARGET/kinotv-auth.db" ]; then
    users="$(sqlite3 "$TARGET/kinotv-auth.db" 'select count(*) from app_users;' 2>/dev/null || echo '?')"
    info "账号记录数：$users"
fi

# 临时实例是否已经在服务。200 即通过；401 说明该二进制还没开放匿名探针，
# 再用一个本来就匿名的接口确认 HTTP 栈确实是活的，避免把"版本旧"误判成"起不来"。
drill_probe_ok() {
    local code
    code="$(curl -s -o /dev/null -w '%{http_code}' -m 3 "http://127.0.0.1:$PORT$PROBE_PATH" 2>/dev/null || true)"
    [ "$code" = "200" ] && return 0
    if [ "$code" = "401" ] && [ -n "$FALLBACK_PATH" ]; then
        code="$(curl -s -o /dev/null -w '%{http_code}' -m 3 "http://127.0.0.1:$PORT$FALLBACK_PATH" 2>/dev/null || true)"
        [ "$code" = "200" ] && return 0
    fi
    return 1
}

# ---- 4. 演练 ----
if [ "$DRILL" -eq 1 ]; then
    printf '\n[4/4] 启动临时实例演练（端口 %s）\n' "$PORT"
    if [ ! -x "$BINARY" ]; then
        fail "演练二进制不可执行：$BINARY"
        exit 1
    fi
    DRILL_LOG="$TARGET/drill.log"
    mock_secret="restore-drill-state-secret"
    env \
        CANVAS_BACKEND_DATA_DIR="$TARGET" \
        CANVAS_DATABASE_DRIVER=sqlite \
        CANVAS_BACKEND_ADDR="127.0.0.1:$PORT" \
        CANVAS_AUTO_MIGRATE=true \
        CANVAS_HOSTED_AUTH=true \
        CANVAS_AUTH_DATABASE_URL="$TARGET/kinotv-auth.db" \
        CANVAS_AUTH_STATE_SECRET="$mock_secret" \
        CANVAS_AUTH_DEV_ECHO_CODE=0 \
        CANVAS_OFFICIAL_PLUGIN_DIR="${CANVAS_OFFICIAL_PLUGIN_DIR:-/opt/kinotv/plugin-packages}" \
        "$BINARY" >> "$DRILL_LOG" 2>&1 &
    DRILL_PID=$!
    stop_drill() {
        kill "$DRILL_PID" 2>/dev/null || true
        wait "$DRILL_PID" 2>/dev/null || true
    }
    trap stop_drill EXIT

    ready=0
    for _ in $(seq 1 40); do
        if ! kill -0 "$DRILL_PID" 2>/dev/null; then
            break
        fi
        if drill_probe_ok; then
            ready=1
            break
        fi
        sleep 1
    done
    if [ "$ready" -eq 1 ]; then
        pass "临时实例已启动"
        if python3 "$SCRIPT_DIR/kinotv-restore-drill.py" \
                --base-url "http://127.0.0.1:$PORT" \
                --auth-db "$TARGET/kinotv-auth.db" \
                --live-path "$PROBE_PATH" \
                --ready-path "$READY_PATH" \
                --fallback-path "$FALLBACK_PATH"; then
            pass "恢复演练断言全部通过"
        else
            fail "恢复演练断言未通过（详见上方 FAIL 行）"
        fi
    else
        fail "临时实例未能启动，见 $DRILL_LOG"
        tail -20 "$DRILL_LOG" 2>/dev/null | sed 's/^/        /'
    fi

    stop_drill
    trap - EXIT
    # 只有演练通过才更新"上次验证时间"：健康检查靠它判断演练是否还在按期执行。
    if [ "$FAILED" -eq 0 ]; then
        {
            printf 'verified_at=%s\n' "$(kinotv_iso_now)"
            printf 'source=%s\n' "$(basename "$RUN_DIR")"
        } > "$BACKUP_DIR/last-verify"
    fi
else
    printf '\n[4/4] 未指定 --drill，只做了文件级恢复与数据库自检\n'
fi

printf '\n'
if [ "$FAILED" -eq 0 ]; then
    printf '恢复结果：成功\n'
    exit 0
fi
printf '恢复结果：失败\n'
exit 1
