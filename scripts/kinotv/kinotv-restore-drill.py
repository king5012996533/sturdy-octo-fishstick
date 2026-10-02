#!/usr/bin/env python3
"""恢复演练的真实性断言：不是"文件拷回来了"，而是"服务能起来、账号能登录、数据还在"。

只做 HTTP 与 SQLite 断言，不负责启动/停止服务——那是 kinotv-restore.sh 的职责。
分开的原因：编排是 shell 的强项，而选账号、造验证码、比对响应体这些事在 shell 里
既难写对也难读。

退出码：0 全部通过；1 有断言失败；2 参数或环境问题。
同 /root/notify.py 一样保持 Python 3.6 兼容（生产机是 3.6.8）。
"""
import argparse
import http.cookiejar
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta

results = []


def check(label, ok, detail=""):
    results.append((label, ok))
    print(("  PASS  " if ok else "  FAIL  ") + label + ((" | " + detail) if detail else ""))
    return ok


def now_local():
    """带时区偏移的时间戳。

    验证码的过期判断是字符串比较，格式必须与服务端写入的一致（本地时区 + 偏移），
    写成 UTC 会被判成"已过期"，演练就会以为恢复失败。
    """
    return datetime.now().astimezone()


def fmt(value):
    text = value.strftime("%Y-%m-%d %H:%M:%S.%f%z")
    return text[:-2] + ":" + text[-2:]


def pick_account(auth_db):
    conn = sqlite3.connect(auth_db)
    try:
        conn.row_factory = sqlite3.Row
        rows = conn.execute(
            "select id, email, phone, role, status from app_users where status <> 'DISABLED'"
        ).fetchall()
        if not rows:
            return None
        # 优先管理员：它同时验证了角色列没被恢复丢，且后续能读平台配置。
        admins = [r for r in rows if (r["role"] or "").upper() == "ADMIN"]
        return (admins or rows)[0]
    finally:
        conn.close()


def issue_code(auth_db, method_type, target):
    code = "9%05d" % (abs(hash(target)) % 100000)
    start = now_local()
    conn = sqlite3.connect(auth_db)
    try:
        conn.execute(
            "insert into auth_verification_codes"
            " (id, method_type, channel, scene, target, code, expires_at, used_at, created_at, updated_at)"
            " values (lower(hex(randomblob(16))), ?, ?, 'login', ?, ?, ?, NULL, ?, ?)",
            (method_type, "email" if method_type == "EMAIL_CODE" else "sms",
             target, code, fmt(start + timedelta(minutes=10)), fmt(start), fmt(start)),
        )
        conn.commit()
    finally:
        conn.close()
    return code


class Session(object):
    def __init__(self):
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, url, body=None):
        data = json.dumps(body).encode("utf-8") if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        if data:
            req.add_header("Content-Type", "application/json")
        try:
            with self.opener.open(req, timeout=30) as resp:
                return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as err:
            return err.code, err.read().decode("utf-8", "replace")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--auth-db", required=True)
    parser.add_argument("--expect-users", type=int, default=None)
    args = parser.parse_args()

    base = args.base_url.rstrip("/")
    if not os.path.exists(args.auth_db):
        print("  参数错误：找不到账号库 %s" % args.auth_db)
        return 2

    account = pick_account(args.auth_db)
    if account is None:
        check("恢复出的账号库里有可用账号", False, "app_users 为空")
        return 1
    check("恢复出的账号库里有可用账号", True,
          "user=%s role=%s" % ((account["email"] or account["phone"]), account["role"]))

    if args.expect_users is not None:
        conn = sqlite3.connect(args.auth_db)
        try:
            actual = conn.execute("select count(*) from app_users").fetchone()[0]
        finally:
            conn.close()
        check("恢复后账号数量与源库一致", actual == args.expect_users,
              "restored=%d source=%d" % (actual, args.expect_users))

    session = Session()
    status, body = session.call("GET", base + "/api/health/live")
    check("恢复实例存活探针可用", status == 200, "http=%d" % status)

    status, body = session.call("GET", base + "/api/health/ready")
    check("恢复实例就绪探针可用", status == 200, "http=%d" % status)

    # 未登录时业务接口必须仍然拒绝：恢复出来的实例不能因为数据来源不同就放松准入。
    status, _ = session.call("GET", base + "/api/projects")
    check("恢复实例仍拒绝匿名访问业务接口", status == 401, "http=%d" % status)

    target = account["email"] or account["phone"]
    method = "EMAIL_CODE" if account["email"] else "PHONE_CODE"
    code = issue_code(args.auth_db, method, target)
    status, body = session.call("POST", base + "/api/auth/login",
                                {"methodType": method, "target": target, "code": code})
    ok = status == 200
    user_id = json.loads(body)["data"]["user"]["id"] if ok else ""
    check("用恢复出的账号真实登录", ok and user_id == account["id"],
          "http=%d id=%s" % (status, user_id[:8]))
    if not ok:
        return 1

    status, body = session.call("GET", base + "/api/workspace/model-config")
    channels = []
    if status == 200:
        config = (json.loads(body).get("data") or {}).get("config") or {}
        channels = config.get("channels") or []
    check("恢复后平台模型配置可读", status == 200 and len(channels) > 0,
          "http=%d channels=%d" % (status, len(channels)))

    status, _ = session.call("GET", base + "/api/projects")
    check("恢复后能读取自己的画布列表", status == 200, "http=%d" % status)

    status, _ = session.call("GET", base + "/api/tasks?pageSize=1")
    check("恢复后能读取任务列表", status == 200, "http=%d" % status)

    failed = [label for label, ok in results if not ok]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
