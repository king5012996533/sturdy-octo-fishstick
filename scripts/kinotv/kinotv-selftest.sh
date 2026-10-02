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

STUB_PID=""
cleanup() {
    [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null || true
    rm -rf "$WORK" 2>/dev/null || true
}
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

printf '\n[静态检查：变量名后紧跟非 ASCII 字符]\n'
# bash 在部分版本/区域设置下会把紧跟 $VAR 的多字节字符并进变量名，变成
# "DEGRADED）: unbound variable"。这种 bug 只在特定分支上才炸，跑不出来就是漏网。
set +e
bad_refs="$(python3 - "$SCRIPT_DIR" <<'PY'
import glob
import io
import os
import re
import sys

pattern = re.compile(r'\$([A-Za-z_][A-Za-z0-9_]*)(?=[^\x00-\x7f])')
found = []
for path in sorted(glob.glob(os.path.join(sys.argv[1], '*.sh'))):
    for number, line in enumerate(io.open(path, encoding='utf-8').read().split('\n'), 1):
        if line.lstrip().startswith('#'):
            continue
        for match in pattern.finditer(line):
            found.append('%s:%d $%s' % (os.path.basename(path), number, match.group(1)))
print('; '.join(found))
PY
)"
set -e
expect "脚本里没有变量名后紧跟中文的写法" "" "$bad_refs"

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

# 降级探活：旧二进制上 /api/health/live 返回 401，但服务其实是好的。
# 这条用例防止"版本旧"被误报成"服务挂了"，那种误报会让人很快不再相信巡检。
printf '\n[巡检：未开放匿名探针的旧二进制]
'
cat > "$WORK/stub.py" <<'PY'
import http.server, sys


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        code = 200 if self.path.startswith('/api/auth/agreements') else 401
        self.send_response(code)
        self.send_header('Content-Length', '0')
        self.end_headers()

    def log_message(self, *args):
        pass


http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), Handler).serve_forever()
PY
python3 "$WORK/stub.py" 18095 &
STUB_PID=$!
for _ in $(seq 1 20); do
    curl -s -o /dev/null -m 1 http://127.0.0.1:18095/api/auth/agreements && break
    sleep 0.2
done
DEG_BACKUPS="$WORK/deg-backups"
"$SCRIPT_DIR/kinotv-backup.sh" --data-dir "$DATA" --backup-dir "$DEG_BACKUPS" --quiet >/dev/null 2>&1
printf 'verified_at=%s\nsource=selftest\n' "$(kinotv_iso_now)" > "$DEG_BACKUPS/last-verify"
set +e
"$SCRIPT_DIR/kinotv-healthcheck.sh" --base-url "http://127.0.0.1:18095" --backup-dir "$DEG_BACKUPS" \
    --state-dir "$WORK/deg-state" --log "$WORK/deg-health.log" --no-alert > "$WORK/deg-health.out" 2>&1
deg_code=$?
set -e
expect "旧二进制下巡检仍判定为正常（退出码 0）" 0 "$deg_code"
expect_true "输出里说明了降级原因" \
    "$(grep -q '降级' "$WORK/deg-health.out" && echo yes || echo no)"
expect_true "落库的日志标注了 degraded" \
    "$(grep -q 'degraded' "$WORK/deg-health.log" && echo yes || echo no)"
expect_true "降级原因是探针 401 而不是服务不通" \
    "$(grep -q '401' "$WORK/deg-health.out" && echo yes || echo no)"
kill "$STUB_PID" 2>/dev/null || true
STUB_PID=""

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
