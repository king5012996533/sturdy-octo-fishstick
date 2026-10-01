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

## 上游出网（换机器后先验这一条）

境外上游（Replicate 这一类，域名落在 Cloudflare 上）从境内机器直连会被**间歇性阻断**：
同一台机器、同一个域名，TCP 443 连着测五次可能只通两次。更麻烦的是它会在
"连接已建立、响应还没回来"的窗口里断开——请求其实已经送达、上游已经受理并出图，
而我们这边只看到 read 超时，于是任务记成失败、成品丢在上游、预扣也退不回来。

所以换服务器或换上游之后，先跑这两条：

```bash
# 境外上游：必须五次都拿到状态码，出现 000 就是出口不可用
for i in 1 2 3 4 5; do
  curl -4 -sS -o /dev/null --max-time 8 -w '%{http_code} %{time_total}\n' https://api.replicate.com/
done

# 境内上游：作为对照，应当是 100~200ms 的 200/401
curl -4 -sS -o /dev/null --max-time 8 -w '%{http_code} %{time_total}\n' https://api.deepseek.com/
```

出口不通时不要靠重试兜底（重试会重复付费，见 `taskRefundVerdict`），而是给后端配出海代理：

```bash
HTTPS_PROXY=http://<可出海的代理>:<端口>
```

追加进 `/etc/kinotv.env` 后 `systemctl restart kinotv`。代理地址支持 `http://` 与
`socks5://`；后端已经把它接进出站传输（`internal/outbound`），配了就会走代理，
代理主机本身也不会被自建的 SSRF 规则拦下。

## 广场封面抓取

灵感广场的平台条目只把封面当"素材地址"存，早期是直接热链 LibTV 的图床。上游一加
防盗链或改目录规则，广场就是一片死图，所以在 `cmd/inspiration-covers` 里留了一条
一次性维护命令：把外链封面抓回本地资源库，条目改挂 `resource_id`，出口换成平台自己
的签名地址（见 `app.HarvestInspirationCovers`）。

```bash
cd /tmp/kinotv-src/backend
CGO_ENABLED=1 GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=https://goproxy.cn,direct \
  go build -o /tmp/inspiration-covers ./cmd/inspiration-covers
cd /opt/kinotv
CANVAS_BACKEND_DATA_DIR=/opt/kinotv/data CANVAS_DATABASE_DRIVER=sqlite /tmp/inspiration-covers
```

资源 ID 由源地址确定性派生，所以重跑只会复用、不会重复下载；上游换了图但地址没变时
加 `-overwrite`。抓下来的文件落在 `data/resources/users/platform-inspiration-covers/`，
归属一个固定的虚拟用户，不会混进任何人的"我的资源"。有失败会返回非零退出码——
截断响应和防盗链这类问题会成片出现，静默成功比失败更难查。

`cover_url` 列仍然保留原外链，只在签名不可用（没配 `CANVAS_PUBLIC_BASE_URL`）时兜底；
运营在后台改封面地址会顺手清掉 `resource_id`，否则签名地址会一直压过新填的 URL。

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

    # 根路径与 index.html 都必须每次协商，不能靠浏览器启发式缓存。
    location = / {
        add_header Cache-Control "no-cache" always;
        try_files /index.html =404;
    }

    location = /index.html {
        add_header Cache-Control "no-cache" always;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

**SPA 入口必须显式关掉缓存。** `index.html` 的文件名不带内容哈希，"是否还有效"
只能靠协商；nginx 默认不给它 `Cache-Control`，浏览器就会按 `Last-Modified` 做启发式
缓存。后果不是"多等几分钟"：发布时旧 chunk 已经被删掉，被钉在旧 `index.html` 上的
浏览器会去请求一个 404 的 JS，页面直接白屏，而服务端日志看起来一切正常（新 chunk
确实有人拉到了）。`/` 和 `/index.html` 要分开写——根路径会走 `try_files` 的"目录存在"
分支留在 `location /` 上下文里，吃不到 `location = /index.html` 那条头。

证书用 `certbot --nginx -d kinotv.xingtudesign.com`，续期走同一条 HTTP-01 路径，
`/etc/letsencrypt/renewal/kinotv.xingtudesign.com.conf` 里 `authenticator = nginx`。

**`server_name` 只保留正式域名。** 联调期曾经为了不等 DNS 把裸 IP 和
`8.163.71.55.nip.io` 写进来过：裸 IP 会让这台机器的 80 端口默认站点变成 KinoTV，
而 `nip.io` 是任何人可解析的第三方域名，都不该长期留在生产配置里。
