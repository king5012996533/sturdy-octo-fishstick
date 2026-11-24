package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	agentToolStart         = "agent_start"
	agentToolStatus        = "agent_status"
	agentToolWait          = "agent_wait"
	agentToolContinue      = "agent_continue"
	agentToolDecide        = "agent_decide_approval"
	agentToolCancel        = "agent_cancel"
	agentToolModels        = "agent_models"
	agentToolTaskGet       = "task_get"
	agentToolRequestBudget = "agent_request_budget"
)

const agentModelEnv = "BEEFTV_MCP_AGENT_MODEL"

// mcpAPI talks to the local Kinotv HTTP runtime with the launch token. The
// runtime validates ownership and permissions; this adapter only shapes MCP
// arguments into the existing Agent and task APIs.
type mcpAPI struct {
	base   string
	token  string
	client *http.Client
}

func newMCPAPI(base, token string, client *http.Client) *mcpAPI {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &mcpAPI{base: strings.TrimRight(base, "/"), token: token, client: client}
}

type apiEnvelope struct {
	Code   int             `json:"code"`
	Data   json.RawMessage `json:"data"`
	Msg    string          `json:"msg"`
	Reason string          `json:"reason"`
}

func (a *mcpAPI) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Desktop-Token", a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return unwrapEnvelope(res.StatusCode, raw)
}

func unwrapEnvelope(status int, raw []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		if status >= 300 {
			return nil, fmt.Errorf("HTTP %d", status)
		}
		return json.RawMessage("null"), nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		if status >= 300 {
			return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(trimmed)))
		}
		return trimmed, nil
	}
	if _, ok := probe["code"]; !ok {
		if status >= 300 {
			return nil, fmt.Errorf("HTTP %d: %s", status, strings.TrimSpace(string(trimmed)))
		}
		return trimmed, nil
	}
	var env apiEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return nil, fmt.Errorf("响应格式无效: %w", err)
	}
	if env.Code != 0 {
		return nil, errors.New(envelopeMessage(env))
	}
	if status >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", status, envelopeMessage(env))
	}
	if len(env.Data) == 0 {
		return json.RawMessage("null"), nil
	}
	return env.Data, nil
}

func envelopeMessage(env apiEnvelope) string {
	reason := strings.TrimSpace(env.Reason)
	message := strings.TrimSpace(env.Msg)
	switch {
	case message != "" && reason != "" && message != reason:
		return fmt.Sprintf("%s (%s)", message, reason)
	case message != "":
		return message
	case reason != "":
		return reason
	default:
		return fmt.Sprintf("业务请求失败 (code=%d)", env.Code)
	}
}

// ---------------------------------------------------------------------------
// Tool definitions

func agentToolDefinitions() []any {
	return []any{
		map[string]any{
			"name":        agentToolStart,
			"description": "拉起一个 Kinotv 创作 Agent 运行（服务端持久执行）。默认 request_approval：画布写入和媒体生成会暂停等待 agent_decide_approval。返回 runId 和 conversationId；用 agent_wait 等待进度。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":          map[string]any{"type": "string", "description": "交给 Agent 的任务，1–16000 字符"},
					"canvasId":        map[string]any{"type": "string", "description": "目标画布 ID；不传则执行纯聊天任务。先用 canvas_list 获取"},
					"conversationId":  map[string]any{"type": "string", "description": "对话 ID；续聊时必须与上一轮一致。省略时画布运行默认使用 canvasId，纯聊天自动生成"},
					"model":           map[string]any{"type": "string", "description": "文本模型名（本地模型配置中的 model）。优先用 agent_models 查到的值"},
					"logicalModelId":  map[string]any{"type": "string", "description": "系统逻辑模型 ID；不能与 channelId/channelModelKey 同时使用"},
					"channelId":       map[string]any{"type": "string", "description": "系统渠道 ID；必须与 channelModelKey 一起使用"},
					"channelModelKey": map[string]any{"type": "string", "description": "系统渠道模型标识；必须与 channelId 一起使用"},
					"permissionMode":  map[string]any{"type": "string", "enum": []string{"read_only", "request_approval", "auto"}, "default": "request_approval", "description": "权限模式。auto 会直接执行画布写入和生成；仅在用户明确授权时使用"},
					"contextScope":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"canvas"}}, "description": "上下文范围；传 canvas 时必须提供 canvasId。省略时画布运行默认开启画布上下文"},
					"skillIds":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选技能 ID"},
					"reasoningMode":   map[string]any{"type": "string", "enum": []string{"off", "auto", "deep"}},
					"budget":          agentBudgetSchema(),
					"idempotencyKey":  map[string]any{"type": "string", "description": "幂等键；省略时按 conversationId + prompt + 模型生成稳定键，重试同一请求不会重复扣费"},
				},
				"required": []string{"prompt"},
			},
		},
		map[string]any{
			"name":        agentToolStatus,
			"description": "读取 Agent 运行状态、步骤、待审批详情和最近事件。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runId":      map[string]any{"type": "string"},
					"eventLimit": map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "default": 10, "description": "返回最近多少条事件；0 表示不返回事件"},
				},
				"required": []string{"runId"},
			},
		},
		map[string]any{
			"name":        agentToolWait,
			"description": "等待 Agent 运行进入终态或等待审批，最多 timeoutSeconds 秒（默认 30，最大 120）。超时返回 timedOut=true，可再次调用。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runId":          map[string]any{"type": "string"},
					"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 120, "default": 30},
					"pollIntervalMs": map[string]any{"type": "integer", "minimum": 250, "maximum": 5000, "default": 1000},
					"eventLimit":     map[string]any{"type": "integer", "minimum": 0, "maximum": 50, "default": 10},
				},
				"required": []string{"runId"},
			},
		},
		map[string]any{
			"name":        agentToolContinue,
			"description": "在上一轮结束后继续同一对话（创建新的不可变轮次）。必须等上一轮 completed/failed/cancelled/rejected。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runId":           map[string]any{"type": "string", "description": "上一轮运行 ID"},
					"prompt":          map[string]any{"type": "string"},
					"conversationId":  map[string]any{"type": "string", "description": "省略时使用运行时返回的 conversationId"},
					"canvasId":        map[string]any{"type": "string", "description": "省略时沿用上一轮画布；纯聊天保持为空"},
					"model":           map[string]any{"type": "string"},
					"logicalModelId":  map[string]any{"type": "string"},
					"channelId":       map[string]any{"type": "string"},
					"channelModelKey": map[string]any{"type": "string"},
					"permissionMode":  map[string]any{"type": "string", "enum": []string{"read_only", "request_approval", "auto"}, "description": "省略时沿用上一轮"},
					"contextScope":    map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"canvas"}}},
					"skillIds":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"reasoningMode":   map[string]any{"type": "string", "enum": []string{"off", "auto", "deep"}},
					"budget":          agentBudgetSchema(),
					"idempotencyKey":  map[string]any{"type": "string"},
				},
				"required": []string{"runId", "prompt"},
			},
		},
		map[string]any{
			"name":        agentToolDecide,
			"description": "批准或拒绝当前待审批操作。高风险/不可逆操作必须先在对话中取得用户明确同意再调用。该工具不能用于提高积分额度：加额只能由用户在 KinoTV 界面批准。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runId":      map[string]any{"type": "string"},
					"approvalId": map[string]any{"type": "string", "description": "省略时使用当前运行唯一待审批项"},
					"decision":   map[string]any{"type": "string", "enum": []string{"approve", "reject"}},
					"reason":     map[string]any{"type": "string"},
					"mediaSettings": map[string]any{
						"type":        "object",
						"description": "仅图片生成审批可传，用于调整生成参数",
						"properties": map[string]any{
							"logicalModelId":  map[string]any{"type": "string"},
							"channelId":       map[string]any{"type": "string"},
							"channelModelKey": map[string]any{"type": "string"},
							"size":            map[string]any{"type": "string"},
							"quality":         map[string]any{"type": "string"},
						},
					},
				},
				"required": []string{"runId", "decision"},
			},
		},
		map[string]any{
			"name":        agentToolCancel,
			"description": "取消一个仍在执行的 Agent 运行。",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"runId": map[string]any{"type": "string"}},
				"required":   []string{"runId"},
			},
		},
		map[string]any{
			"name":        agentToolModels,
			"description": "列出本地模型配置与系统模型目录中的可用模型（凭据已脱敏）。返回项带 capability；Agent 运行需要 capability=text 的模型。把返回的 model 或 logicalModelId 传给 agent_start。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"capability": map[string]any{"type": "string", "description": "可选：按能力过滤，例如 text"},
				},
			},
		},
		map[string]any{
			"name":        agentToolRequestBudget,
			"description": "申请提高本轮授权的积分额度。该工具只提交申请，不能批准；必须由用户在 KinoTV 授权中心确认。批准后 Grant 版本会变化，请用 agent_continue 开始新一轮。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runId":            map[string]any{"type": "string"},
					"requestedCredits": map[string]any{"type": "integer", "minimum": 1},
					"reason":           map[string]any{"type": "string", "maxLength": 500},
				},
				"required": []string{"runId", "requestedCredits"},
			},
		},
		map[string]any{
			"name":        agentToolTaskGet,
			"description": "读取一次生成任务的状态、进度和结果预览。",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"taskId": map[string]any{"type": "string"}},
				"required":   []string{"taskId"},
			},
		},
	}
}

func agentBudgetSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"maxGenerationTasks": map[string]any{"type": "integer", "minimum": 0},
			"maxVideoSeconds":    map[string]any{"type": "integer", "minimum": 0},
			"maxCredits":         map[string]any{"type": "integer", "minimum": 0, "description": "本轮授权积分上限。auto 模式必须设置；省略时使用令牌 Grant 的默认值"},
			"maxSteps":           map[string]any{"type": "integer", "minimum": 0, "description": "模型调用步数上限；省略或 0 使用服务端默认值 200"},
		},
	}
}

// ---------------------------------------------------------------------------
// Dispatch

func callAgentTool(name string, raw json.RawMessage, base, token string, client *http.Client) (any, bool) {
	api := newMCPAPI(base, token, client)
	switch name {
	case agentToolStart:
		value, err := api.agentStart(raw)
		return toolResult(value, err), true
	case agentToolStatus:
		value, err := api.agentStatus(raw)
		return toolResult(value, err), true
	case agentToolWait:
		value, err := api.agentWait(raw)
		return toolResult(value, err), true
	case agentToolContinue:
		value, err := api.agentContinue(raw)
		return toolResult(value, err), true
	case agentToolDecide:
		value, err := api.agentDecide(raw)
		return toolResult(value, err), true
	case agentToolCancel:
		value, err := api.agentCancel(raw)
		return toolResult(value, err), true
	case agentToolModels:
		value, err := api.agentModels(raw)
		return toolResult(value, err), true
	case agentToolTaskGet:
		value, err := api.taskGet(raw)
		return toolResult(value, err), true
	case agentToolRequestBudget:
		value, err := api.agentRequestBudget(raw)
		return toolResult(value, err), true
	default:
		return nil, false
	}
}

func toolResult(value any, err error) any {
	if err != nil {
		return toolError(err)
	}
	return jsonToolResult(value)
}

func jsonToolResult(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(raw)}}}
}

// ---------------------------------------------------------------------------
// Agent run tools

type agentRunArgs struct {
	RunID           string         `json:"runId"`
	Prompt          string         `json:"prompt"`
	ConversationID  string         `json:"conversationId"`
	CanvasID        string         `json:"canvasId"`
	Model           string         `json:"model"`
	LogicalModelID  string         `json:"logicalModelId"`
	ChannelID       string         `json:"channelId"`
	ChannelModelKey string         `json:"channelModelKey"`
	PermissionMode  string         `json:"permissionMode"`
	ContextScope    []string       `json:"contextScope"`
	SkillIDs        []string       `json:"skillIds"`
	ReasoningMode   string         `json:"reasoningMode"`
	IdempotencyKey  string         `json:"idempotencyKey"`
	Budget          agentRunBudget `json:"budget"`
}

type agentRunBudget struct {
	MaxGenerationTasks int   `json:"maxGenerationTasks,omitempty"`
	MaxVideoSeconds    int   `json:"maxVideoSeconds,omitempty"`
	MaxCredits         int64 `json:"maxCredits,omitempty"`
	MaxSteps           int   `json:"maxSteps,omitempty"`
}

// mcpAgentIdempotencyKey 为省略幂等键的调用生成稳定值。
//
// 随机键在客户端重试时会变成第二次提交：同一句话扣两次钱。把父 Run 一并纳入，
// 既能让"同一轮重试"命中同一条记录，也不会把后续新的一轮误判成重复。
func mcpAgentIdempotencyKey(parentID string, args agentRunArgs) string {
	modelKey := args.Model
	if modelKey == "" {
		modelKey = args.ChannelModelKey
	}
	value := strings.Join([]string{parentID, args.ConversationID, args.CanvasID, strings.TrimSpace(args.Prompt), modelKey}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return "mcp-" + hex.EncodeToString(sum[:16])
}

type agentModelChoice struct {
	Model           string `json:"model,omitempty"`
	LogicalModelID  string `json:"logicalModelId,omitempty"`
	ChannelID       string `json:"channelId,omitempty"`
	ChannelModelKey string `json:"channelModelKey,omitempty"`
}

type agentRunWire struct {
	ID             string             `json:"id"`
	ConversationID string             `json:"conversationId"`
	MCPTokenID     string             `json:"mcpTokenId"`
	Source         string             `json:"source"`
	CanvasID       string             `json:"canvasId"`
	ParentID       string             `json:"parentId"`
	Status         string             `json:"status"`
	Revision       int64              `json:"revision"`
	CleanupPending bool               `json:"cleanupPending"`
	FailureMessage string             `json:"failureMessage"`
	PermissionMode string             `json:"permissionMode"`
	Model          string             `json:"model"`
	CreatedAt      string             `json:"createdAt"`
	UpdatedAt      string             `json:"updatedAt"`
	Events         []agentEventWire   `json:"events"`
	Approval       *agentApprovalWire `json:"approval"`
	Step           int                `json:"step"`
	ActiveMessage  map[string]string  `json:"activeMessage"`
}

type agentEventWire struct {
	EventID   string          `json:"eventId"`
	RunID     string          `json:"runId"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"createdAt"`
}

type agentApprovalWire struct {
	ModelName string `json:"modelName"`
	ID        string `json:"approvalId"`
	Call      struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"call"`
	CallHash string `json:"callHash"`
	Preview  struct {
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Items       []any  `json:"items"`
	} `json:"preview"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (a *mcpAPI) agentRequestBudget(raw json.RawMessage) (any, error) {
	var args struct {
		RunID            string `json:"runId"`
		RequestedCredits int64  `json:"requestedCredits"`
		Reason           string `json:"reason"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" || args.RequestedCredits <= 0 {
		return nil, errors.New("runId 与 requestedCredits 不能为空")
	}
	data, err := a.do(context.Background(), http.MethodPost, "/agent/runs/"+url.PathEscape(args.RunID)+"/budget-request", map[string]any{
		"requestedCredits": args.RequestedCredits,
		"reason":           args.Reason,
	})
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("Agent 响应格式无效: %w", err)
	}
	result["nextStep"] = "等待用户在 KinoTV 授权中心批准；Agent 不能自行批准。批准后 Grant 版本已变化，请用 agent_continue 开始新一轮。"
	return result, nil
}

func (a *mcpAPI) agentStart(raw json.RawMessage) (any, error) {
	var args agentRunArgs
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	return a.submitRun("", args)
}

func (a *mcpAPI) agentContinue(raw json.RawMessage) (any, error) {
	var args agentRunArgs
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, errors.New("runId 不能为空")
	}
	parent, err := a.getRun(args.RunID)
	if err != nil {
		return nil, err
	}
	if args.ConversationID == "" {
		switch {
		case parent.ConversationID != "":
			args.ConversationID = parent.ConversationID
		case strings.HasPrefix(parent.CanvasID, "agent-conversation:"):
			args.ConversationID = strings.TrimPrefix(parent.CanvasID, "agent-conversation:")
		}
	}
	if args.ConversationID == "" {
		return nil, errors.New("conversationId 不能为空；请使用 agent_start 返回的 conversationId")
	}
	if args.CanvasID == "" && parent.CanvasID != "" && !strings.HasPrefix(parent.CanvasID, "agent-conversation:") {
		args.CanvasID = parent.CanvasID
	}
	if args.PermissionMode == "" {
		args.PermissionMode = parent.PermissionMode
	}
	if !args.hasModelSelection() && parent.Model != "" {
		args.Model = parent.Model
	}
	return a.submitRun(args.RunID, args)
}

func (a *mcpAPI) submitRun(parentID string, args agentRunArgs) (any, error) {
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return nil, errors.New("prompt 不能为空")
	}
	if args.ConversationID == "" {
		if args.CanvasID != "" {
			args.ConversationID = args.CanvasID
		} else {
			args.ConversationID = "mcp-" + uuid.NewString()
		}
	}
	switch args.PermissionMode {
	case "", "read_only", "request_approval", "auto":
	default:
		return nil, fmt.Errorf("permissionMode 无效: %s", args.PermissionMode)
	}
	if args.ContextScope == nil {
		if args.CanvasID != "" {
			args.ContextScope = []string{"canvas"}
		} else {
			args.ContextScope = []string{}
		}
	}
	if args.IdempotencyKey == "" {
		args.IdempotencyKey = mcpAgentIdempotencyKey(parentID, args)
	}
	autoModel := false
	if args.hasModelSelection() {
		// A model name alone is ambiguous when the local config proxies a
		// system channel. Reuse that channel selection so the run reaches the
		// same server-managed route the desktop model picker would use.
		if err := a.enrichLocalModelSelection(&args); err != nil {
			return nil, err
		}
	} else {
		choice, err := a.resolveAgentModel()
		if err != nil {
			return nil, err
		}
		args.Model, args.LogicalModelID, args.ChannelID, args.ChannelModelKey = choice.Model, choice.LogicalModelID, choice.ChannelID, choice.ChannelModelKey
		autoModel = true
	}
	payload := map[string]any{
		"conversationId": args.ConversationID,
		"prompt":         args.Prompt,
		"contextScope":   args.ContextScope,
		"idempotencyKey": args.IdempotencyKey,
	}
	if args.PermissionMode != "" {
		payload["permissionMode"] = args.PermissionMode
	}
	if args.CanvasID != "" {
		payload["canvasId"] = args.CanvasID
	}
	if args.Model != "" {
		payload["model"] = args.Model
	}
	if args.LogicalModelID != "" {
		payload["logicalModelId"] = args.LogicalModelID
	}
	if args.ChannelID != "" {
		payload["channelId"] = args.ChannelID
	}
	if args.ChannelModelKey != "" {
		payload["channelModelKey"] = args.ChannelModelKey
	}
	if len(args.SkillIDs) > 0 {
		payload["skillIds"] = args.SkillIDs
	}
	if args.ReasoningMode != "" {
		payload["reasoningMode"] = args.ReasoningMode
	}
	if args.Budget != (agentRunBudget{}) {
		payload["budget"] = args.Budget
	}
	path := "/agent/runs"
	if parentID != "" {
		path = "/agent/runs/" + url.PathEscape(parentID) + "/messages"
	}
	data, err := a.do(context.Background(), http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		Run agentRunWire `json:"run"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("Agent 响应格式无效: %w", err)
	}
	out := map[string]any{
		"run":                        compactAgentRun(&result.Run, 10),
		"conversationId":             args.ConversationID,
		"nextTool":                   nextToolForStatus(result.Run.Status),
		"modelSelectedAutomatically": autoModel,
	}
	if autoModel {
		out["modelChoice"] = agentModelChoice{Model: args.Model, LogicalModelID: args.LogicalModelID, ChannelID: args.ChannelID, ChannelModelKey: args.ChannelModelKey}
	}
	return out, nil
}

func (a *mcpAPI) agentStatus(raw json.RawMessage) (any, error) {
	var args struct {
		RunID      string `json:"runId"`
		EventLimit *int   `json:"eventLimit"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, errors.New("runId 不能为空")
	}
	run, err := a.getRun(args.RunID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"run":             compactAgentRun(run, eventLimit(args.EventLimit)),
		"terminal":        isTerminalStatus(run.Status),
		"waitingApproval": run.Approval != nil || run.Status == "waiting_approval",
		"nextTool":        nextToolForStatus(run.Status),
	}, nil
}

func (a *mcpAPI) agentWait(raw json.RawMessage) (any, error) {
	var args struct {
		RunID          string `json:"runId"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
		PollIntervalMS int    `json:"pollIntervalMs"`
		EventLimit     *int   `json:"eventLimit"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, errors.New("runId 不能为空")
	}
	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}
	poll := args.PollIntervalMS
	if poll <= 0 {
		poll = 1000
	}
	if poll < 250 {
		poll = 250
	}
	if poll > 5000 {
		poll = 5000
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for {
		run, err := a.getRun(args.RunID)
		if err != nil {
			return nil, err
		}
		waiting := run.Approval != nil || run.Status == "waiting_approval"
		if isTerminalStatus(run.Status) || waiting {
			out := map[string]any{
				"run":             compactAgentRun(run, eventLimit(args.EventLimit)),
				"timedOut":        false,
				"terminal":        isTerminalStatus(run.Status),
				"waitingApproval": waiting,
				"nextTool":        nextToolForStatus(run.Status),
			}
			return out, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return map[string]any{
				"run":             compactAgentRun(run, eventLimit(args.EventLimit)),
				"timedOut":        true,
				"terminal":        false,
				"waitingApproval": false,
				"nextTool":        agentToolWait,
			}, nil
		}
		sleep := time.Duration(poll) * time.Millisecond
		if sleep > remaining {
			sleep = remaining
		}
		timer := time.NewTimer(sleep)
		<-timer.C
	}
}

func (a *mcpAPI) agentDecide(raw json.RawMessage) (any, error) {
	var args struct {
		RunID         string          `json:"runId"`
		ApprovalID    string          `json:"approvalId"`
		Decision      string          `json:"decision"`
		Reason        string          `json:"reason"`
		MediaSettings json.RawMessage `json:"mediaSettings"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, errors.New("runId 不能为空")
	}
	if args.Decision != "approve" && args.Decision != "reject" {
		return nil, errors.New("decision 必须是 approve 或 reject")
	}
	approvalID := strings.TrimSpace(args.ApprovalID)
	if approvalID == "" {
		run, err := a.getRun(args.RunID)
		if err != nil {
			return nil, err
		}
		if run.Approval == nil {
			return nil, errors.New("当前运行没有待审批项")
		}
		approvalID = run.Approval.ID
	}
	if approvalID == "" {
		return nil, errors.New("approvalId 不能为空")
	}
	payload := map[string]any{"decision": args.Decision}
	if strings.TrimSpace(args.Reason) != "" {
		payload["reason"] = args.Reason
	}
	if len(bytes.TrimSpace(args.MediaSettings)) > 0 && string(bytes.TrimSpace(args.MediaSettings)) != "null" {
		payload["mediaSettings"] = json.RawMessage(args.MediaSettings)
	}
	path := "/agent/runs/" + url.PathEscape(args.RunID) + "/approvals/" + url.PathEscape(approvalID) + "/decision"
	if _, err := a.do(context.Background(), http.MethodPost, path, payload); err != nil {
		return nil, err
	}
	return map[string]any{
		"accepted":   true,
		"runId":      args.RunID,
		"approvalId": approvalID,
		"decision":   args.Decision,
		"nextTool":   agentToolWait,
	}, nil
}

func (a *mcpAPI) agentCancel(raw json.RawMessage) (any, error) {
	var args struct {
		RunID string `json:"runId"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.RunID) == "" {
		return nil, errors.New("runId 不能为空")
	}
	if _, err := a.do(context.Background(), http.MethodPost, "/agent/runs/"+url.PathEscape(args.RunID)+"/cancel", nil); err != nil {
		return nil, err
	}
	return map[string]any{"accepted": true, "runId": args.RunID, "nextTool": agentToolStatus}, nil
}

func (a *mcpAPI) taskGet(raw json.RawMessage) (any, error) {
	var args struct {
		TaskID string `json:"taskId"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.TaskID) == "" {
		return nil, errors.New("taskId 不能为空")
	}
	data, err := a.do(context.Background(), http.MethodGet, "/tasks/"+url.PathEscape(args.TaskID), nil)
	if err != nil {
		return nil, err
	}
	var task any
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("任务响应格式无效: %w", err)
	}
	return task, nil
}

func (a *mcpAPI) getRun(runID string) (*agentRunWire, error) {
	data, err := a.do(context.Background(), http.MethodGet, "/agent/runs/"+url.PathEscape(runID), nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Run agentRunWire `json:"run"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("Agent 响应格式无效: %w", err)
	}
	if result.Run.ID == "" {
		return nil, errors.New("Agent 运行不存在")
	}
	return &result.Run, nil
}

func parseAgentArgs(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("参数格式无效: %w", err)
	}
	return nil
}

func (args agentRunArgs) hasModelSelection() bool {
	return strings.TrimSpace(args.Model) != "" || strings.TrimSpace(args.LogicalModelID) != "" ||
		(strings.TrimSpace(args.ChannelID) != "" && strings.TrimSpace(args.ChannelModelKey) != "")
}

func eventLimit(value *int) int {
	if value == nil {
		return 10
	}
	limit := *value
	if limit < 0 {
		return 0
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "rejected":
		return true
	default:
		return false
	}
}

func nextToolForStatus(status string) string {
	switch {
	case status == "waiting_approval":
		return agentToolDecide
	case isTerminalStatus(status):
		return agentToolContinue
	default:
		return agentToolWait
	}
}

func compactAgentRun(run *agentRunWire, limit int) map[string]any {
	if run == nil {
		return nil
	}
	if limit < 0 {
		limit = 0
	}
	events := run.Events
	omitted := 0
	if limit == 0 {
		omitted = len(events)
		events = nil
	} else if len(events) > limit {
		omitted = len(events) - limit
		events = events[len(events)-limit:]
	}
	compactedEvents := make([]any, 0, len(events))
	for _, event := range events {
		compactedEvents = append(compactedEvents, map[string]any{
			"seq":       event.Seq,
			"type":      event.Type,
			"payload":   event.Payload,
			"createdAt": event.CreatedAt,
		})
	}
	out := map[string]any{
		"id":              run.ID,
		"conversationId":  run.ConversationID,
		"canvasId":        run.CanvasID,
		"parentId":        run.ParentID,
		"status":          run.Status,
		"revision":        run.Revision,
		"permissionMode":  run.PermissionMode,
		"model":           run.Model,
		"source":          run.Source,
		"mcpTokenId":      run.MCPTokenID,
		"createdAt":       run.CreatedAt,
		"updatedAt":       run.UpdatedAt,
		"step":            run.Step,
		"terminal":        isTerminalStatus(run.Status),
		"waitingApproval": run.Approval != nil || run.Status == "waiting_approval",
	}
	if run.CleanupPending {
		out["cleanupPending"] = true
	}
	if run.FailureMessage != "" {
		out["failureMessage"] = run.FailureMessage
	}
	if run.ActiveMessage != nil {
		out["activeMessage"] = run.ActiveMessage
	}
	if run.Approval != nil {
		out["approval"] = compactAgentApproval(run.Approval)
	}
	if len(compactedEvents) > 0 {
		out["events"] = compactedEvents
	}
	if omitted > 0 {
		out["eventsOmitted"] = omitted
	}
	return out
}

func compactAgentApproval(approval *agentApprovalWire) map[string]any {
	if approval == nil {
		return nil
	}
	var arguments any
	raw := strings.TrimSpace(approval.Call.Function.Arguments)
	if raw != "" {
		if json.Unmarshal([]byte(raw), &arguments) != nil {
			arguments = raw
		}
	}
	return map[string]any{
		"approvalId": approval.ID,
		"modelName":  approval.ModelName,
		"tool":       approval.Call.Function.Name,
		"callId":     approval.Call.ID,
		"arguments":  arguments,
		"callHash":   approval.CallHash,
		"preview": map[string]any{
			"kind":        approval.Preview.Kind,
			"title":       approval.Preview.Title,
			"description": approval.Preview.Description,
			"items":       approval.Preview.Items,
		},
		"instruction": "把 preview 和 arguments 给用户确认；用户同意后调用 agent_decide_approval(decision=approve)，否则 reject。",
	}
}

// ---------------------------------------------------------------------------
// Model discovery

type agentModelEntry struct {
	Model           string `json:"model,omitempty"`
	LogicalModelID  string `json:"logicalModelId,omitempty"`
	ChannelID       string `json:"channelId,omitempty"`
	ChannelModelKey string `json:"channelModelKey,omitempty"`
	ChannelName     string `json:"channelName,omitempty"`
	Capability      string `json:"capability,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	Source          string `json:"source"`
	Available       bool   `json:"available"`
	Reason          string `json:"reason,omitempty"`
}

func (a *mcpAPI) agentModels(raw json.RawMessage) (any, error) {
	var args struct {
		Capability string `json:"capability"`
	}
	if err := parseAgentArgs(raw, &args); err != nil {
		return nil, err
	}
	entries, err := a.collectAgentModels()
	if err != nil {
		return nil, err
	}
	filtered := make([]agentModelEntry, 0, len(entries))
	textModels := make([]string, 0)
	textSeen := map[string]bool{}
	for _, entry := range entries {
		if strings.TrimSpace(args.Capability) != "" && !strings.EqualFold(entry.Capability, args.Capability) {
			continue
		}
		filtered = append(filtered, entry)
		if strings.EqualFold(entry.Capability, "text") && entry.Model != "" && !textSeen[entry.Model] {
			textSeen[entry.Model] = true
			textModels = append(textModels, entry.Model)
		}
	}
	return map[string]any{
		"models":     filtered,
		"textModels": textModels,
		"note":       "agent_start 会优先自动选择 capability=text 的本地模型；也可以显式传返回的 model 或 logicalModelId。",
	}, nil
}

// enrichLocalModelSelection fills the system channel identifiers when the
// caller names a local model that proxies a system channel. It returns the
// explicit reason when that model cannot run in a local workspace instead of
// letting the backend fail later with an opaque selection error.
func (a *mcpAPI) enrichLocalModelSelection(args *agentRunArgs) error {
	if args == nil || args.Model == "" || args.LogicalModelID != "" || args.ChannelID != "" || args.ChannelModelKey != "" {
		return nil
	}
	entries, err := a.localAgentModels()
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.Model != args.Model {
			continue
		}
		if !entry.Available {
			if entry.Reason != "" {
				return errors.New(entry.Reason)
			}
			return nil
		}
		if entry.ChannelID != "" && entry.ChannelModelKey != "" {
			args.ChannelID = entry.ChannelID
			args.ChannelModelKey = entry.ChannelModelKey
			return nil
		}
	}
	return nil
}

func (a *mcpAPI) resolveAgentModel() (agentModelChoice, error) {
	if value := strings.TrimSpace(os.Getenv(agentModelEnv)); value != "" {
		return agentModelChoice{Model: value}, nil
	}
	entries, err := a.collectAgentModels()
	if err != nil {
		return agentModelChoice{}, err
	}
	unavailable := make([]string, 0)
	for _, entry := range entries {
		if !strings.EqualFold(entry.Capability, "text") {
			continue
		}
		if !entry.Available {
			if entry.Reason != "" {
				unavailable = append(unavailable, entry.Model+"："+entry.Reason)
			}
			continue
		}
		return entry.choice(), nil
	}
	if len(unavailable) > 0 {
		return agentModelChoice{}, fmt.Errorf("本地没有可直连的文本模型：%s", strings.Join(unavailable, "; "))
	}
	return agentModelChoice{}, errors.New("未找到可用的文本模型：请先配置本地文本模型渠道，或用 agent_models 查看后在 agent_start 显式传 model")
}

func (entry agentModelEntry) choice() agentModelChoice {
	switch {
	case entry.LogicalModelID != "":
		return agentModelChoice{LogicalModelID: entry.LogicalModelID}
	case entry.ChannelID != "" && entry.ChannelModelKey != "":
		return agentModelChoice{ChannelID: entry.ChannelID, ChannelModelKey: entry.ChannelModelKey, Model: entry.ChannelModelKey}
	case entry.ChannelID != "":
		return agentModelChoice{Model: entry.Model}
	default:
		return agentModelChoice{Model: entry.Model}
	}
}

func (a *mcpAPI) collectAgentModels() ([]agentModelEntry, error) {
	entries := make([]agentModelEntry, 0)
	local, localErr := a.localAgentModels()
	system, systemErr := a.systemAgentModels()
	if localErr == nil {
		entries = append(entries, local...)
	}
	if systemErr == nil {
		entries = append(entries, system...)
	}
	entries = dedupeAgentModels(entries)
	// The desktop profile has no system catalog route. A missing catalog is
	// not an error when the local config answered; only report a failure when
	// both sources failed and produced no models.
	if len(entries) == 0 && localErr != nil && systemErr != nil {
		return nil, fmt.Errorf("本地模型配置: %v; 系统模型目录: %v", localErr, systemErr)
	}
	return entries, nil
}

type localModelConfigPayload struct {
	Config *struct {
		Channels []localModelChannel `json:"channels"`
	} `json:"config"`
}

type localModelChannel struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	DisplayName   string              `json:"displayName"`
	Enabled       *bool               `json:"enabled"`
	BaseURL       string              `json:"baseUrl"`
	InterfaceType string              `json:"interfaceType"`
	Models        []string            `json:"models"`
	ModelProfiles []localModelProfile `json:"modelProfiles"`
}

type localModelProfile struct {
	Model      string `json:"model"`
	Capability string `json:"capability"`
	Protocol   string `json:"protocol"`
}

func (a *mcpAPI) localAgentModels() ([]agentModelEntry, error) {
	data, err := a.do(context.Background(), http.MethodGet, "/workspace/model-config", nil)
	if err != nil {
		return nil, err
	}
	var payload localModelConfigPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Config == nil {
		return []agentModelEntry{}, nil
	}
	entries := make([]agentModelEntry, 0)
	for _, channel := range payload.Config.Channels {
		if channel.Enabled != nil && !*channel.Enabled {
			continue
		}
		profiles := make(map[string]localModelProfile, len(channel.ModelProfiles))
		for _, profile := range channel.ModelProfiles {
			if strings.TrimSpace(profile.Model) == "" {
				continue
			}
			profiles[profile.Model] = profile
		}
		models := make([]string, 0, len(channel.Models)+len(channel.ModelProfiles))
		seen := map[string]bool{}
		for _, model := range channel.Models {
			model = strings.TrimSpace(model)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			models = append(models, model)
		}
		for _, profile := range channel.ModelProfiles {
			model := strings.TrimSpace(profile.Model)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			models = append(models, model)
		}
		channelName := strings.TrimSpace(channel.DisplayName)
		if channelName == "" {
			channelName = strings.TrimSpace(channel.Name)
		}
		if channelName == "" {
			channelName = channel.ID
		}
		for _, model := range models {
			profile := profiles[model]
			protocol := strings.TrimSpace(profile.Protocol)
			if protocol == "" {
				protocol = strings.TrimSpace(channel.InterfaceType)
			}
			entry := agentModelEntry{
				Model:       model,
				ChannelID:   channel.ID,
				ChannelName: channelName,
				Capability:  strings.TrimSpace(profile.Capability),
				Protocol:    protocol,
				Source:      "local",
				Available:   true,
			}
			// Local workspaces deliberately reject server-managed system
			// channels, including the relative /api/ai/system/<channelId>
			// relay used by the hosted frontend. A remote MCP connection to a
			// hosted service can execute them. Report those local models but
			// do not steer an external Agent into a run that cannot execute.
			if strings.Contains(strings.ToLower(channel.BaseURL), "/api/ai/system/") {
				entry.ChannelModelKey = model
				if mcpLocalRuntime {
					entry.Available = false
					entry.Reason = "本地工作区不支持系统渠道中转；请配置直连模型渠道，或改用托管部署的远程 MCP"
				}
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

type modelCatalogPayload struct {
	Source string `json:"source"`
	Models []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Capability string `json:"capability"`
		Available  bool   `json:"available"`
	} `json:"models"`
	Channels []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Models []struct {
			ModelKey    string `json:"modelKey"`
			DisplayName string `json:"displayName"`
			Capability  string `json:"capability"`
			Available   bool   `json:"available"`
		} `json:"models"`
	} `json:"channels"`
}

func (a *mcpAPI) systemAgentModels() ([]agentModelEntry, error) {
	data, err := a.do(context.Background(), http.MethodGet, "/model-catalog", nil)
	if err != nil {
		return nil, err
	}
	var payload modelCatalogPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	entries := make([]agentModelEntry, 0, len(payload.Models))
	for _, model := range payload.Models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = strings.TrimSpace(model.ID)
		}
		if name == "" {
			continue
		}
		entries = append(entries, agentModelEntry{
			Model:          name,
			LogicalModelID: model.ID,
			Capability:     model.Capability,
			Source:         "system",
			Available:      model.Available,
		})
	}
	for _, channel := range payload.Channels {
		for _, model := range channel.Models {
			modelKey := strings.TrimSpace(model.ModelKey)
			if modelKey == "" {
				continue
			}
			name := strings.TrimSpace(model.DisplayName)
			if name == "" {
				name = modelKey
			}
			entries = append(entries, agentModelEntry{
				Model:           name,
				ChannelID:       channel.ID,
				ChannelModelKey: modelKey,
				ChannelName:     channel.Name,
				Capability:      model.Capability,
				Source:          "system",
				Available:       model.Available,
			})
		}
	}
	return entries, nil
}

func dedupeAgentModels(entries []agentModelEntry) []agentModelEntry {
	seen := map[string]bool{}
	out := make([]agentModelEntry, 0, len(entries))
	for _, entry := range entries {
		key := entry.LogicalModelID
		if key == "" {
			key = entry.ChannelID + "::" + entry.ChannelModelKey + "::" + entry.Model
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out
}
