#!/usr/bin/env python3
"""告警外发：把巡检结果送到人能看见的地方，而不是只写进日志文件。

支持两种出口，配哪个用哪个（配置写在 /etc/kinotv-alert.env，权限 600）：

  ALERT_WEBHOOK_URL  钉钉 / 企业微信 / 飞书群机器人
  ALERT_EMAIL_TO     邮件，走 BEEFTV_SMTP_*（与登录验证码同一套发信配置）

两种出口都失败时返回非 0，让调用方（systemd / cron）自己再报一次错——静默丢弃
告警是这类脚本最危险的失败模式：看起来一切正常，出事时却没人知道。

用法: kinotv-alert.py "<正文>" [LEVEL]
保持 Python 3.6 兼容（生产机为 3.6.8）。
"""
import json
import os
import smtplib
import ssl
import sys
import urllib.request
from datetime import datetime
from email.header import Header
from email.mime.text import MIMEText

CONF_PATH = os.environ.get("KINOTV_ALERT_ENV", "/etc/kinotv-alert.env")
LOG_PATH = os.environ.get("KINOTV_ALERT_LOG", "/var/log/kinotv-alert.log")


def load_conf():
    conf = {}
    if os.path.exists(CONF_PATH):
        with open(CONF_PATH) as handle:
            for line in handle:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                key, value = line.split("=", 1)
                conf[key.strip()] = value.strip().strip('"')
    return conf


def log(level, text):
    stamp = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    try:
        with open(LOG_PATH, "a") as handle:
            handle.write("[%s] %s %s\n" % (stamp, level, text.replace("\n", " / ")))
    except OSError:
        pass


def send_webhook(url, text):
    # 飞书的载荷键名与钉钉/企微不同，按 URL 判断，避免多加一个必须记得填的开关。
    if "feishu" in url or "larksuite" in url:
        payload = {"msg_type": "text", "content": {"text": text}}
    else:
        payload = {"msgtype": "text", "text": {"content": text}}
    request = urllib.request.Request(
        url, data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=15) as response:
        return response.status


def send_email(conf, text, level):
    host = conf.get("BEEFTV_SMTP_HOST", "")
    if not host:
        raise RuntimeError("未配置 BEEFTV_SMTP_HOST")
    to = conf.get("ALERT_EMAIL_TO", "")
    if not to:
        raise RuntimeError("未配置 ALERT_EMAIL_TO")
    port = int(conf.get("BEEFTV_SMTP_PORT") or 587)
    user = conf.get("BEEFTV_SMTP_USERNAME", "")
    password = conf.get("BEEFTV_SMTP_PASSWORD", "")
    sender = conf.get("BEEFTV_SMTP_FROM") or user
    message = MIMEText(text, "plain", "utf-8")
    message["Subject"] = Header("[KinoTV] " + level, "utf-8")
    message["From"] = "%s <%s>" % (conf.get("BEEFTV_SMTP_FROM_NAME", "KinoTV"), sender)
    message["To"] = to
    context = ssl.create_default_context()
    if port == 465:
        with smtplib.SMTP_SSL(host, port, timeout=20, context=context) as server:
            if user:
                server.login(user, password)
            server.sendmail(sender, [to], message.as_string())
    else:
        with smtplib.SMTP(host, port, timeout=20) as server:
            server.starttls(context=context)
            if user:
                server.login(user, password)
            server.sendmail(sender, [to], message.as_string())
    return to


def main():
    if len(sys.argv) < 2:
        print("用法: kinotv-alert.py <正文> [LEVEL]")
        return 2
    text = sys.argv[1]
    level = sys.argv[2] if len(sys.argv) > 2 else "INFO"
    conf = load_conf()

    sent = []
    errors = []
    url = conf.get("ALERT_WEBHOOK_URL", "")
    if url:
        try:
            send_webhook(url, text)
            sent.append("webhook")
        except Exception as err:  # noqa: BLE001 - 出口失败必须继续尝试下一个
            errors.append("webhook: %s" % err)
    if conf.get("ALERT_EMAIL_TO"):
        try:
            to = send_email(conf, text, level)
            sent.append("email:%s" % to)
        except Exception as err:  # noqa: BLE001
            errors.append("email: %s" % err)

    if not sent and not errors:
        log(level, "未配置任何告警出口，仅记录日志")
        print("未配置告警出口（ALERT_WEBHOOK_URL / ALERT_EMAIL_TO），已记入 %s" % LOG_PATH)
        return 0
    if sent:
        log(level, "已发送 %s" % ",".join(sent))
        print("已发送：%s" % ",".join(sent))
    for item in errors:
        log(level, "发送失败 %s" % item)
        print("发送失败：%s" % item, file=sys.stderr)
    return 0 if sent else 1


if __name__ == "__main__":
    sys.exit(main())
