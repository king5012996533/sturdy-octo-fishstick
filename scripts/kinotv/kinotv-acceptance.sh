#!/usr/bin/env bash
# 托管形态上线验收：先证明"配错就起不来"，再证明"普通账号越不了权"。
#
#   kinotv-acceptance.sh --data-dir /var/lib/kinotv/acceptance --port 18092
#
# 数据目录必须是备份恢复出来的副本，绝不能指向线上数据目录：验收会写入账号库
# 里的验证码，也会真的启动一个服务。脚本自己会把关。
#
# 退出码：0 全部通过；1 有失败；2 参数或环境问题。
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

BINARY="${KINOTV_BINARY:-/opt/kinotv/kinotv-server}"
DATA_DIR=""
PORT="${KINOTV_ACCEPT_PORT:-18092}"
PLUGIN_DIR="${CANVAS_OFFICIAL_PLUGIN_DIR:-/opt/kinotv/plugin-packages}"
LIVE_DATA_DIR="${KINOTV_DATA_DIR:-/opt/kinotv/data}"
FAILED=0

usage() {
    cat <<'USAGE'
用法: kinotv-acceptance.sh --data-dir DIR [选项]

  --data-dir DIR     验收用的数据目录副本（必填，且不能是线上数据目录）
  --binary PATH      被测二进制，默认 /opt/kinotv/kinotv-server
  --port PORT        验收实例端口，默认 18092
  --plugin-dir DIR   官方插件包目录，默认 /opt/kinotv/plugin-packages
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --data-dir) DATA_DIR="$2"; shift 2 ;;
        --binary) BINARY="$2"; shift 2 ;;
        --port) PORT="$2"; shift 2 ;;
        --plugin-dir) PLUGIN_DIR="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        -*) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
        *) printf '未知参数：%s\n' "$1" >&2; usage >&2; exit 2 ;;
    esac
done

pass() { printf '  PASS  %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; FAILED=1; }

[ -n "$DATA_DIR" ] || { usage >&2; exit 2; }
[ -x "$BINARY" ] || { printf '二进制不可执行：%s\n' "$BINARY" >&2; exit 2; }
[ -d "$DATA_DIR" ] || { printf '数据目录不存在：%s\n' "$DATA_DIR" >&2; exit 2; }
# 验收会写账号库，指向线上数据目录等于直接改生产。
DATA_REAL="$(cd "$DATA_DIR" && pwd -P)"
LIVE_REAL="$(cd "$LIVE_DATA_DIR" 2>/dev/null && pwd -P || echo "$LIVE_DATA_DIR")"
if [ "$DATA_REAL" = "$LIVE_REAL" ]; then
    printf '拒绝在线上数据目录上做验收：%s\n' "$DATA_REAL" >&2
    exit 2
fi

AUTH_DB="$DATA_REAL/kinotv-auth.db"
[ -f "$AUTH_DB" ] || { printf '账号库不存在：%s\n' "$AUTH_DB" >&2; exit 2; }

LOG="$DATA_REAL/acceptance.log"
GUARD_PORT=$((PORT + 1))

# 这两种变量必须由本脚本完全掌控：父 shell 若残留一个 CANVAS_HOSTED_AUTH，
# "未设置开关"的用例就会假通过。
unset CANVAS_HOSTED_AUTH CANVAS_AUTH_DATABASE_URL

# exec：背景执行时 $! 才等于真正的服务进程。写成普通函数调用，$! 是那个子 shell，
# kill 只杀到壳，服务会变成孤儿继续占着端口，下一次验收就撞"地址已占用"。
server_env() {
    exec env \
        CANVAS_BACKEND_DATA_DIR="$DATA_REAL" \
        CANVAS_DATABASE_DRIVER=sqlite \
        CANVAS_BACKEND_ADDR="127.0.0.1:$PORT" \
        CANVAS_AUTO_MIGRATE=true \
        CANVAS_AUTH_STATE_SECRET="acceptance-state-secret" \
        CANVAS_AUTH_DEV_ECHO_CODE=0 \
        CANVAS_OFFICIAL_PLUGIN_DIR="$PLUGIN_DIR" \
        "$@"
}

# ---- 1. 启动守卫：配错必须起不来 ----
# 托管形态漏配开关或账号库会退回"无登录的单工作区"，那种实例挂在公网上等于把
# 上游渠道整个敞开。所以这里逐条验"拒绝启动"，而不是只看文档。
printf '\n[1/3] 启动守卫（配错必须拒绝启动）\n'

guard_case() {
    local label="$1" expect_token="$2" hosted="$3" authdb="$4"
    local out="$DATA_REAL/guard-$(printf '%s' "$label" | tr -c 'a-zA-Z0-9' '-').log"
    local code=0
    set +e
    timeout 25 env \
        CANVAS_BACKEND_DATA_DIR="$DATA_REAL" \
        CANVAS_DATABASE_DRIVER=sqlite \
        CANVAS_BACKEND_ADDR="127.0.0.1:$GUARD_PORT" \
        CANVAS_AUTO_MIGRATE=true \
        ${hosted:+CANVAS_HOSTED_AUTH=$hosted} \
        ${authdb:+CANVAS_AUTH_DATABASE_URL=$authdb} \
        ${authdb:+CANVAS_AUTH_STATE_SECRET=acceptance-guard-secret} \
        "$BINARY" > "$out" 2>&1
    code=$?
    set -e
    if [ "$code" -eq 0 ]; then
        fail "${label}：进程正常启动并退出了，守卫没拦住"
        return
    fi
    if [ "$code" -eq 124 ]; then
        fail "${label}：进程一直在跑，守卫没拦住（已在 25 秒后终止）"
        return
    fi
    if grep -q "$expect_token" "$out"; then
        pass "${label}：已拒绝启动（退出码 ${code}，提示 ${expect_token}）"
    else
        fail "${label}：拒绝了，但提示里没有 ${expect_token}"
        sed 's/^/        /' "$out" | tail -5
    fi
}

guard_case "未设置运行模式开关" "CANVAS_HOSTED_AUTH" "" "$AUTH_DB"
guard_case "启用托管但缺账号库" "CANVAS_AUTH_DATABASE_URL" "true" ""
guard_case "开关值非法" "必须是 true 或 false" "maybe" "$AUTH_DB"

# 配置错误信息不能顺手把 DSN 打出来——它会进日志、进工单。
if grep -rq "kinotv-auth.db" "$DATA_REAL"/guard-*.log 2>/dev/null; then
    fail "启动失败的提示里回显了账号库路径"
else
    pass "启动失败的提示里没有回显账号库"
fi

# ---- 2. 起验收实例 ----
printf '\n[2/3] 启动验收实例（端口 %s）\n' "$PORT"
: > "$LOG"
server_env CANVAS_HOSTED_AUTH=true CANVAS_AUTH_DATABASE_URL="$AUTH_DB" \
    "$BINARY" >> "$LOG" 2>&1 &
ACCEPT_PID=$!
stop_accept() {
    kill "$ACCEPT_PID" 2>/dev/null || true
    wait "$ACCEPT_PID" 2>/dev/null || true
    # 兜底：端口还占着说明杀的可能是壳而不是服务，升级到 KILL 并明说。
    if curl -s -o /dev/null -m 2 "http://127.0.0.1:$PORT/api/health/live" 2>/dev/null; then
        kill -9 "$ACCEPT_PID" 2>/dev/null || true
        sleep 1
        if curl -s -o /dev/null -m 2 "http://127.0.0.1:$PORT/api/health/live" 2>/dev/null; then
            printf '  警告  验收实例仍在端口 %s 上应答，请手动清理\n' "$PORT" >&2
        fi
    fi
}
trap stop_accept EXIT

ready=0
for _ in $(seq 1 40); do
    if ! kill -0 "$ACCEPT_PID" 2>/dev/null; then
        break
    fi
    if [ "$(curl -s -o /dev/null -w '%{http_code}' -m 3 "http://127.0.0.1:$PORT/api/health/live" || true)" = "200" ]; then
        ready=1
        break
    fi
    sleep 1
done
if [ "$ready" -eq 1 ]; then
    pass "验收实例已启动"
    # 这一条同时验证了"匿名探活白名单"确实进了二进制：旧二进制在这里是 401。
    pass "匿名探针 /api/health/live 返回 200（白名单已生效）"
else
    fail "验收实例未能启动，见 $LOG"
    tail -20 "$LOG" 2>/dev/null | sed 's/^/        /'
    stop_accept
    trap - EXIT
    printf '\n验收结果：失败\n'
    exit 1
fi

# ---- 3. 权限矩阵 ----
printf '\n[3/3] 权限矩阵与脱敏\n'
set +e
python3 "$SCRIPT_DIR/kinotv-acceptance.py" \
    --base-url "http://127.0.0.1:$PORT" \
    --auth-db "$AUTH_DB"
python_code=$?
set -e
if [ "$python_code" -ne 0 ]; then
    FAILED=1
fi

stop_accept
trap - EXIT
printf '\n'
if [ "$FAILED" -eq 0 ]; then
    printf '验收结果：成功\n'
    exit 0
fi
printf '验收结果：失败\n'
exit 1
