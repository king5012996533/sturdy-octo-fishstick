# KinoTV 服务端部署

这份文档记录 KinoTV（BeefTV 的 SaaS 形态）当前的真实部署形态：跑在哪台机器、
用了哪些路径、改配置要动什么。目标是照着能重放，而不是一份理想架构说明。

登录模块本身见 [托管登录](hosted-auth.md)。

## 合规信息

对外可查的主体与备案口径，改动这些值要走两处：站点页脚在后台「系统配置 → 站点及外观」，
协议正文在后台「协议管理」发布新版本（发布即要求所有账号重新同意）。

| 项 | 值 | 出现位置 |
| --- | --- | --- |
| 运营主体全称 | 深圳市光启云科电子商务有限公司 | 用户协议正文、页脚版权 |
| ICP 主体备案号 | 粤ICP备2026065982号 | 页脚（链至工信部备案系统） |
| 统一社会信用代码 | 91440300MAEGQGNT69 | 备案信息，站内不展示 |
| 主体所在地 | 广东省深圳市龙华区 | 用户协议的争议管辖 |
| 联系渠道 | 站内「帮助与反馈」提交工单，或 samwork2026@126.com | 用户协议、隐私政策 |

`backend/internal/auth/agreements.go` 里那份内置正文只是还没发布过版本时的兜底，
线上以后台发布的版本为准。

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

### 前端发布：覆盖合并，不要整体替换目录

构建产物里的 chunk 名带内容哈希，而**已经打开的页面还握着一份旧的哈希图**。把
`/opt/kinotv/web` 换成新目录（`mv` 走旧目录）等于把旧 chunk 从线上抹掉，那些页面下一次
懒加载就会 404，用户看到的是 `Failed to fetch dynamically imported module`——
页面本身没坏，坏的是它引用的那个文件已经不在了。刷新能好，但用户不会知道要刷新。

所以前端发布只做"覆盖合并"：同名文件用新的，旧文件留着继续服务，靠定时清理淘汰。

```bash
# 1. 本地构建（托管形态必须显式打开登录界面）
cd web && BEEFTV_HOSTED_AUTH=1 bun run build

# 2. 先传到一个只属于本次发布的暂存目录，别直接写线上目录
COPYFILE_DISABLE=1 tar -cz -C dist --exclude '._*' -f - . \
  | ssh kinotv "mkdir -p /opt/kinotv/web-new-<时间戳> && tar -xz -C /opt/kinotv/web-new-<时间戳> -f -"

# 3. 校验拿到的是这一版，再留一份可回滚的复制（是 cp，不是 mv）
ssh kinotv "md5sum /opt/kinotv/web-new-<时间戳>/index.html"   # 与本地 dist/index.html 一致
ssh kinotv "cp -a /opt/kinotv/web /opt/kinotv/web.bak-<时间戳>"

# 4. 覆盖合并：同名覆盖，旧 chunk 原地保留
ssh kinotv "cp -a /opt/kinotv/web-new-<时间戳>/. /opt/kinotv/web/ && rm -r /opt/kinotv/web-new-<时间戳>"

# 5. 验收：入口 chunk 是新哈希且 200，顺手确认一个旧 chunk 也还在 200
curl -s https://kinotv.xingtudesign.com/ | grep -o 'static/index-[a-zA-Z0-9_-]*\.js' | head -1
curl -s -o /dev/null -w '%{http_code}\n' https://kinotv.xingtudesign.com/static/<旧入口 chunk>
```

留旧文件会慢慢堆：一轮发布约 100M，几轮下来 `static/` 会翻倍。淘汰按文件年龄走，
和 `kinotv-prune-releases.sh` 的"按份数保留回滚物"是两件事，别混在同一个策略里——
回滚物按份数，静态残留按天数（默认 30 天，比任何用户的标签页存活时间都长）。
`kinotv-prune-releases.sh` 里已经带上这条策略，不用再手敲 `find`。

```bash
# 先看清单（--web-stale-days 0 可以整条关掉）
/opt/kinotv/scripts/kinotv/kinotv-prune-releases.sh --dry-run
```

为什么按天数而不是按份数：一天连发三次的话，按份数保留 3 份等于把两天前的 chunk 清光，
正好是长时间开着标签页的那批用户受害。另外新构建每轮都会重写全部产物（mtime 跟着刷新），
所以只有"确实不再被任何一份构建产出"的旧 chunk 才会攒到 30 天，判据是安全的。

一个前提：旧 chunk 要是靠 `cp -rn`（不带 `-p`）合进来的，mtime 会被刷成合并那一刻，
等于把年龄清零、再等 30 天。所以合并旧产物请用 `cp -a` 或用 tar 解包。

### 上线验收

换二进制之前先跑一遍，别拿生产当它第一次运行的环境：

```bash
# 1. 从备份恢复一份副本（写验证码、起临时实例都只发生在副本上）
/opt/kinotv/scripts/kinotv/kinotv-restore.sh latest --target /var/lib/kinotv/acceptance --force
# 2. 用待发布的二进制跑验收
/opt/kinotv/scripts/kinotv/kinotv-acceptance.sh --data-dir /var/lib/kinotv/acceptance \
    --binary /tmp/kinotv-server-new --port 18092
```

`kinotv-acceptance.sh` 先验"配错就必须起不来"，再起一个实例打权限矩阵：

- 启动守卫：不写 `CANVAS_HOSTED_AUTH`、写了 `true` 但缺账号库、开关值非法，三种都必须拒绝启动
- 匿名边界：`/api/health/live`、`/api/health/ready` 允许匿名；`/api/health`、
  `/api/projects`、`/api/tasks`、`/api/workspace/model-config` 仍然 401
- 脱敏目录：普通账号读到 `source=platform-catalog`，只含平台渠道，且 `apiKey`、
  `secretKey`、`headers` 这类凭据字段全部为空
- 越权写入：普通账号 PUT 必须 403 且 `reason=forbidden`，配置内容与 `revision` 不变
- 管理员：仍拿到完整视图（渠道凭据在），原样回写被接受

数据目录必须指向副本：验收会往账号库里写登录验证码，脚本会直接拒绝在线上数据目录上运行。
前端隐藏入口挡不住 curl，所以这一层只能按接口验。

## 环境变量

见 `/etc/kinotv.env`：

```
CANVAS_BACKEND_DATA_DIR=/opt/kinotv/data
CANVAS_DATABASE_DRIVER=sqlite
CANVAS_BACKEND_ADDR=127.0.0.1:8090
CANVAS_AUTO_MIGRATE=1
CANVAS_HOSTED_AUTH=true
CANVAS_AUTH_DATABASE_URL=/opt/kinotv/data/kinotv-auth.db
CANVAS_AUTH_STATE_SECRET=<随机串>
CANVAS_AUTH_DEV_ECHO_CODE=0
CANVAS_OFFICIAL_PLUGIN_DIR=/opt/kinotv/plugin-packages
CANVAS_PUBLIC_BASE_URL=https://kinotv.xingtudesign.com
```

- `CANVAS_PUBLIC_BASE_URL` 必须是对外可访问的**域名**。上游（如 ai-genvideo）拉取
  参考图时会校验素材地址，明确拒绝 IP 字面量。
- `CANVAS_HOSTED_AUTH` 必须显式写出，`cmd/server` 缺这个变量会**拒绝启动**：它决定
  安全边界，漏配时回退到某个默认模式比启动失败危险得多。置 `true` 后缺
  `CANVAS_AUTH_DATABASE_URL` 同样拒绝启动——公网上的无登录共享工作区等于把上游渠道
  开放给所有人。置 `false` 才进入单工作区模式，且默认只监听回环地址。桌面版不读这个开关。
- `CANVAS_AUTH_DEV_ECHO_CODE=0` 是生产必需值：置 `1` 时验证码会回显在接口响应里，
  只有短信/邮件通道接通前的本地联调才允许，且必须同时确认没人能访问服务端日志
  （未配置投递通道时验证码会写日志，日志可见者等于可以登录任意账号）。

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

## 广场成片地址与复刻配方抓取

一条灵感要能"照着做"，需要三样东西：提示词、成片、以及**配方**——参考图、视频模型、
时长比例。提示词早就在库里，成片和配方靠 `cmd/inspiration-videos` 从上游补齐。

### 成片：热链，不落盘

成片**不能自存**：池子里 80 条的中位体量是 292MB、最大 1.5GB，合计 32.6GB，而机器只剩
20G。所以口径是"封面自持、成片热链"——数据库只存一个几百字节的 URL，实际字节永远留在
上游 CDN，播放时由浏览器直接去取。

### 配方：为什么它才是这条链的重点

上游的"模板"是整张画布（`nodes` + `edges`），提示词只是其中一个文本节点。同一条提示词
配上不同的参考图与模型是两个作品——库里那条《时尚服饰TVC》的提示词引用着
`{{Portrait 1..4}}`，只搬提示词就只能生成四张随机脸。实测 58 条里 46 条带参考图、0 条的
首帧图等于我们挂的封面，所以"光有提示词复刻不出来"不是错觉。

成片地址与整张画布快照来自同一个接口：

```bash
# snapshotData 是字符串化的 JSON，内容为 {nodes, edges, savedAt}
curl 'https://api.liblib.tv/api/community/project/template/detail?projectTemplateUuid=<32位uuid>'
```

早期做法是抓作品页 HTML 再对载荷做正则，现在走这个接口：体积减半，配方也是结构化的。
**不要再退回正则**——上游转义方式一变，解析器就碎，而且是静默地碎。

两处已经踩过的坑，改这块之前先看一眼：

- 快照里"资源地址"字段有字符串与数组两种形态，同一条作品里还混着出现。解码时按
  `liblibSnapshotMediaURL` 两种都收；整份文档一次解码的话，一个字段换形态赔上的是整条
  作品的配方。
- 节点要逐个解码（`decodeLibrarySnapshotNodes`），坏的跳过。画布是用户自由编织的，
  129 个节点里什么形状都有。

节点选择见 `app.inspirationRecipeFromSnapshot`：先用成片地址锚节点，锚不到再按"有参考图的
生成节点 > 有参考图的任意节点 > 任意生成节点"取最后一个。锚点只是兜底——`finalOutput`
是导出产物，58 条实测只有 1 条能在节点里对上；而"取最后一个"会撞上末尾的纯文生视频或
放大节点，所以带参考图的生成节点优先。

### 参考图：落本地、重编码、按需签名

参考图必须落本地（`data/resources/users/platform-inspiration-references/`）："使用这个创意"
时它要作为素材进入用户自己的资源库，热链的话上游一加防盗链，用户在点下去的那一刻才发现
图没了。

- 落库前统一重编码成 1280px 的 JPEG（`app.downscaleToJPEG`）：实测单张从 3MB 降到 150KB
  上下，全池 121 张合计 21MB。
- 请求时让上游先缩再传：`?x-oss-process=image/resize,m_lfit,w_2048,h_2048/format,jpg/ignore-error,1`。
  上游原图是 5504×3072 的 19MB PNG（`m_lfit` 只缩不放，实测 1920×1080 的图原样返回）。
  挂上之后全量一轮的图片流量从几百 MB 降到 74MB。这个参数只挂给已知图床
  （`*.liblib.art`）且地址本身不带查询串的情况。
- 资源 ID 由**原始**地址派生（`inspirationReferenceResourceID`），重跑只复用、不会重复下载；
  上游换了图但地址没变时加 `-overwrite`。

```bash
cd /tmp/kinotv-src/backend
CGO_ENABLED=1 GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=https://goproxy.cn,direct \
  go build -o /tmp/inspiration-videos ./cmd/inspiration-videos
cd /opt/kinotv
# 只补缺项；加 -limit 5 分批；加 -overwrite 重抓已有条目
CANVAS_BACKEND_DATA_DIR=/opt/kinotv/data CANVAS_DATABASE_DRIVER=sqlite /tmp/inspiration-videos
```

要点：

- 命令是**串行**的，每条一次详情请求 + 最多 4 张参考图（`inspirationRecipeMaxImages`，与前端
  `creationRecipeMaxImages` 对齐）；`-limit` 用来分批，**避开晚高峰**。
- 成片地址与配方各自判断缺项：库里存量的 55 条只有成片、没有配方，整条按"已抓过"跳过的话
  它们永远补不上配方。
- 有参考图抓失败时返回非零退出码。条目级输出会给出 `recipeVideoModel` 与 `recipeImages`，
  用来判断配方到底有没有落到库里——静默成功比失败更难查。
- 优先存 HLS 播放列表（`master.m3u8`，带 1080p/720p/480p 三档），探测不到才回落原片。
  播放列表地址由原片地址推导，不去详情里另找——快照里存着每个视频节点的产物，按"第一个
  m3u8"取会张冠李戴。
- 上游 CORS 是 `access-control-allow-origin: *`，因此播放走**浏览器直连上游**：服务端不中转、
  不占出口带宽，`kinotv` 只负责发页面和接口。这是这条链能上的前提，改前端播放器时不要
  退回成服务端代理。
- 前端 `hls.js` 是**动态 import**，只在真要播 HLS 时才加载；列表里不挂 `<video>`，只在弹层
  里创建、关掉即销毁。列表页并发打开几十张卡片不会拉起几十个解码器。
- 没有地址的条目（`image` 类型）只是不显示播放入口，不是故障；上游没有生成节点的条目
  （实测 2 条）拿不到配方，同样是正常状态。
- 配方图库里只存 `recipe_image_ids`，接口出口时现场签名（12 小时）成 `recipeImageUrls`，
  广场不会因为一张图失效变成死图。
- 后台保存灵感**不清空配方**：配方由抓取命令写，表单里没有它的编辑入口（见
  `TestSaveCreationInspirationKeepsRecipe`）。运营改文案时顺手清空配方会静默毁掉复刻入口。

## 备份与恢复演练

脚本都在仓库的 `scripts/kinotv/` 下，安装到 `/opt/kinotv/scripts/kinotv/`。
放在仓库里而不是只留在服务器上：服务器整台丢掉时，最不能丢的恰恰是"怎么恢复"这件事。

| 脚本 | 职责 |
| --- | --- |
| `kinotv-backup.sh` | SQLite 在线快照 + 配置 + 用户资源，按次产出整份目录 |
| `kinotv-restore.sh` | 校验备份 → 恢复到目标目录 → 可选真起一次服务演练 |
| `kinotv-restore-drill.py` | 演练里的 HTTP/SQLite 断言（登录、配置、列表） |
| `kinotv-prune-releases.sh` | 按份数裁掉发布目录下的旧前端 / 旧二进制 / 旧快照 |
| `kinotv-selftest.sh` | 改过上面任何一个之后跑一次，含负向用例 |

### 备份

```bash
/opt/kinotv/scripts/kinotv/kinotv-backup.sh            # 默认数据目录 /opt/kinotv/data
```

每次产出一个 `20261003-033001/` 形式的目录，最后才写 `done`：

```
open_ai_canvas.db  kinotv-auth.db
config/{local-model-config.json,plugin_registry.json,.settings-key}
media/resources.tar.gz
manifest.tsv        每行 <相对路径>	<sha256>	<字节数>	<权限>
done                只有全部成功才出现
```

三个容易踩的点，改动前先读：

- **数据库必须用 `sqlite3 .backup`，不能 `cp`。** 开着 WAL 时主库文件里没有尚未
  checkpoint 的事务，直接拷出来的库看着正常、实际丢最近一段写入。
- **快照要归一成单文件。** `.backup` 会连源库的 WAL 设置一起复制，留下 `-wal`/`-shm`
  边车文件。脚本会把它转成 `journal_mode=delete`，这样归档是自包含的单文件，
  异地存放、只读挂载、换任意 sqlite 版本都能直接打开。
- **`.settings-key` 必须和库同一批。** 少了它，渠道密钥解不开、资源签名对不上。

保留策略：数据库与配置 7 天，`resources` 3 天（它比库大一个数量级，用同一个保留期会
把盘吃光）。资源包被清掉后会留 `media-pruned` 标记，恢复侧据此区分"按策略清理"和
"本来就没备份"。

### 恢复与演练

```bash
# 只看文件级恢复（快，几秒）
/opt/kinotv/scripts/kinotv/kinotv-restore.sh latest --target /tmp/restore-check

# 完整演练：恢复后真起一个临时实例，走真实登录与接口
/opt/kinotv/scripts/kinotv/kinotv-restore.sh latest --target /var/lib/kinotv/restore-drill \
    --force --with-media --drill --port 18099
```

演练会断言：清单里每个文件的 sha256 都对得上、两个库 `integrity_check=ok`、
`.settings-key` 权限仍是 600、临时实例的存活与就绪探针为 200、匿名访问业务接口仍是
401、用一个恢复出来的真实账号登录成功、平台模型配置和画布/任务列表都能读。

**目标目录不允许是线上数据目录**，脚本会直接拒绝；演练只写恢复出来的副本。
只有全部通过才会更新 `<备份目录>/last-verify`，巡检靠它判断演练是否还在按期执行。

## 巡检与告警

| 脚本 | 职责 |
| --- | --- |
| `kinotv-healthcheck.sh` | 探针 + 备份与演练新鲜度 + 磁盘，异常时告警 |
| `kinotv-alert.py` | 告警外发（机器人 Webhook / 邮件） |

### 巡检脚本

```bash
/opt/kinotv/scripts/kinotv/kinotv-healthcheck.sh
```

检查五项：`/api/health/live` 与 `/api/health/ready` 的 HTTP 状态、最新备份的年龄、
上次恢复演练的时间、磁盘使用率。**看 HTTP 而不是端口**：进程活着但数据库锁死、
迁移卡住、插件加载失败时，`netstat` 一样显示端口在听，只有真发一次请求才看得出来。

异常时退出码为 1（systemd 里就是 `failed`），并调用 `kinotv-alert.py` 外发一次。
去重规则：`OK→ALERT` 立即发，持续异常每 6 小时重发一次，`ALERT→OK` 发一条恢复通知。
没有这一层，5 分钟一次的定时任务会把告警出口刷爆，最后所有人都把它静音。

告警出口配置在 `/etc/kinotv-alert.env`（600），配哪个用哪个：

```
ALERT_WEBHOOK_URL=        # 钉钉/企业微信/飞书群机器人
ALERT_EMAIL_TO=           # 邮件，走 BEEFTV_SMTP_*
```

两个出口都没配时脚本会明说"仅记录日志"并且不影响退出码；配了但发送失败会返回非 0，
不会静默吞掉。

### 定时任务

```bash
install -m 644 scripts/kinotv/systemd/*.service scripts/kinotv/systemd/*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now kinotv-backup.timer kinotv-healthcheck.timer kinotv-restore-drill.timer kinotv-prune-releases.timer
```

| 定时器 | 频率 | 作用 |
| --- | --- | --- |
| `kinotv-backup.timer` | 每天 03:30 | 备份 |
| `kinotv-healthcheck.timer` | 每 5 分钟 | 巡检，异常告警 |
| `kinotv-restore-drill.timer` | 每周日 04:30 | 恢复演练 |
| `kinotv-prune-releases.timer` | 每天 04:00 | 清理发布备份，见「磁盘维护」 |

四个 timer 都带 `Persistent=true`（关机错过会补跑）——漏掉一天备份却毫无痕迹，
是这类任务里最难发现的一种故障。

**这套与 `/root` 下旧脚本的关系。** 机器上原本有 `backup-db.sh`（PostgreSQL/MySQL）、
`backup-kinotv.sh`（旧版 KinoTV 备份）、`check-server.sh`（端口与进程巡检）、
`notify.py`（告警）。这套脚本是它们的替代品，多了三样旧脚本没有的东西：
备份产出自带校验清单与 `done` 标记、恢复演练、以及基于 HTTP 的探活。
切换前先让两套并行跑几天，比对结果一致再停掉旧的 cron。

## 磁盘维护

每轮发布前会把前端复制成 `web.bak-<时间戳>`（每份约 103M），并保留旧二进制。它们只用于
回滚，**不需要长期堆**——这对文件一个多月能攒到好几个 G，所以交给定时任务每天裁一次，
不再靠人记得敲 `rm`。

注意这里**只裁备份目录**。线上目录 `web/` 里的旧 chunk 是有意留着的（见上文
「前端发布：覆盖合并」），它们要按文件年龄单独淘汰，别用这份按份数的策略去删。
同一条定时任务里已经带了这一段：在 `web/static` 下淘汰超过 `--web-stale-days`
（默认 30 天）的文件，只删普通文件、不删目录，`--web-stale-days 0` 可整条关闭。

```bash
# 看一遍将删除什么（不动文件）
/opt/kinotv/scripts/kinotv/kinotv-prune-releases.sh --dry-run

# 实际执行（kinotv-prune-releases.timer 每天 04:00 自动跑这条）
/opt/kinotv/scripts/kinotv/kinotv-prune-releases.sh
```

保留策略按**份数**而不是天数：这些是发布回滚物，生命周期跟着"上次发布"走。按天数算的话，
长时间不发布时旧的会因过期被删光，真出事时一份都回滚不了。

| 类别 | 默认保留 |
| --- | --- |
| `web.bak-*`、`kinotv-server.bak-*` | 各 3 份 |
| `open_ai_canvas.db.bak-*`、`kinotv-auth.db.bak-*` | 各 3 份 |
| `resources.bak-*.tgz` | 2 份 |
| `backups/` 下的发布前手工快照 | 2 份 |

几条硬约束，改脚本时别拆掉：

- 每个类别至少留 1 份，`--keep-*` 传 0 也不生效；"全删光"不交给脚本决定。
- 排序认文件名里的时间戳（`yyyymmdd-hhmmss`），取不到才退回 mtime。**别改成按 mtime 排**：
  发布备份是 `cp -a` / `mv` 出来的，目录 mtime 继承自源目录，实测
  `kinotv-server.bak-20261005-1516` 的 mtime 是 10-04 20:58，比自己早一天——按 mtime
  排会把最新那份回滚物排到很旧的位置。
- 只删名字匹配已知模式的条目——不认识的、以及 `web/`、`kinotv-server`、`data/`、
  `src.new*` 这些活文件一律不碰。
- 与备份脚本一样带锁，且超过 6 小时的锁视为残留自动接管。

清理记录写在 `/var/log/kinotv-prune.log`；有条目删不掉时退出码为 1，systemd 里就是
`failed`。发布目录如果配错（不存在），脚本直接失败而不是"成功清理 0 份"。

`/root/backups/kinotv`（真正的数据备份）由 `kinotv-backup.sh` 自己裁剪：库与配置留
7 天、资源包留 3 天，见 `KINOTV_BACKUP_KEEP_DAYS` / `KINOTV_BACKUP_KEEP_MEDIA_DAYS`。

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
