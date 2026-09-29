# Aigen Seedance Video 接口字段

## 协议身份

- 插件 ID：`aigenvideo-seedance`。
- Provider ID：`aigenvideo-seedance-v20`（2.0 档）、`aigenvideo-seedance-v25`（2.5 档）。
- 能力：`video`。
- 默认 Base URL：`https://ai-genvideo.com/v1`。
- 鉴权驱动：`bearer`。
- 创建：`POST /videos/generations`。
- 查询：`GET /tasks/{{taskId}}`。

两档共用同一 Base URL、鉴权与查询端点，唯一差别是上游 `mode` 与时长取值：2.0 档把画布选中的时长原样发到 `durationSeconds`（5 / 10 / 15），2.5 档固定发送 `mode=2.5`、`durationSeconds=30`。上游没有 `model` 字段，模型档位由 `mode` 决定。

## 配置字段

| 字段 | 类型 | 必填 | 含义 |
| --- | --- | --- | --- |
| `apiKey` | secret | 是 | API Key，随 `Authorization: Bearer` 头发送。 |

## 统一字段映射

| 统一字段 | 类型 | 必填 | 上游映射 | 说明 |
| --- | --- | --- | --- | --- |
| `prompt` | string | 是 | `prompt` | 视频描述提示词，可用 `@图N` 引用第 N 张参考图。 |
| `aspectRatio` | string | 是 | `ratio` | 画幅比例。 |
| `images` | media[] | 否 | `images[].url` | 参考图片，最多 10 张。 |
| `duration` | integer | 是 | `durationSeconds` | 2.0 档取 5 / 10 / 15；2.5 档固定 30。 |
| `providerOptions` | object | 否 | `provider-specific fields` | 插件命名空间内的厂商扩展字段。 |

## 上游请求模板逐字段清单

| 上游位置 | 值或转换表达式 |
| --- | --- |
| `create.method` | `"POST"` |
| `create.path` | `"/videos/generations"` |
| `create.contentType` | `"application/json"` |
| `create.body.prompt` | `request.prompt` |
| `create.body.mode` | 2.0 档字面量 `"2.0"`；2.5 档字面量 `"2.5"` |
| `create.body.ratio` | `request.aspectRatio` |
| `create.body.durationSeconds` | 2.0 档 `request.duration`；2.5 档字面量 `30` |
| `create.body.images[].url` | 逐张参考图取 `media.value`，空数组时整体省略 |
| `create.body.<providerOptions>` | `request.providerOptions.<providerId>.body` 或 `.extra_body`，最后整体合并覆盖 |
| `poll.method` | `"GET"` |
| `poll.path` | `"/tasks/{{taskId}}"` |

## 响应映射

| 统一字段 | 取值来源 | 说明 |
| --- | --- | --- |
| `taskId` | `response.id` → `response.taskId` → `response.data.id` | 上游 `id` 是 long，以字符串返回，必须按字符串保存。 |
| `status` | `response.status` → `response.data.status`，缺省 `pending` | 见下方状态映射。 |
| `message` | `response.message` → `response.error.message` | 失败任务的可读原因。 |
| `videos` | `response.outputUrl` → `response.output_url` | 仅 `succeeded` 时存在；CDN 签名地址会过期，因此 `resultEphemeral` 为 true，落库时必须尽快转存。 |

## 状态映射

| 上游 status | 平台语义 | 说明 |
| --- | --- | --- |
| `pending` | `pending` | 任务已受理，排队等待处理。 |
| `processing` | `processing` | 处理中（含生成与后处理两个阶段，上游不对外区分）。 |
| `succeeded` | `succeeded` | 处理完成，`outputUrl` 可用。 |
| `failed` | `failed` | 处理失败，上游全额退还积分。 |
| `timeout` | `failed` | 上游 2 小时未完成即判超时并全额退还积分；平台按终态失败处理，不再继续轮询。 |
| `cancelled` | `cancelled` | 已取消，上游全额退还积分。 |

## 轮询与计费约定

- 上游首次查询建议在创建后 5 分钟发起，之后按 10-30 秒间隔轮询；过早轮询只会命中 `pending / processing` 并消耗限流配额（120 次/分钟）。
- 上游按任务固定扣 5 积分，创建时预扣，失败或取消时全额退还；平台侧积分按渠道模型的规格档位单独定价，不直接沿用上游积分。
- 奖励接口 `GET /balance` 与产物刷新接口 `GET /tasks/output-url/{id}` 未接入协议层：前者属于运营侧对账，后者只在 CDN 地址过期时才有意义，平台在任务成功后立即转存产物，不依赖刷新。

## 校验规则

| Provider | 断言 | 失败提示 |
| --- | --- | --- |
| `aigenvideo-seedance-v20` | `request.duration` 非 0 | Seedance 2.0 需要选择 5 / 10 / 15 秒时长 |
| `aigenvideo-seedance-v25` | `request.aspectRatio` 非空 | Seedance 2.5 需要选择画幅比例 |

## 已知限制

- 上游不接受内嵌 `data:` 参考图之外的本地路径；本插件声明 `requiresPublicMediaUrls: false`，本地工作区的资源会以内嵌数据发送，若上游拒绝内嵌数据需改走对象存储并改为 `true`。
- 2.5 档分辨率固定 720P，上游请求体没有分辨率字段，画布上的分辨率选择不参与上游请求。

<!-- BEEFTV_PLUGIN_MANIFEST_START -->
## Manifest 完整接口定义

以下 JSON 与插件包内实际 `manifest.json` 逐字段一致，覆盖插件身份、权限、配置、鉴权、参数、校验、创建、Agent、查询、取消、结果下载、响应和 Agent 响应映射。`documentation` 字段的值就是当前完整文档；为避免文档在自身内部无限递归，JSON 中仅用等义占位文本表示正文。

```json
{
  "apiVersion": "beeftv.plugin/v2",
  "id": "aigenvideo-seedance",
  "name": "Aigen Seedance Video",
  "version": "2.0.0",
  "author": "KinoTV",
  "description": "ai-genvideo.com Seedance 视频独立请求协议插件（2.0 / 2.5 两档）。",
  "permissions": [
    "generation.run",
    "media.read"
  ],
  "configuration": {
    "fields": [
      {
        "name": "apiKey",
        "type": "secret",
        "label": "API Key",
        "required": true
      }
    ]
  },
  "contributes": {
    "providers": [
      {
        "id": "aigenvideo-seedance-v20",
        "label": "Seedance 2.0",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://ai-genvideo.com/v1",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频描述提示词，可用 @图N 引用指定参考图。"
          },
          {
            "name": "mode",
            "type": "string",
            "required": false,
            "mapping": "mode",
            "description": "视频模式，本插件固定为 2.0；不传时上游按 2.0 处理。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": true,
            "mapping": "ratio",
            "description": "画幅比例，可选 21:9 / 16:9 / 4:3 / 1:1 / 3:4 / 9:16。"
          },
          {
            "name": "duration",
            "type": "integer",
            "required": true,
            "mapping": "durationSeconds",
            "description": "视频时长秒数，2.0 档取 5 / 10 / 15。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "images[].url",
            "description": "参考图片，最多 10 张。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段。"
          }
        ],
        "validations": [
          {
            "assert": {
              "$ref": "request.duration"
            },
            "message": "Seedance 2.0 需要选择 5 / 10 / 15 秒时长"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/videos/generations",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "prompt": {
                  "$ref": "request.prompt"
                },
                "mode": "2.0",
                "ratio": {
                  "$ref": "request.aspectRatio"
                },
                "durationSeconds": {
                  "$ref": "request.duration"
                },
                "images": {
                  "$omitEmpty": {
                    "$map": {
                      "from": {
                        "$ref": "request.images"
                      },
                      "as": "media",
                      "in": {
                        "url": {
                          "$ref": "media.value"
                        }
                      }
                    }
                  }
                }
              },
              {
                "$coalesce": [
                  {
                    "$ref": "request.providerOptions.aigenvideo-seedance-v20.body"
                  },
                  {
                    "$ref": "request.providerOptions.aigenvideo-seedance-v20.extra_body"
                  },
                  {}
                ]
              }
            ]
          }
        },
        "poll": {
          "method": "GET",
          "path": "/tasks/{{taskId}}"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$coalesce": [
              {
                "$ref": "response.status"
              },
              {
                "$ref": "response.data.status"
              },
              "pending"
            ]
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.message"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.outputUrl"
              },
              {
                "$ref": "response.output_url"
              },
              {
                "$ref": "response.data.outputUrl"
              }
            ]
          },
          "resultEphemeral": true
        }
      },
      {
        "id": "aigenvideo-seedance-v25",
        "label": "Seedance 2.5",
        "capabilities": [
          "video"
        ],
        "scopes": [
          "admin.system-channel",
          "user.custom-channel",
          "canvas",
          "creation",
          "agent"
        ],
        "baseUrl": "https://ai-genvideo.com/v1",
        "requiresPublicMediaUrls": false,
        "auth": {
          "type": "bearer",
          "field": "apiKey"
        },
        "parameters": [
          {
            "name": "prompt",
            "type": "string",
            "required": true,
            "mapping": "prompt",
            "description": "视频描述提示词，可用 @图N 引用指定参考图。"
          },
          {
            "name": "mode",
            "type": "string",
            "required": false,
            "mapping": "mode",
            "description": "视频模式，本插件固定为 2.5；不传时上游按 2.0 处理，因此本档必须显式传 2.5。"
          },
          {
            "name": "aspectRatio",
            "type": "string",
            "required": true,
            "mapping": "ratio",
            "description": "画幅比例，可选 21:9 / 16:9 / 4:3 / 1:1 / 3:4 / 9:16。"
          },
          {
            "name": "duration",
            "type": "integer",
            "required": true,
            "mapping": "durationSeconds",
            "description": "视频时长秒数，2.5 档固定 30；本插件始终向上游发送 30。"
          },
          {
            "name": "images",
            "type": "media[]",
            "required": false,
            "mapping": "images[].url",
            "description": "参考图片，最多 10 张。"
          },
          {
            "name": "providerOptions",
            "type": "object",
            "required": false,
            "mapping": "provider-specific fields",
            "description": "插件命名空间内的厂商扩展字段。"
          }
        ],
        "validations": [
          {
            "assert": {
              "$ref": "request.aspectRatio"
            },
            "message": "Seedance 2.5 需要选择画幅比例"
          }
        ],
        "create": {
          "method": "POST",
          "path": "/videos/generations",
          "contentType": "application/json",
          "body": {
            "$merge": [
              {
                "prompt": {
                  "$ref": "request.prompt"
                },
                "mode": "2.5",
                "ratio": {
                  "$ref": "request.aspectRatio"
                },
                "durationSeconds": 30,
                "images": {
                  "$omitEmpty": {
                    "$map": {
                      "from": {
                        "$ref": "request.images"
                      },
                      "as": "media",
                      "in": {
                        "url": {
                          "$ref": "media.value"
                        }
                      }
                    }
                  }
                }
              },
              {
                "$coalesce": [
                  {
                    "$ref": "request.providerOptions.aigenvideo-seedance-v25.body"
                  },
                  {
                    "$ref": "request.providerOptions.aigenvideo-seedance-v25.extra_body"
                  },
                  {}
                ]
              }
            ]
          }
        },
        "poll": {
          "method": "GET",
          "path": "/tasks/{{taskId}}"
        },
        "response": {
          "taskId": {
            "$coalesce": [
              {
                "$ref": "response.id"
              },
              {
                "$ref": "response.taskId"
              },
              {
                "$ref": "response.data.id"
              },
              {
                "$ref": "taskId"
              }
            ]
          },
          "status": {
            "$coalesce": [
              {
                "$ref": "response.status"
              },
              {
                "$ref": "response.data.status"
              },
              "pending"
            ]
          },
          "message": {
            "$coalesce": [
              {
                "$ref": "response.message"
              },
              {
                "$ref": "response.error.message"
              },
              {
                "$ref": "response.data.message"
              }
            ]
          },
          "videos": {
            "$coalesce": [
              {
                "$ref": "response.outputUrl"
              },
              {
                "$ref": "response.output_url"
              },
              {
                "$ref": "response.data.outputUrl"
              }
            ]
          },
          "resultEphemeral": true
        }
      }
    ]
  },
  "documentation": "<当前插件的完整 documentation，由 README.md 与 docs/interface.md 拼接而成；为避免 JSON 递归，此处不重复展开正文。>"
}
```
<!-- BEEFTV_PLUGIN_MANIFEST_END -->
