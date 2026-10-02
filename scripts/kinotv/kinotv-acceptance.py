#!/usr/bin/env python3
"""托管形态上线验收：匿名边界、模型目录脱敏、配置写入收敛。

审计关心的两件事——"普通账号能不能绕过前端直接改平台模型配置"、"脱敏视图会不会
漏出凭据"——都不是页面按钮能保证的，只能按接口打一遍。前端隐藏入口挡不住 curl。

退出码：0 全部通过；1 有断言失败；2 参数或环境问题。
同 kinotv-restore-drill.py 一样保持 Python 3.6 兼容（生产机是 3.6.8）。
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

# 与服务端 modelConfigCredentialFields 保持一致。headers 也算凭据：
# 渠道自定义头里经常直接塞 Authorization。
CREDENTIAL_FIELDS = ("apiKey", "secretKey", "encryptedApiKey",
                     "deviceCode", "device_code", "headers")


def check(label, ok, detail=""):
    results.append((label, ok))
    print(("  PASS  " if ok else "  FAIL  ") + label + ((" | " + detail) if detail else ""))
    return ok


def now_local():
    return datetime.now().astimezone()


def fmt(value):
    text = value.strftime("%Y-%m-%d %H:%M:%S.%f%z")
    return text[:-2] + ":" + text[-2:]


def load_accounts(auth_db):
    """挑出两个普通账号和一个管理员。两个普通账号是为了验"互相看不到"，
    只有一个账号时这条永远为真，等于没测。"""
    conn = sqlite3.connect(auth_db)
    try:
        conn.row_factory = sqlite3.Row
        rows = conn.execute(
            "select id, email, phone, role, status from app_users where status = 'ACTIVE'"
        ).fetchall()
    finally:
        conn.close()
    admins = [r for r in rows if (r["role"] or "").upper() == "ADMIN"]
    users = [r for r in rows if (r["role"] or "").upper() != "ADMIN"]
    return admins, users


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
    def __init__(self, base):
        self.base = base.rstrip("/")
        self.opener = urllib.request.build_opener(
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def call(self, method, path, body=None):
        data = json.dumps(body).encode("utf-8") if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        if data:
            req.add_header("Content-Type", "application/json")
        try:
            with self.opener.open(req, timeout=60) as resp:
                return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as err:
            return err.code, err.read().decode("utf-8", "replace")


def login(session, auth_db, account, label):
    target = account["email"] or account["phone"]
    method = "EMAIL_CODE" if account["email"] else "PHONE_CODE"
    code = issue_code(auth_db, method, target)
    status, body = session.call("POST", "/api/auth/login",
                                {"methodType": method, "target": target, "code": code})
    if status != 200:
        check("%s 登录" % label, False, "http=%d" % status)
        return False
    try:
        user_id = json.loads(body)["data"]["user"]["id"]
    except Exception:
        check("%s 登录响应可解析" % label, False, body[:120])
        return False
    return check("%s 登录" % label, user_id == account["id"], "role=%s" % account["role"])


def data_of(body):
    try:
        return json.loads(body).get("data") or {}
    except Exception:
        return {}


def leaked_credentials(node, path=""):
    """返回非空凭据字段的位置。空串/null 表示已被抹掉，是预期结果。"""
    found = []
    if isinstance(node, dict):
        for key, value in node.items():
            here = "%s.%s" % (path, key)
            if key in CREDENTIAL_FIELDS and value not in (None, "", {}, []):
                found.append(here)
            found.extend(leaked_credentials(value, here))
    elif isinstance(node, list):
        for index, value in enumerate(node):
            found.extend(leaked_credentials(value, "%s[%d]" % (path, index)))
    return found


def platform_visible(channel):
    if (channel.get("scope") or "") == "system":
        return True
    if channel.get("pinned") is True:
        return True
    return channel.get("id") == "beefapi"


def catalog_channels(body):
    config = data_of(body).get("config") or {}
    return config.get("channels") or []


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--auth-db", required=True)
    args = parser.parse_args()

    if not os.path.exists(args.auth_db):
        print("  参数错误：找不到账号库 %s" % args.auth_db)
        return 2

    admins, users = load_accounts(args.auth_db)
    if len(admins) < 1 or len(users) < 2:
        print("  参数错误：至少需要 1 个管理员和 2 个普通账号，实际 admin=%d user=%d"
              % (len(admins), len(users)))
        return 2
    admin, user_a, user_b = admins[0], users[0], users[1]

    # ---- 1. 匿名边界 ----
    # 探活要能被外部监控匿名访问；业务与平台配置必须仍然要求登录。
    anon = Session(args.base_url)
    for path, want in (("/api/health/live", 200), ("/api/health/ready", 200),
                       ("/api/health", 401), ("/api/projects", 401),
                       ("/api/workspace/model-config", 401), ("/api/tasks?pageSize=1", 401)):
        status, _ = anon.call("GET", path)
        check("匿名访问 %s" % path, status == want, "http=%d 期望 %d" % (status, want))

    # ---- 2. 两个普通账号各自登录 ----
    session_a = Session(args.base_url)
    session_b = Session(args.base_url)
    if not login(session_a, args.auth_db, user_a, "普通账号 A"):
        return 1
    if not login(session_b, args.auth_db, user_b, "普通账号 B"):
        return 1

    # ---- 3. 普通账号看到的模型目录 ----
    status, body = session_a.call("GET", "/api/workspace/model-config")
    if not check("A 读取模型目录", status == 200, "http=%d" % status):
        return 1
    payload = data_of(body)
    check("A 拿到的是脱敏目录", payload.get("source") == "platform-catalog",
          "source=%s" % payload.get("source"))
    channels_a = catalog_channels(body)
    check("脱敏目录非空", len(channels_a) > 0, "channels=%d" % len(channels_a))
    outsiders = [c.get("id") for c in channels_a if not platform_visible(c)]
    check("目录里只有平台渠道，没有账号私有渠道", not outsiders, "越界渠道=%s" % outsiders)
    leaks = leaked_credentials(payload)
    check("脱敏目录不含任何凭据", not leaks, "泄漏位置=%s" % leaks)
    revision_before = payload.get("revision")

    status, body = session_b.call("GET", "/api/workspace/model-config")
    check("B 读取模型目录", status == 200, "http=%d" % status)
    channels_b = catalog_channels(body)
    check("A 与 B 看到的目录一致",
          json.dumps(channels_a, sort_keys=True) == json.dumps(channels_b, sort_keys=True),
          "A=%d B=%d" % (len(channels_a), len(channels_b)))

    # ---- 4. 普通账号不能写平台配置 ----
    put_body = {"config": data_of(body).get("config"), "expectedRevision": revision_before}
    for session, label in ((session_a, "A"), (session_b, "B")):
        status, body = session.call("PUT", "/api/workspace/model-config", put_body)
        reason = ""
        try:
            reason = json.loads(body).get("reason") or ""
        except Exception:
            pass
        check("普通账号 %s 写平台配置被拒" % label, status == 403, "http=%d" % status)
        check("拒绝原因可机读（%s）" % label, reason == "forbidden", "reason=%r" % reason)

    status, body = session_a.call("GET", "/api/workspace/model-config")
    after = data_of(body)
    check("被拒写入没有改动配置",
          json.dumps(after.get("config"), sort_keys=True) == json.dumps(payload.get("config"), sort_keys=True)
          and after.get("revision") == revision_before,
          "revision %s -> %s" % (revision_before, after.get("revision")))

    # ---- 5. 普通账号日常读写仍然可用 ----
    # 收权限最容易误伤的就是"读"，所以登录后要立刻确认自己的数据还读得到。
    for path, want in (("/api/projects", 200), ("/api/tasks?pageSize=1", 200),
                       ("/api/workspace/bootstrap", 200)):
        status, _ = session_a.call("GET", path)
        check("A 正常使用 %s" % path, status == want, "http=%d" % status)

    status, body = session_a.call("GET", "/api/workspace/bootstrap")
    boot_user = ((data_of(body).get("user") or {}) if status == 200 else {}).get("id") or ""
    check("bootstrap 返回的是 A 自己的账号", boot_user == user_a["id"],
          "user=%s" % boot_user[:8])

    # ---- 6. 管理员仍握有完整配置与写权限 ----
    admin_session = Session(args.base_url)
    if not login(admin_session, args.auth_db, admin, "管理员"):
        return 1
    status, body = admin_session.call("GET", "/api/workspace/model-config")
    if not check("管理员读取模型配置", status == 200, "http=%d" % status):
        return 1
    admin_payload = data_of(body)
    check("管理员拿到的是完整视图", admin_payload.get("source") != "platform-catalog",
          "source=%s" % admin_payload.get("source"))
    admin_channels = (admin_payload.get("config") or {}).get("channels") or []
    with_key = [c.get("id") for c in admin_channels if c.get("apiKey")]
    check("完整视图里渠道凭据仍在（说明脱敏只作用于普通账号）", bool(with_key),
          "带凭据的渠道=%s" % with_key)

    # 原样回写：证明写通道可用，又不去改任何真实内容。
    status, body = admin_session.call("PUT", "/api/workspace/model-config", {
        "config": admin_payload.get("config"),
        "expectedRevision": admin_payload.get("revision"),
    })
    check("管理员写入被接受", status == 200, "http=%d %s" % (status, body[:120]))

    status, body = session_a.call("GET", "/api/workspace/model-config")
    check("管理员写入后普通账号仍读到同一份脱敏目录",
          json.dumps(catalog_channels(body), sort_keys=True) == json.dumps(channels_a, sort_keys=True))

    failed = [label for label, ok in results if not ok]
    print("")
    print("总计：通过 %d，失败 %d" % (len(results) - len(failed), len(failed)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
