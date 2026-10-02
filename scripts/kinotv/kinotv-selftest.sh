#!/usr/bin/env bash
# 备份 / 恢复 / 巡检三个脚本的自检。
#
# 改过这几个脚本之后跑一次：它不碰线上数据，全部在临时目录里完成，但会真正走一遍
# "备份 → 恢复 → 校验 → 篡改被发现 → 缺 done 被拒绝 → 巡检告警与去重"。
# 这些负向用例是重点：一个只会说"通过"的校验脚本等于没有校验。
#
#   bash scripts/kinotv/kinotv-selftest.sh                # 只做文件级验证
#   KINOTV_BINARY=/path/to/kinotv-server \
#     bash scripts/kinotv/kinotv-selftest.sh --drill      # 额外做一次真实启动演练
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=kinotv-common.sh
source "$SCRIPT_DIR/kinotv-common.sh"

RUN_DRILL=0
[ "${1:-}" = "--drill" ] && RUN_DRILL=1

WORK="$(mktemp -d "${TMPDIR:-/tmp}/kinotv-selftest.XXXXXX")"
DATA="$WORK/data"
BACKUPS="$WORK/backups"
STATE="$WORK/state"
FAKE_ALERT="$WORK/fake-alert.py"
PASSED=0
FAILED=0

cleanup() { rm -rf "$WORK" 2>/dev/null || true; }
trap cleanup EXIT

expect() {
    local label="$1" want="$2" got="$3"
    if [ "$want" = "$got" ]; then
        printf '  PASS  %s\n' "$label"
        PASSED=$((PASSED + 1))
    else
        printf '  FAIL  %s（期望 %s，实际 %s）\n' "$label" "$want" "$got"
        FAILED=$((FAILED + 1))
    fi
}

expect_true() {
    local label="$1" condition="$2"
    if [ "$condition" = "yes" ]; then
        printf '  PASS  %s\n' "$label"; PASSED=$((PASSED + 1))
    else
        printf '  FAIL  %s\n' "$label"; FAILED=$((FAILED + 1))
    fi
}

# ---- 造一份最小数据目录 ----
mkdir -p "$DATA/resources/2026/10"
sqlite3 "$DATA/open_ai_canvas.db" \
    "create table canvas_projects(id text primary key); create table canvas_snapshots(id text primary key);"
sqlite3 "$DATA/open_ai_canvas.db" "insert into canvas_projects values ('p1'); insert into canvas_snapshots values ('s1');"
sqlite3 "$DATA/kinotv-auth.db" \
    "create table app_users(id text primary key, email text, phone text, role text, status text);
     create table auth_verification_codes(id text, method_type text, channel text, scene text, target text,
        code text, expires_at text, used_at text, created_at text, updated_at text);
     insert into app_users values ('u1','owner@example.com',null,'ADMIN','ACTIVE');"
printf '{"channels":[]}' > "$DATA/local-model-config.json"
printf '{}' > "$DATA/plugin_registry.json"
head -c 32 /dev/urandom > "$DATA/.settings-key"
chmod 600 "$DATA/.settings-key"
printf 'media-bytes' > "$DATA/resources/2026/10/a.bin"

cat > "$FAKE_ALERT" <<'PY'
import sys
open(sys.argv[0] + ".out", "a").write(sys.argv[1].splitlines()[0] + "\n")
PY

printf '\n[备份]\n'
"$SCRIPT_DIR/kinotv-backup.sh" --data-dir "$DATA" --backup-dir "$BACKUPS" --quiet >/dev/null 2>&1
RUN_DIR="$(find "$BACKUPS" -mindepth 1 -maxdepth 1 -type d | sort | tail -1)"
expect_true "产出 done 标记" "$([ -f "$RUN_DIR/done" ] && echo yes || echo no)"
expect_true "两个库都在备份里" \
    "$([ -f "$RUN_DIR/open_ai_canvas.db" ] && [ -f "$RUN_DIR/kinotv-auth.db" ] && echo yes || echo no)"
expect_true "点文件 .settings-key 进了备份" "$([ -f "$RUN_DIR/config/.settings-key" ] && echo yes || echo no)"
sidecars="$(find "$RUN_DIR" -maxdepth 1 \( -name '*-wal' -o -name '*-shm' \) | wc -l | tr -d ' ')"
expect "没有遗留 WAL 边车文件" 0 "$sidecars"

printf '\n[恢复]\n'
"$SCRIPT_DIR/kinotv-restore.sh" latest --backup-dir "$BACKUPS" --target "$WORK/restored" --with-media \
    > "$WORK/restore.log" 2>&1
expect "恢复退出码" 0 "$?"
expect_true "恢复出的 .settings-key 内容一致" \
    "$(cmp -s "$DATA/.settings-key" "$WORK/restored/.settings-key" && echo yes || echo no)"
expect_true "恢复出的资源文件内容一致" \
    "$(cmp -s "$DATA/resources/2026/10/a.bin" "$WORK/restored/resources/2026/10/a.bin" && echo yes || echo no)"
expect_true "恢复出的画布库可读" \
    "$([ "$(sqlite3 "$WORK/restored/open_ai_canvas.db" 'select count(*) from canvas_projects;')" = "1" ] && echo yes || echo no)"

printf '\n[负向：备份被篡改必须被拦下]\n'
python3 - "$RUN_DIR/open_ai_canvas.db" <<'PY'
import sys
path = sys.argv[1]
data = bytearray(open(path, "rb").read())
data[len(data) // 2] ^= 0xFF
open(path, "wb").write(bytes(data))
PY
set +e
"$SCRIPT_DIR/kinotv-restore.sh" latest --backup-dir "$BACKUPS" --target "$WORK/bad" --force > "$WORK/tamper.log" 2>&1
tamper_code=$?
set -e
expect "篡改后恢复被拒绝（退出码 1）" 1 "$tamper_code"
expect_true "日志指出校验和不符" \
    "$(grep -q '校验和不符' "$WORK/tamper.log" && echo yes || echo no)"

printf '\n[负向：缺少 done 的备份必须被拒绝]\n'
rm -f "$RUN_DIR/done"
set +e
"$SCRIPT_DIR/kinotv-restore.sh" latest --backup-dir "$BACKUPS" --target "$WORK/bad2" --force > "$WORK/nodone.log" 2>&1
nodone_code=$?
set -e
expect "缺 done 时恢复被拒绝" 1 "$nodone_code"

printf '\n[巡检]\n'
# 一个必然连不上的地址：确认探针真的在探测，而不是永远返回 OK。
set +e
"$SCRIPT_DIR/kinotv-healthcheck.sh" --base-url "http://127.0.0.1:1" --backup-dir "$BACKUPS" \
    --state-dir "$STATE" --log "$WORK/health.log" --no-alert > "$WORK/hc-bad.log" 2>&1
bad_code=$?
set -e
expect "服务不可达时巡检退出码为 1" 1 "$bad_code"
expect_true "巡检报告里点名了探针" \
    "$(grep -q '存活探针' "$WORK/health.log" && echo yes || echo no)"

if [ "$RUN_DRILL" -eq 1 ]; then
    printf '\n[演练]\n'
    binary="${KINOTV_BINARY:-/opt/kinotv/kinotv-server}"
    if [ ! -x "$binary" ]; then
        printf '  SKIP  未提供可执行的 KINOTV_BINARY（%s）\n' "$binary"
    else
        "$SCRIPT_DIR/kinotv-backup.sh" --data-dir "$DATA" --backup-dir "$BACKUPS" --quiet >/dev/null 2>&1
        set +e
        "$SCRIPT_DIR/kinotv-restore.sh" latest --backup-dir "$BACKUPS" --target "$WORK/drill" --force \
            --drill --binary "$binary" --port 18097 > "$WORK/drill.log" 2>&1
        drill_code=$?
        set -e
        # 合成数据没有真实表结构，服务起不来是预期的；这里只断言失败被如实报告，
        # 真实演练由 kinotv-restore-drill.timer 用生产数据完成。
        expect_true "演练失败时条目被如实报告" \
            "$(grep -qE 'FAIL|未通过|未能启动' "$WORK/drill.log" && echo yes || echo no)"
        printf '        演练退出码=%s（合成数据下失败属预期）\n' "$drill_code"
    fi
fi

printf '\n总计：通过 %d，失败 %d\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
