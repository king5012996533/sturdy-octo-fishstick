# MCP 接入（外部 Agent 驱动 Kinotv）

Kinotv 附带一个 stdio MCP server（`backend/cmd/mcp`）。Claude Code、Claude Desktop 或其他 MCP 客户端可以通过它拉起 Agent、在画布上执行创作任务、处理审批并查询生成结果。

MCP server 有两种运行方式：

- **内嵌本地模式（默认）**：启动一个仅监听 `127.0.0.1` 的桌面 profile 后端实例，用随机启动令牌调用 `/agent/runs`、`/tasks` 与 `/canvas-projects`。
- **远程模式**：不启动内嵌后端，直接连接一个已经在运行的 Kinotv 服务（本机 8080、局域网或托管部署），用桌面 token 或登录 session 调用同一组 API。

两种方式下，用户归属、权限、计费、幂等和内容快照校验都由 Kinotv 后端执行，MCP 只是调用方。

## 构建

```bash
cd backend
go build -o ../.local/bin/kinotv-mcp ./cmd/mcp
```

## 配置 MCP 客户端

Claude Code：

```bash
claude mcp add kinotv -- /absolute/path/to/kinotv-mcp
```

Claude Desktop（`claude_desktop_config.json`）：

```json
{
  "mcpServers": {
    "kinotv": {
      "command": "/absolute/path/to/kinotv-mcp"
    }
  }
}
```

默认使用桌面应用的数据目录（macOS 为 `~/Library/Application Support/BeefTV`，Windows 为 `%AppData%\BeefTV`）。这样 MCP 创建的运行、画布和生成结果与桌面应用共用同一份工作区。

可用环境变量：

| 变量 | 用途 |
| --- | --- |
| `CANVAS_DATA_DIR` | 覆盖内嵌模式的数据目录；实验时建议指向独立临时目录 |
| `CANVAS_DESKTOP_DATA_DIR` | 桌面应用使用的数据目录覆盖，MCP 会按同一优先级读取 |
| `BEEFTV_MCP_AGENT_MODEL` | `agent_start` 未指定模型时的默认文本模型 |
| `KINOTV_MCP_BASE_URL` | 设置后进入远程模式，例如 `http://127.0.0.1:8080` 或 `https://kinotv.example.com` |
| `KINOTV_MCP_ACCESS_TOKEN` | 托管服务的个人访问令牌（推荐）；MCP 以 `Authorization: Bearer` 发送 |
| `KINOTV_MCP_BEARER_TOKEN` | `KINOTV_MCP_ACCESS_TOKEN` 的别名 |
| `KINOTV_MCP_COOKIE` | 远程模式的登录 Cookie，例如 `canana_session=...`；仅用于临时联调 |
| `KINOTV_MCP_SESSION_COOKIE` | `KINOTV_MCP_COOKIE` 的别名 |
| `KINOTV_MCP_TOKEN` | 桌面后端的启动令牌；MCP 以 `X-Desktop-Token` 发送 |
| `KINOTV_MCP_PROFILE` | `local` / `hosted`，覆盖“Bearer/Cookie=托管、桌面 Token=本地”的默认判断 |

## 连接已有服务（远程模式）

设置 `KINOTV_MCP_BASE_URL` 后，MCP 不启动内嵌后端，也不会启动 Agent 调度器；它假设目标 Kinotv 服务已经在运行。

托管实例推荐使用个人访问令牌。在 Kinotv 设置页的“连接 Agent（MCP）”卡片里创建令牌，复制现成的 Claude Code / Claude Desktop 配置即可：

```bash
claude mcp add kinotv \
  -e KINOTV_MCP_BASE_URL=https://kinotv.example.com \
  -e KINOTV_MCP_ACCESS_TOKEN=ktv_mcp_... \
  -- /absolute/path/to/kinotv-mcp
```

Claude Desktop 配置：

```json
{
  "mcpServers": {
    "kinotv": {
      "command": "/absolute/path/to/kinotv-mcp",
      "env": {
        "KINOTV_MCP_BASE_URL": "https://kinotv.example.com",
        "KINOTV_MCP_ACCESS_TOKEN": "ktv_mcp_..."
      }
    }
  }
}
```

令牌只保存哈希，创建时显示一次；可以设置 7/30/90/365 天有效期，并随时在设置页吊销。可用范围限定为 Agent 运行、画布、任务查询和模型目录读取；任务创建、模型配置写入和其他接口一律拒绝。

创建令牌时可以同时设定授权：权限模式、单轮积分上限和每日积分上限。未指定时默认
`auto` + 单轮 1000 积分 + 每日 2000 积分。`auto` 不能脱离额度存在：服务端拒绝
“自主执行但没有积分上限”的令牌，也会把客户端传来的、超出 Grant 的额度夹紧到
Grant 上限。

临时联调也可以使用登录 Cookie：

```bash
KINOTV_MCP_COOKIE='canana_session=...'
```

Cookie 从已登录浏览器的 DevTools → Application → Cookies 获取，会过期并随退出登录失效，只适合开发者临时使用。

远程模式下，所有 Agent 运行、生成任务、积分流水和画布节点都会写进目标 Kinotv 后台，能在 UI 和任务中心看到；内嵌模式则写入本机数据目录。通过访问令牌创建的 Agent 运行会带 `source=mcp` 和对应的 `mcpTokenId`，便于审计。

## 工具

画布：

- `canvas_list` / `canvas_get`
- `canvas_add_node` / `canvas_update_node` / `canvas_delete_node` / `canvas_connect_nodes`
- `canvas_batch_apply` / `canvas_clear_test_nodes`

Agent：

- `agent_models`：列出本地配置与系统目录中可用的模型，凭据已脱敏
- `agent_start`：拉起一轮 Agent 运行，返回 `runId` 和 `conversationId`；可传
  `permissionMode` 与 `maxCredits`，实际额度取 Agent 请求与 Grant 上限的较小值
- `agent_status`：读取状态、步骤、待审批项和最近事件
- `agent_wait`：等待终态或待审批状态，默认 30 秒、最长 120 秒
- `agent_continue`：上一轮结束后继续同一对话
- `agent_decide_approval`：批准或拒绝当前审批
- `agent_request_budget`：申请提高本轮额度。只提交申请，**不能自行批准**；必须由
  用户在 Kinotv 授权中心确认
- `agent_cancel`：取消运行
- `task_get`：查询生成任务状态和结果预览

## 典型流程

1. `canvas_list` 找到目标画布。
2. `agent_models` 选择文本模型（也可以让 `agent_start` 自动选择 `capability=text` 的模型）。
3. `agent_start`：

   ```json
   {
     "canvasId": "canvas-123",
     "prompt": "为这段旁白设计 3 个镜头，并生成首帧和 5 秒视频",
     "permissionMode": "auto",
     "maxCredits": 1000
   }
   ```

4. `agent_wait` 轮询。额度内的画布写入和媒体生成直接执行；只有删除、恢复历史、
   发布等不可逆操作会返回 `waitingApproval=true`。
5. 遇到审批时，把 `approval.preview`、`approval.arguments` 和费用相关信息交给用户
   确认，再由用户决定是否调用 `agent_decide_approval`（`decision=approve`/`reject`）。
6. 继续 `agent_wait`，直到 `terminal=true`。需要下一轮时用 `agent_continue`。

## 权限模式

- `read_only`：只读，不写画布、不生成媒体。
- `request_approval`：画布写入和媒体生成暂停等待审批。
- `auto`：在 Grant 的单轮与每日额度内直接执行；必须同时设置两个额度，否则服务端
  拒绝创建授权，或在缺少单轮额度时把本轮降级为 `request_approval`。

审批不会因为走 MCP 而跳过。`agent_decide_approval` 的调用方必须先取得用户同意；
MCP server 只负责把审批请求和决定传给后端。审批结果绑定用户、Run、操作类型、
目标资源和授权范围，不能被另一个 Run 复用。

只有不可逆或越界操作进入审批白名单：删除项目、恢复历史、公开发布。普通画布编辑、
生成图片/视频/音频在 `auto` 的额度内自主执行，不逐次打断用户。

## 积分与并发约束

- **Run 上限**：`maxCredits` 在启动时写入 Run，之后不能由 Agent 抬高。超出后返回
  `agent_budget_exceeded`，Run 停止推进，Agent 只能提交加额申请。
- **每日上限**：按 UTC+8 自然日统计，跨所有 MCP 令牌、所有 Run、所有客户端共享。
  两个 Run 同时争抢额度时，服务端在事务内做条件更新，只有一个能拿到剩余额度。
- **原子预留**：任务执行前先写预留记录并对额度桶做 `reserved + credits <= limit`
  的条件更新，不存在“先查后写”的并发窗口。同一任务 ID 重复提交只预留一次。
- **结算与释放**：任务成功按实际费用结算并释放差额；失败、取消、退款或超时释放
  未消费的预留。已结算的记录不会被回收。
- **熔断**：同一 Run 连续 3 次工具失败后自动停止，需人工确认后再继续。
- **撤销与版本**：撤销 Grant 或修改授权会递增版本。已开始的 Run 在下一次额度预留
  时因版本过期被拒绝，不能继续使用旧权限；用户批准加额后需要 `agent_continue`
  开始新一轮。

加额只能由用户在 Web 授权中心完成。Agent 调用 `agent_request_budget` 只是提交
申请，服务端不会接受 Agent 自批的额度变更。批准会把单轮额度提高到申请值，并按
相同增量抬高每日池，避免批了单轮却仍被当日总额度卡住。

## 运行方式与限制

- MCP server 会启动 Agent 调度器和任务 worker，因此即使桌面 UI 没有打开，运行也会继续执行。
- 任务领取使用数据库租约，多个进程不会重复执行同一个任务。Agent 轮次使用乐观并发和幂等键，重复调用 `agent_start` 时应复用同一个 `idempotencyKey`。
- 如果桌面应用正在打开同一个画布，Agent 写入会持久化到服务端；界面可能需要重新进入画布才能看到最新内容。
- 建议同一数据目录只运行一个 MCP server 实例。需要隔离实验时，用 `CANVAS_DATA_DIR` 指向独立目录。
- stdio MCP 工具调用是同步的。`agent_wait` 会主动在超时后返回，避免长时间占住客户端。
- **内嵌本地模式**使用桌面/本地 profile，本地工作区不支持系统渠道中转（`/api/ai/system/<channelId>`）：这类模型会在 `agent_models` 中标记为不可用，请配置直连模型渠道。**远程模式**连接托管服务时，系统渠道中转是后端正常执行路径。
- 远程模式优先使用个人访问令牌。Cookie 仅用于临时联调：它会过期、会随退出登录失效，不适合作为长期集成凭据。

## 连接管理与接口

设置页「连接 Agent（MCP）」提供令牌创建、列表、吊销和配置复制。接口：

- `GET /api/integrations/mcp/status`：连接地址、有效令牌数
- `GET /api/integrations/mcp/tokens`：当前账号的令牌列表
- `POST /api/integrations/mcp/tokens`：创建令牌
  （`name`、`expiresInDays`、`permissionMode`、`maxCreditsPerRun`、
  `maxCreditsPerDay`），明文只返回一次
- `DELETE /api/integrations/mcp/tokens/{id}`：吊销令牌
- `GET /api/integrations/mcp/grants`：当前账号的 Agent 授权及额度
- `DELETE /api/integrations/mcp/grants/{id}`：撤销授权，旧 Run 随即失效
- `POST /api/agent/runs/{runId}/budget-request`：Agent 提交加额申请
- `GET /api/agent/budget-requests`：用户查看待处理的加额申请
- `POST /api/agent/budget-requests/{id}/approve`：用户批准加额申请

令牌认证只放行 MCP 需要的接口：Agent、画布、任务和模型目录。通过令牌创建的 Agent 运行会带 `source=mcp` 与 `mcpTokenId`；任务中心和积分流水的来源筛选仍可继续补齐。

## 后续产品化

1. **任务中心按来源筛选**：把 `source=mcp` 显示成标签，并支持按连接令牌查看发起过的运行。
2. **本地桌面一键接入**：桌面端把 `kinotv-mcp` 二进制随应用分发，设置页提供“写入 Claude 配置”按钮，自动生成 command 和 env，必要时提示重启客户端。
3. **托管部署的远程 MCP**：提供 HTTP/SSE MCP endpoint，用户只需连接 URL + PAT，不必安装二进制；这是 SaaS 用户的最终形态。
4. **更细的令牌权限**：在现有 `agent/canvas/tasks/models` 基础上增加只读、审批、自动执行等运行级限制。
