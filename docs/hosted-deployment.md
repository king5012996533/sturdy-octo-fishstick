# KinoTV 服务端部署

这份文档记录 KinoTV（BeefTV 的 SaaS 形态）当前的真实部署形态：跑在哪台机器、
用了哪些路径、改配置要动什么。目标是照着能重放，而不是一份理想架构说明。

登录模块本身见 [托管登录](hosted-auth.md)。

## 拓扑

```
浏览器
  └─ nginx :80/:443  (server_name kinotv.xingtudesign.com)
       ├─ /            → /opt/kinotv/web      前端静态产物（SPA，回退 index.html）
       └─ /api/        → 127.0.0.1:8090       Go 后端
```

后端与前端同机同源，前端因此不需要 `VITE_CANVAS_BACKEND_URL`，
`web/src/services/api/request.ts` 默认的 `/api` 就是对的。

## 服务器上的路径

| 路径 | 内容 |
| --- | --- |
| `/opt/kinotv/kinotv-server` | 后端二进制（`cmd/server`） |
| `/opt/kinotv/web/` | 前端构建产物（`web/dist`） |
| `/opt/kinotv/data/` | 数据目录：`open_ai_canvas.db`、`resources/`、`.settings-key`、`local-model-config.json`、插件状态 |
| `/opt/kinotv/data/kinotv-auth.db` | 账号库（`CANVAS_AUTH_DATABASE_URL`） |
| `/opt/kinotv/plugin-packages/` | 官方声明式协议插件包（`CANVAS_OFFICIAL_PLUGIN_DIR`） |
| `/etc/kinotv.env` | 环境变量，`600` |
| `/etc/systemd/system/kinotv.service` | 常驻服务 |
| `/var/log/kinotv.log` | 服务日志（stdout + stderr） |
| `/etc/nginx/conf.d/kinotv.conf` | 站点配置，与 `xingtudesign.com.conf` 互不影响 |

`.settings-key` 必须和数据目录一起搬。少了它，模型渠道里的密钥解不开，
公网资源签名也对不上，画布参考图会全线失败。

## 构建与发布

后端必须 `CGO_ENABLED=1`：`gorm.io/driver/sqlite` 走 `mattn/go-sqlite3`，
纯 Go 构建出来的二进制能编译通过，但连不上数据库。

```bash
# 后端（在服务器上编译，架构一致、省一次交叉编译）
cd /tmp/kinotv-src/backend
CGO_ENABLED=1 GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=https://goproxy.cn,direct \
  go build -o /opt/kinotv/kinotv-server ./cmd/server

# 前端（托管形态必须显式打开登录界面，否则产物里没有登录页）
cd web && BEEFTV_HOSTED_AUTH=1 bun run build
```

上传时排除 macOS 的 AppleDouble 旁注文件（`._*`）：`tar` 里带上
`COPYFILE_DISABLE=1` 和 `--exclude '._*'`。这类文件混进
`internal/providerpreset/catalog/` 会被 `//go:embed catalog/*.json` 一起嵌进二进制，
`mustLoadCatalog` 解码失败即 panic，服务根本起不来。

## 环境变量

见 `/etc/kinotv.env`：

```
CANVAS_BACKEND_DATA_DIR=/opt/kinotv/data
CANVAS_DATABASE_DRIVER=sqlite
CANVAS_BACKEND_ADDR=127.0.0.1:8090
CANVAS_AUTO_MIGRATE=1
CANVAS_AUTH_DATABASE_URL=/opt/kinotv/data/kinotv-auth.db
CANVAS_AUTH_STATE_SECRET=<随机串>
CANVAS_AUTH_DEV_ECHO_CODE=1
CANVAS_OFFICIAL_PLUGIN_DIR=/opt/kinotv/plugin-packages
CANVAS_PUBLIC_BASE_URL=https://kinotv.xingtudesign.com
```

- `CANVAS_PUBLIC_BASE_URL` 必须是对外可访问的**域名**。上游（如 ai-genvideo）拉取
  参考图时会校验素材地址，明确拒绝 IP 字面量。
- `CANVAS_AUTH_DEV_ECHO_CODE=1` 让验证码回显在接口响应里，便于短信/邮件网关接通前联调。
  接入真实网关后必须去掉。

## 日常操作

```bash
systemctl status kinotv
systemctl restart kinotv        # 改完 /etc/kinotv.env 后执行
tail -f /var/log/kinotv.log
nginx -t && systemctl reload nginx
```

## nginx 站点

`/etc/nginx/conf.d/kinotv.conf`，443 段由 certbot 生成，不要手改带
`# managed by Certbot` 的行：

```nginx
server {
    server_name kinotv.xingtudesign.com;
    client_max_body_size 512m;
    root /opt/kinotv/web;
    index index.html;

    location /api/ {
        proxy_pass http://127.0.0.1:8090;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_read_timeout 900s;   # 视频任务轮询长，别用默认 60s
        proxy_send_timeout 900s;
    }

    location /static/ {
        expires 30d;
        add_header Cache-Control "public, immutable";
        try_files $uri =404;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

证书用 `certbot --nginx -d kinotv.xingtudesign.com`，续期走同一条 HTTP-01 路径，
`/etc/letsencrypt/renewal/kinotv.xingtudesign.com.conf` 里 `authenticator = nginx`。

**`server_name` 只保留正式域名。** 联调期曾经为了不等 DNS 把裸 IP 和
`8.163.71.55.nip.io` 写进来过：裸 IP 会让这台机器的 80 端口默认站点变成 KinoTV，
而 `nip.io` 是任何人可解析的第三方域名，都不该长期留在生产配置里。
