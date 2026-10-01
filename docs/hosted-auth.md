# 托管登录（SaaS 账号体系）

这份文档描述 BeefTV 在 SaaS 形态下的登录模块：账号从哪来、代码放在哪、怎么在本地跑通，
以及目前还没有做完的部分。

## 边界

登录属于**仅服务端存在**的能力。桌面构建（`cmd/desktop`）不注册登录、不连接账号库，
依旧是一个单用户本地工作区；这条边界由 `backend/internal/bootstrap/desktop_dependencies_test.go`
的 `TestDesktopDependencyBoundary` 强制（该测试会遍历编译器依赖图，禁止
`infinite-canvas/backend/internal/auth` 出现在桌面二进制里）。

因此：

- `internal/auth`：纯逻辑（会话、验证码、OAuth、策略编排），不含宿主装配。
- `internal/hosted`：只有服务端会链接的装配层，负责连账号库、注入中间件与路由。
- `internal/bootstrap`：通过 `Config.HostedFactory` 注入托管能力，自身不引用任何认证包。

## 账号数据归属

账号表沿用 CanvasMind（Prisma + MySQL）已有的结构，BeefTV 只做读写投影：

| 表 | 用途 |
| --- | --- |
| `app_users` | 账号主体 |
| `app_sessions` | 会话（存令牌摘要，可即时吊销） |
| `auth_verification_codes` | 验证码（明文入库，响应对应字段已物理删除） |
| `app_user_auth_identities` | 第三方身份绑定 |
| `auth_method_configs` | 登录方式开关（由 CanvasMind 后台维护） |

**BeefTV 绝不对这张库执行 AutoMigrate**：结构由 Prisma 迁移管理，GORM 的自动迁移会按自己的
类型推断改写列定义。本地 SQLite 开发库是唯一例外，见下面的 `EnsureDevSchema`。

生产启动时会先做一次 `auth.VerifySchema`，缺表直接启动失败，而不是等到用户点登录才报 500。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `CANVAS_AUTH_DATABASE_URL` | 账号库 DSN。**留空即关闭登录模块**，服务端退回单工作区模式 |
| `CANVAS_AUTH_DATABASE_DRIVER` | 驱动，默认 `mysql`；本地开发可填 `sqlite` |
| `CANVAS_AUTH_STATE_SECRET` | OAuth state 签名密钥，生产必须注入 |
| `CANVAS_AUTH_COOKIE_SECURE` | HTTPS 部署必须为 `true` |
| `BEEFTV_GITHUB_CLIENT_ID` / `BEEFTV_GITHUB_CLIENT_SECRET` | GitHub OAuth 凭据（也可写在 `auth_method_configs.config_json`） |
| `BEEFTV_GITHUB_REDIRECT_URI` | GitHub 回调地址，应指向 `https://<域名>/api/auth/oauth/callback` |
| `BEEFTV_SMTP_HOST` / `_PORT` / `_USERNAME` / `_PASSWORD` / `_FROM` | 邮箱验证码投递；未配置时验证码只写服务端日志 |
| `BEEFTV_SMS_ACCESS_KEY_ID` / `BEEFTV_SMS_ACCESS_KEY_SECRET` | 阿里云短信凭据；未配置时短信验证码只写服务端日志 |
| `BEEFTV_SMS_SIGN_NAME` / `BEEFTV_SMS_TEMPLATE_CODE` | 短信签名与模板编号，配了 AccessKey 后必填，缺失会直接启动失败 |
| `BEEFTV_SMS_TEMPLATE_PARAM_KEY` | 模板里验证码变量的名字，默认 `code`（模板写作 `${code}`） |
| `BEEFTV_SMS_REGION_ID` / `BEEFTV_SMS_ENDPOINT` | 覆盖短信接入点，默认 `cn-hangzhou` 与 `dysmsapi.aliyuncs.com` |

未配置 SMTP 或短信凭据时，启动日志会明确提示「禁止用于生产」——日志可见者等于可以登录任意账号。
短信通道手写阿里云 RPC 签名（`aliyun*` 系列函数），只依赖标准库，不引入 SDK。
`PHONE_CODE` 与 `EMAIL_CODE` 完全同构：同一套通道抽象、同一套注册/登录语义。

## 本地跑通

```bash
cd backend
CANVAS_AUTH_DATABASE_URL=$PWD/.local/auth-dev.db \
CANVAS_AUTH_STATE_SECRET=local-dev-secret \
CANVAS_BACKEND_ADDR=127.0.0.1:8080 \
go run ./cmd/server
```

`sqlite` 驱动下会自动建表并补一批默认登录方式（`EnsureDevSchema`），GitHub 一项只有给了
client id/secret 才会置为启用。随后：

```bash
curl -s localhost:8080/api/auth/methods
curl -s -X POST localhost:8080/api/auth/verification-code \
  -H 'Content-Type: application/json' -d '{"methodType":"EMAIL_CODE","target":"you@example.com"}'
# 验证码在服务端日志里，复制后登录
curl -s -c cookies.txt -X POST localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"methodType":"EMAIL_CODE","target":"you@example.com","code":"<码>"}'
curl -s -b cookies.txt localhost:8080/api/workspace/bootstrap
```

## 多租户接缝

会话中的用户 ID 直接作为工作区 ID（`workspaces.id == app_users.id`）：

- 请求进入时由 `hosted.Extension.WorkspaceMiddleware` 解析会话并写入工作区作用域，
  业务代码只读作用域，不自行解析 Cookie，因此不存在第二条身份来源。
- 新账号首次访问时会调用 `app.Service.EnsureWorkspace` 建出工作区行（进程内缓存，避免每请求写库）。
- 资产路径已经是 `users/<userID>/...`，与工作区 ID 对齐。

认证路由（`/api/auth/*`）不参与会话校验，否则用户连登录页都打不开。

## 相对 CanvasMind 的加固

- 验证码响应结构里没有 `code` 字段，编译期就杜绝了 CanvasMind `debugCode` 那类回显。
- 验证码用 `crypto/rand` 生成；会话令牌为不透明随机串，库内只存 sha256 摘要。
- 会话在 30 天滑动有效期之上加了 90 天绝对上限。
- `DISABLED` 账号的会话立即失效并批量吊销。
- `last_active_at` 写入按 5 分钟节流；验证码下发有 60 秒冷却。
- OAuth state 走 HMAC 签名 + 10 分钟 TTL；GitHub 仅在 `verified: true` 时按邮箱合并账号。

## 密码通道

普通用户的密码登录走 `PASSWORD`，标识可以是邮箱或手机号；管理员那条 `ADMIN_PASSWORD`
按 `username` 列寻址，两者不复用同一条策略。

- 登录：`target` + `password`。
- 注册：`target` + `password` + `agreementVersion`，**不需要验证码**。短信通道审批没下来时，
  这是唯一一条不依赖任何第三方投递能力的注册路径。
- 密码按 scrypt 落 `app_users.password_hash`，参数与 CanvasMind 的 `hashUserPassword` 一致，
  存量哈希可直接验证。
- 失败节流：同一标识或来源 IP 在 15 分钟内失败 5 次后返回 429。计数是进程内的，
  单实例有效；多实例部署必须换成共享存储，否则有效阈值会放大到「副本数 × 5」。
- 密码注册没有验证过标识归属，所以 `app_user_auth_identities.is_verified` 落 `0`，
  不能和验证码通道的已核验身份混为一谈。
- 登录路径**不写身份绑定**：`resolveExistingUser` 会给存量账号补 identity 记录，
  复用它等于给未认证请求一个可以无限触发的写放大入口。

### 上线前必做：新增枚举值

`auth_method_configs.method_type` 与 `app_user_auth_identities.method_type` 都是 MySQL enum 列。
新增 `PASSWORD` 取值需要在 CanvasMind 侧加一条 Prisma migration，否则生产写入会被数据库
直接拒绝（本地 sqlite 没有这个限制，本地跑通不代表生产能跑）。

## 品牌（KinoTV）

品牌名 `KinoTV`，slug `kinotv`，标记是 `/kino-mark.svg`（圆角取景框 + 字母 K）。默认值分布在
两处，换品牌时必须一起改：

- 前端 `web/src/stores/use-appearance-store.ts` 的 `DEFAULT_PUBLIC_APPEARANCE`：首屏品牌名、
  SEO 标题与描述、页脚版权，以及 `BrandLogo` 取不到配置时的回落。
- 后端 `backend/internal/app/appearance.go` 的 `defaultAppearance*` 常量：管理端外观接口的默认值。

首字母回落图标、`favicon`、首屏加载动画、侧栏与欢迎页文案都跟着品牌走。分享图是
`web/public/og-kinotv.png`（1200×630），由 `index.html` 的 `og:image` 静态引用——爬虫不执行 JS，
所以这几条 meta 只能写死在模板里，运行时注入的 `og:*` 是给浏览器用的，两者不能互相替代。

刻意**没有**跟着改的标识符，改了会破坏兼容或既有外部契约：

| 保留 | 原因 |
| --- | --- |
| `beeftv.plugin/v1`、`.beeftv-plugin` | 插件包格式标识，改了已装插件全部失效 |
| `beefapi`、`beeftv-enterprise` | 与企业连接服务 BeefAPI 的既有契约 |
| `BEEFTV_HOSTED_AUTH`、`BEEFTV_SMS_*` 等环境变量 | 部署侧按这套名字配置，改名等于要求所有环境同步改 |
| 桌面自动更新的路径与 `.app` 名称 | 与已签名的更新产物绑定 |

`web/public/beef-logo.png`、`beef-mark.png`、`logo.svg` 已无任何代码引用，但暂未删除：存量数据库的
`system_setting.appearance` 里可能仍存着 `/logo.svg`，删文件会让那个值 404。确认线上没有引用后再清理。

## 账号注销

用户自助注销，入口在「设置 → 账户与用量 → 注销账号」。流程是**申请 → 冷静期 → 到期匿名化**，
不是点一下即删：注销不可逆，而账号背后可能还挂着没跑完的生成任务、未结算的订单和没导出的作品。

| 动作 | 接口 | 说明 |
| --- | --- | --- |
| 查询状态 | `GET /api/finance/account/deletion` | 无申请时 `status=NONE` |
| 提交申请 | `POST /api/finance/account/deletion` | 需 `methodType` + `code`，可选 `reason` |
| 撤销申请 | `DELETE /api/finance/account/deletion` | 冷静期内可撤销；无申请时 404 |

实现落在 `auth` 域的 `account_deletion.go`（表 `auth_account_deletions`）与托管层的
`hosted/account_deletion.go`（路由 + 到期执行协程）。

**确认身份**：验证码必须发到账号自己绑定的邮箱或手机号。请求体里的目标不参与校验——
否则任何人只要有一个收得到验证码的邮箱，就能注销别人的账号。验证码复用登录场景
（同一 `scene`），因此不存在「注销专用验证码」这种旁路。

**冷静期**：默认 7 天（`AccountDeletionGraceDays`），到期前可随时撤销。同一账号同时只
允许一条待执行申请，重复申请按重新计时处理；两条并存会让到期扫描对同一个人跑两遍匿名化。

**到期执行**：托管层协程每小时执行一次（启动时先扫一遍，补上停机期间到期的申请）。
执行是**匿名化**而不是物理删除——账号行保留（账本、订单、审计按 `user_id` 外键指向它），
但 `username`/`email`/`phone`/`password_hash`/`avatar_url` 置 NULL、展示名改为「已注销用户」、
状态改为 `DISABLED`，并删除第三方身份绑定、吊销全部会话。
三个唯一索引上的列必须置 NULL 而不是空串：空串会让第二个注销的账号撞唯一约束而失败。

**保留范围**：订单、积分流水等交易记录按法律法规要求继续保留；账号内剩余积分不折现退还。
这两条都写在注销弹窗里，用户按下按钮前能看到。

## 用户中心

托管形态的 `/settings` 不摆任何配置表单（上游凭证、渠道与计费都由平台持有），整页就是
账号自助。入口是账户菜单里的「用户中心」——侧栏里没有它的位置：这一页答的是「我是谁、
我的账号现在什么状态」，与工作区里的创作入口不同类。侧栏的模型配置入口在托管产物里被
摇树删除后，`/settings` 就只剩这一页，所以账户菜单里那一条是它唯一的入口。

| 主题 | 接口 | 实现 |
| --- | --- | --- |
| 积分（首屏） | `GET /api/finance/wallet` | `hosted/credit.go` → `pages/settings/account-credits-card.tsx` |
| 资料（昵称 / 头像） | `GET`、`PATCH /api/finance/account/profile` | `auth/account_profile.go` + `hosted/account_profile.go` |
| 密码 | `GET`、`POST`、`PUT /api/finance/account/password` | `auth/account_password.go` + `hosted/account_password.go` |
| 登录设备（更多） | `GET /api/finance/account/sessions`、`DELETE /api/finance/account/sessions/:id`、`POST /api/finance/account/sessions/revoke-others` | `auth/account_sessions.go` + `hosted/account_sessions.go` |
| 身份绑定（更多） | `GET /api/finance/account/bindings`、`POST /api/finance/account/bindings/code`、`POST /api/finance/account/bindings` | `auth/account_bindings.go` + `hosted/account_bindings.go` |
| 用量与协议留痕（更多） | `GET /api/finance/account` | `hosted/admin.go` → `pages/settings/account-usage-card.tsx` |
| 我的投稿（更多） | 见「发布到灵感广场」 | `pages/settings/my-creation-posts-card.tsx` |
| 注销（更多） | `GET`/`POST`/`DELETE /api/finance/account/deletion` | `hosted/account_deletion.go` |

**信息架构**：首屏只有三张卡——积分、资料、密码，按"还剩多少 → 我叫什么 → 密码怎么改"
排。登录设备、身份绑定、我的投稿、用量与协议、注销账号收进「更多设置」，默认收起且**不收
起时不渲染**（五张子卡的请求一并不发）。这五个能力低频且多为破坏性动作，摊在首屏会把
"看一眼余额"变成一次浏览任务。

前端一个功能一个文件，聚合在 `pages/settings/account-center.tsx`，样式单独放
`pages/settings/account-center.css`（不往 `globals.css` 里再堆一段）；三张主卡各自加载、
各自失败、各自重试——共享一个请求之后，任何一块抖动都会拖垮整页。接口封装同样按主题
分文件（`services/api/account-profile.ts` / `account-security.ts` / `account-bindings.ts`）。

**密码卡不依赖绑定接口**：密码状态与绑定关系是两次独立请求（`Promise.allSettled`），
绑定接口失败只影响"首次设置密码"那一支，已经有密码的账号照样能改——它们本来就不需要
验证码。第一版用 `Promise.all` 把两者绑死，绑定接口一挂整张卡就只剩一句"读不到状态"。

**账号主体只有会话**：这一组接口的请求体里不接受任何用户标识，因此不存在「传别人的 ID」
的入口。与 `/finance/*` 其余部分同一条铁律。

**公共前置**：四组写操作共用 `auth/account_guard.go` 的 `accountForSelfService`（账号
存在且状态可用）、`accountVerificationTarget` 与 `consumeAccountCode`（验证码必须发到并
校验账号**自己绑定**的地址）。不抽这一段，四条路径会各自长出一份略有差异的校验。

**密码**：没有密码的走「验证码 → 新密码」，已有密码的走「旧密码 → 新密码」。两条路径
都保留发起操作的这台设备、吊销其余会话——改完密码旧会话继续有效，正是账号被盗后攻击者
最想要的结果。密码只卡长度（≥8，上下限由服务端下发，表单不写死），不强制字符类别组合。

**换绑**：分两步——先给新地址发码，再带码确认；成功后给**旧地址**发一条变更通知。
少了新地址这一步，一次手误就能把账号绑到一个永远收不到验证码的地址上，而那种状态下
用户连注销都做不了（注销同样要验证码）。

## 尚未完成

- **GitHub OAuth**：需要注册 OAuth App 并把回调填成 `https://<域名>/api/auth/oauth/callback`。
- **计费对接**：CanvasMind 的网关扣费仍有审计遗留（chat 端点未鉴权、异步视频任务失败不退款、
  充值无支付校验），BeefTV 借道计费前需要先补齐。
- **工作区展示名与契约**：`/api/workspace/bootstrap` 的 `workspace.name` 仍是桌面契约里的
  `本地工作区`，`profile` 仍是 `local`；SaaS 前端接入时需要一并调整。
- **企业连接（BeefAPI）仍是单租户**：其状态与 provider 配置存放在 dataDir 的单一文件里，
  按账号隔离是独立的一块工作。
- **忘记密码自助找回**：用户中心已能设置密码（验证码）与修改密码（旧密码），但「已有密码
  却忘了」这条路径还没有——既没有邮件重置链接，也没有凭验证码直接重设的入口。这类账号
  不会因此登不进来（验证码通道照常可用），但想恢复密码只能走后台的
  `POST /api/admin/users/:id/password` 由运营重置。
- **验证码尝试次数上限**：6 位码在 5 分钟内可被枚举。要封住需要在 CanvasMind 侧加
  「失败次数」列并让 `ConsumeCode` 累加，属于跨仓改动，尚未做。

## 前端登录页

界面在 `web/src/features/hosted-auth/`：

| 文件 | 职责 |
| --- | --- |
| `api.ts` | 调用 `/api/auth/*`，只有这一层直接拼这些路径 |
| `gate.tsx` | 形态探测与会话判定，纯判定逻辑在 `resolveHostedAuthGatePhase` |
| `login-page.tsx` | 登录/注册表单与 GitHub 入口，含 OAuth 回调处理 |
| `login-page.css` | 登录场景的取景网格、监视器与表单卡样式 |
| `sidebar-footer.tsx` | 左侧 tab 栏底部的退出入口 |

### 交互契约

界面只有**一个标识字段**（邮箱或手机号，形态自动判定）和一个因子字段。不再有
「邮箱 / 手机号 / 密码」渠道切换器，也不存在「先声明走哪条通道」这一步——登录方式按
`methods` 决定默认值与可切换范围：

- 默认因子是**密码**（不依赖 SMTP/短信，是唯一一定能用的那条）；验证码是标识标签行上的
  一个次要动作，切因子**只清另一个因子的字段，不清标识**。
- 标识能收什么由已开启的通道决定：密码通道两种都收，验证码通道按形态一条一条来。
  形态对应的通道没开时，字段校验直接给出具体原因，不会放行到一次必然失败的提交。
- 「发送验证码」在标识形态明确且通道可用之前保持禁用——禁用态本身就是说明，比点一下
  再弹「格式不对」少一次往返。
- 标识未注册时后端返回 404，界面据此切到注册并保留已填标识；未开放注册时只报错不引导。

### 字体

品牌字系与上游刻意分开（`web/src/styles/globals.css` 的 `--font-display/sans/mono`，
字体包在 `web/src/main.tsx` 里引入）：

| 令牌 | 字体 | 用途 |
| --- | --- | --- |
| `--font-display` | Space Grotesk | 品牌字标、页面标题、主按钮 |
| `--font-sans` | Geist | 界面正文与控件 |
| `--font-mono` | Geist Mono | 等宽微标（`SIGN IN`、`CANVAS`、验证码与倒计时） |

中文没有本地字体包（体积），按 `PingFang SC → Hiragino Sans GB → Microsoft YaHei` 回落；
上游的 Inter + JetBrains Mono 已从依赖里移除。

退出入口挂在 `workspace-sidebar-nav.tsx` 的 footer 区（与主题切换同一区域），但组件本身在
托管模块内：侧边栏只保留一个受开关保护的懒加载引用，`footer: []` 这条上游断言不受影响。
点击后先让服务端吊销会话，再清本地账号状态并整页回到入口——**必须整页重载**，
否则内存里会留着上一个账号的画布与素材状态。

**为什么不在 `src/pages/auth/login.tsx`**：上游有守卫测试
（`web/test/local-only-source-boundary.test.ts`、`local-workspace-bootstrap.test.ts`）断言
该路径必须不存在、且 `router.tsx` 不得出现 `path: "/login"`。这些断言是"公开发布物里没有托管
UI"这条边界的一部分，保留它们比绕开更有价值，因此登录界面收在单一 feature 目录里，
由构建开关控制是否进入产物。

### 构建开关

`BEEFTV_HOSTED_AUTH`（`web/hosted-auth-build-mode.ts`）默认**关闭**：

| 构建 | 是否含登录界面 |
| --- | --- |
| `bun run dev` / `bun run build` / `build:desktop` / 本地发布校验 | 否 |
| `bun run dev:hosted` / `bun run build:hosted` | 是 |
| Dockerfile（云端工作台镜像）、`web/vercel.json` | 是 |

关闭时 `app-providers.tsx` 里的 `resolveHostedAuthGate` 返回 null，动态 import 变成死代码被摇树
删除——与 `devRoutes` 处理实验路由的做法一致，保证本地产物里没有登录代码：

```bash
cd web && bun run build && rg -l "登录创作空间|auth/oauth/callback" dist/   # 无输出
BEEFTV_HOSTED_AUTH=1 bun run build && ls dist/static | grep hosted-auth      # 有产物
```

### 门的语义

启动时探测 `GET /api/auth/methods`：

- **404** → 本地/桌面形态，整道门透传，桌面行为与改动前完全一致。
- **有登录方式** → 再查 `/api/auth/session`；无会话则渲染登录页，有会话则放行。
- **其他错误**（后端 5xx、网络失败）→ 停在加载态，**不放行**。

最后一条是刻意的不对称：工作区水合在拿不到 bootstrap 时会回落成本地工作区，如果探测失败时
直接放行，托管部署就会在后端故障期间变成无账号的本地会话。这条不变量由
`web/test/hosted-auth-flow.test.tsx` 的「探测失败时保持加载态，绝不放行」固定。

对应的后端边界由 `backend/internal/bootstrap/hosted_hook_test.go` 固定：没有注入
`HostedFactory` 时 `/api/auth/methods` 必须是 404、业务接口不要求登录。

## 模型配置（平台下发）

托管形态里用户不填 Base URL、也不接触 API Key：模型目录由平台下发，执行凭证留在服务端。

### 开关语义

| 开关 | 托管默认 | 效果 |
| --- | --- | --- |
| `customChannels` | **关闭**（首启由 `EnsureHostedFeatureDefaults` 写入） | 前端只保留系统渠道，设置页不出现个人渠道表单 |
| `frontendModels` | 关闭 | 目录 `source=system`（渠道目录），而不是 `source=frontend`（逻辑模型） |

运维在后台改过功能开放之后，启动流程不再覆盖它（`platform.EnsureFeatureAvailability` 只在配置
缺失时写入）。自建渠道在托管必须保持关闭：用户直连上游会绕过平台的模型与计费。服务端还有一层
独立守卫——`requireCustomChannelsForTaskInput` 与 `/api/ai/custom` 都要求该开关，即使前端被改动，
服务端也不会替用户转发自建渠道请求。

### 服务实例模式

托管实例必须用 hosted 模式构造服务（`bootstrap/runtime.go` 依据 `HostedFactory` 判定）：

- 本地模式会把系统渠道判成非法配置（`resolveProviderConfig` 返回"本地工作区不支持系统渠道配置"），
  选中平台模型的每一次生成都会失败；
- 本地模式还把功能开放读成硬编码的本地默认值，运维在后台改的开关完全不生效。

回归测试：`backend/internal/app/system_channel_mode_test.go`。

### 用户端入口

关闭自建渠道后，用户端不再出现任何模型配置入口：

- 侧栏、顶栏、命令面板的「模型配置」项统一走 `userChannelConfigVisible()`
  （`src/lib/user-channel-ui.ts`）：托管产物里它恒为 `false`，本地/桌面才看功能开关。
  只看 `customChannelsEnabled` 是不够的——运维一旦在后台打开该开关，SaaS 前台就会重新
  长出配置入口，而托管实例上这个开关只剩接口与计费语义；
- `/settings` 在托管形态只剩一页说明（模型由平台提供），不渲染渠道表单；画布里
  「去配置模型」的跳转会落在这个说明页而不是空表单上。

因为配置页没了，默认模型必须自动补齐：目录合并进渠道后，
`normalizeConfigSnapshot` 会把 `imageModel`/`videoModel`/`textModel`/`audioModel`
回落到对应能力的第一个可用模型，用户不做任何设置也能直接创作。

闭环已端到端验证：管理员在后台 UI 新建渠道（填上游地址与密钥）→ 在该渠道下新增模型
（平台标识 / 上游标识 / 能力 / 协议 / 启用）→ 普通用户刷新画布后模型下拉按渠道出现该模型
→ 选中生成成功。普通用户全程看不到地址与密钥，上游收到的是服务端注入的渠道凭证。

引导路径也要跟着收敛：画布与创作流程里"模型未就绪"原本一律跳
`/settings?section=channels&continue=1`，在托管实例上那只是一页说明，用户被扔过去
既不能操作也不知道原因。`navigateToSettings` 现在先问 `canOpenChannelSettings()`，
托管形态直接拦下不跳转。这里**不要**再补一个全局 toast 兜底：`styles/globals.css`
已经用 `display: none !important` 关掉了 antd 的 message/notification，反馈要落在画布内。

所以可见反馈改在入口本身表达——平台没给某个能力配模型时：

- `canvas-node-prompt-panel.tsx` 把提交按钮置灰（`hasUsableModel`）；
- `model-picker.tsx` 把触发器置灰，并显示 `emptyModelLabel` 的原因
  （"暂无支持当前输入的视频模型"），而不是上游内置的占位模型名
  （`2.0` / `Lib Image 2.5 Pro` / `Seed Audio 1.0`，会让人以为该能力已经可用）。

本地/桌面形态不受影响：那里的功能开关默认开启，模型配置是桌面产品自己的界面。

### 读路径与写路径

`GET /api/model-catalog` 是**托管专属**路由（只挂在 `hosted != nil` 分支上；
`internal/handler/api_test.go` 断言桌面产物不得暴露它）。它返回脱敏读模型——渠道名、模型名、
能力、协议——**不含 Base URL 与密钥**；单个损坏模型被隔离并记录诊断，不拖垮整个目录。

前端侧 `src/features/hosted-auth/system-models.ts` 负责拉取与投影：

- 目录 → 渠道快照（`scope=system`、`baseUrl=/api/ai/system/<id>`、`apiKey` 只是占位）；
- 系统渠道的模型必须带 `modelProfiles` 才会进入模型下拉（`hasSystemModelProfile`）；
- 本地水合契约会清掉 `scope=system` 的渠道，因此合并按签名幂等重放，刷新后仍然可见。

### 平台转发端点（已接通）

浏览器直连的路径（流式文本、生图、模型列表）会把 `/api/ai/system/<id>/*path` 打回本进程，
由 `internal/handler/system_proxy.go` 的 `ANY /api/ai/system/:channelId/*path` 承接：

- **协议与模型只认服务端记录**：带 `model` 的请求先查 `channel_models`（协议、别名、真实 SKU），
  再按协议套路径白名单；不带 `model` 的请求（模型列表、异步任务轮询）按渠道实际配置过的协议探测。
- **别名 → SKU 重写**：请求体（`{"model": ...}`）与 Gemini 路径里的模型名都会替换成
  `provider_model_key`，平台对外只暴露别名。
- **凭证与请求头由服务端注入**：Base URL、API Key/Secret Key、渠道级请求头都取自渠道记录；
  浏览器带来的 `Cookie`/`Origin`/`Authorization` 一律不转发。查询串会转发（Gemini 的 `alt=sse`），
  但 `key`/`api_key`/`access_token`/`token` 直接拒绝，避免把转发端点当成密钥透传通道。
- **响应脱敏**：JSON、二进制与 SSE 流都会抹掉渠道密钥；流式边到边发（`X-Accel-Buffering: no`）。
- **限流与并发**：用户级频控用 `runtimePolicy.request.systemRelayPerMinute`，单次体积用
  `systemRelayRequestMB`/`systemRelayResponseMB`，超时用 `systemRelayTimeoutMinutes`，
  并发槽复用渠道自身的并发上限（`AcquireChannelSlot`）。
- **`GET /models` 不回源**：上游会返回真实 SKU，原样转发等于公布平台后端是谁；
  这里改成用平台目录回答，只回该渠道已授权的模型键（`SystemChannelEnabledModels`）。

守卫：`frontendModels` 打开时该端点直接 403——前台模型目录与平台托管模型是互斥形态，
不能出现"用户自建渠道 + 平台凭证"混用。桌面/本地产物不注册这条路由
（`internal/handler/api_test.go` 断言），路由注册在 desktop 路由之后，才拿得到
`RuntimeDependenciesMiddleware` 提供的频控与并发依赖。

回归测试：`backend/internal/handler/system_relay_test.go`、`system_proxy_stream_test.go`。

**计费卡点**：`proxySystemRelayRequest` 里真正发请求之前是唯一的平台凭证出网点，
注释标出了 quote（转发前预估并失败关闭）与 settle（响应后按 token/媒体数量回写）的位置；
任务管线路径（`POST /api/tasks` + `channelId`）另有一处上游调用，接计费时两处都要覆盖。

## 管理后台（平台配置）

运营后台在 `/admin`，只存在于托管构建：路由与侧栏入口都写在 `__BEEFTV_HOSTED_AUTH__`
开关后面，本地/桌面产物里既没有入口也没有后台代码（`web/test/admin-console-boundary.test.ts`
是这条边界的回归测试）。

### 后端接口

`internal/handler/admin.go` 把整组 `/api/admin` 挂在 `requireAdminMiddleware` 下
（未登录 401、非管理员 403）。守卫挂在 group 上而不是逐个 handler，新增路由忘记鉴权
就会变成接口级漏洞。业务规则、密钥加密、审计与一致性检查都在 `internal/app` 与
`internal/platform`，管理端和后台任务共用同一套实现：

| 接口 | 说明 |
| --- | --- |
| `GET/POST /admin/channels`、`GET/PUT/DELETE /admin/channels/:id`、`POST .../duplicate` | 系统渠道增删改查与复制 |
| `GET/POST /admin/channels/:id/models`、`PUT/DELETE .../models/:modelId`、`POST .../models/batch-delete` | 渠道模型增删改查（批量删除要求整批可删，避免部分成功） |
| `GET .../models/upstream`、`POST .../models/import` | 上游目录只读预览 + 只导入明确选中的项 |
| `POST .../models/test` | 连通性测试在服务端执行，密钥不出库 |
| `GET/PUT /admin/order`、`GET/PUT /admin/channels/:id/order` | 渠道与模型排序（`expectedIds` 乐观并发，冲突回 409） |
| `GET/PUT /admin/features` | 功能开放，决定前台是「平台托管模型」还是「用户自配模型」 |
| `GET/PUT/DELETE /admin/runtime-policy` | 运行时策略的读取、覆盖与恢复默认 |
| `GET /admin/protocols` | 模型请求协议目录（见下） |

`GET /admin/protocols` 的数据源必须是**保存时校验用的那份合并注册表**
（`Service.protocolRegistry()`）。管理端另取一份"官方协议清单"，就会重新出现
"下拉里能选中、保存时报请选择有效的模型请求协议"的分裂——上一轮管理接口的保存失败正是
这么来的，所以协议列表由服务端下发，前端不再自建常量表。

### 托管共享表

`admin_audit_events` 属于托管侧的审计流水，**不在** `database.LocalModels()` 里：
`bootstrap` 的结构边界测试要求桌面产物证明自己的结构里没有托管表。因此建表责任落在
托管装配这一层：`database.MigrateHostedSharedSchema` 只在配置了 `HostedFactory` 时执行，
禁用自动迁移的部署则由 `RequireHostedSharedSchema` 在启动阶段直接报错，而不是让第一个
管理员写操作在运行期撞上 `no such table`。

### 密钥可见性

管理端的渠道读模型只回 `hasApiKey`/`hasSecretKey`，不回密钥原文；编辑时密钥字段留空
表示「不修改」，而不是清空。这也是为什么前端把空串当成 `undefined` 提交。

### 前端

界面在 `web/src/features/admin-console/`，恒为暗色（与登录页共用 `--auth-*` 皮肤令牌）：

| 文件 | 职责 |
| --- | --- |
| `api.ts` | 只有这一层直接拼 `/api/admin/*`，其余源码不得出现这些路径 |
| `admin-console.tsx` | 顶栏、分区导航与暗色主题装配 |
| `channels-pane.tsx` | 渠道列表（含排序）、模型表格、渠道/模型编辑、上游拉取、连通性测试 |
| `features-pane.tsx` | 功能开放开关与后果说明 |
| `policy-pane.tsx` | 运行时策略数值分组编辑与恢复默认 |
| `require-admin.tsx` | 入口守卫：会话未水合停在加载态，非管理员跳回工作台 |

后台不引用本地工作区仓储（`use-config-store`、localforage 等）：平台配置与"我自己的工作区"
是两套持久化，混用会让后台读写落到本地快照上。
