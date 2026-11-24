package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func writeTestEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data, "msg": "ok"}); err != nil {
		t.Errorf("encode envelope: %v", err)
	}
}

func testAPI(t *testing.T, handler http.HandlerFunc) *mcpAPI {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newMCPAPI(server.URL, "test-token", server.Client())
}

func TestAgentStartAutoSelectsLocalTextModel(t *testing.T) {
	var submitted map[string]any
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Desktop-Token") != "test-token" {
			t.Errorf("missing launch token on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/workspace/model-config":
			writeTestEnvelope(t, w, map[string]any{"config": map[string]any{"channels": []any{
				map[string]any{
					"id": "local-openai", "name": "OpenAI", "enabled": true, "interfaceType": "openai",
					"models":        []string{"gpt-4o"},
					"modelProfiles": []any{map[string]any{"model": "gpt-4o", "capability": "text", "protocol": "openai"}},
				},
			}}})
		case "/model-catalog":
			http.NotFound(w, r)
		case "/agent/runs":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode agent request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
				"id": "run-1", "conversationId": "canvas-1", "canvasId": "canvas-1",
				"status": "queued", "permissionMode": "request_approval", "model": "gpt-4o",
			}})
		default:
			http.NotFound(w, r)
		}
	})

	result, err := api.agentStart(json.RawMessage(`{"prompt":"做一个 10 秒短片","canvasId":"canvas-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if submitted["model"] != "gpt-4o" {
		t.Fatalf("model = %#v", submitted["model"])
	}
	if submitted["conversationId"] != "canvas-1" {
		t.Fatalf("conversationId = %#v", submitted["conversationId"])
	}
	if v, ok := submitted["permissionMode"]; ok {
		t.Fatalf("permissionMode should be omitted so the server grant decides, got %#v", v)
	}
	scope, _ := submitted["contextScope"].([]any)
	if len(scope) != 1 || scope[0] != "canvas" {
		t.Fatalf("contextScope = %#v", submitted["contextScope"])
	}
	if key, _ := submitted["idempotencyKey"].(string); len(key) < 8 {
		t.Fatalf("idempotencyKey = %q", key)
	}
	out, _ := result.(map[string]any)
	if out["conversationId"] != "canvas-1" || out["modelSelectedAutomatically"] != true {
		t.Fatalf("result = %#v", out)
	}
}

func TestAgentStartPassesExplicitPermissionMode(t *testing.T) {
	var submitted map[string]any
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/workspace/model-config":
			writeTestEnvelope(t, w, map[string]any{"config": map[string]any{"channels": []any{
				map[string]any{
					"id": "local-openai", "name": "OpenAI", "enabled": true, "interfaceType": "openai",
					"models":        []string{"gpt-4o"},
					"modelProfiles": []any{map[string]any{"model": "gpt-4o", "capability": "text", "protocol": "openai"}},
				},
			}}})
		case "/model-catalog":
			http.NotFound(w, r)
		case "/agent/runs":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode agent request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
				"id": "run-1", "conversationId": "canvas-1", "status": "queued", "permissionMode": "auto",
			}})
		default:
			http.NotFound(w, r)
		}
	})

	if _, err := api.agentStart(json.RawMessage(`{"prompt":"做一个短片","canvasId":"canvas-1","permissionMode":"auto"}`)); err != nil {
		t.Fatal(err)
	}
	if submitted["permissionMode"] != "auto" {
		t.Fatalf("permissionMode = %#v", submitted["permissionMode"])
	}
}

func TestAgentStartRejectsSystemRelayLocalModel(t *testing.T) {
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/workspace/model-config":
			writeTestEnvelope(t, w, map[string]any{"config": map[string]any{"channels": []any{
				map[string]any{
					"id": "CHANNEL_000006", "name": "DeepSeek", "enabled": true,
					"baseUrl":       "/api/ai/system/CHANNEL_000006",
					"models":        []string{"deepseek-flash"},
					"modelProfiles": []any{map[string]any{"model": "deepseek-flash", "capability": "text"}},
				},
			}}})
		default:
			http.NotFound(w, r)
		}
	})
	models, err := api.agentModels(nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := models.(map[string]any)["models"].([]agentModelEntry)
	if len(entries) != 1 || entries[0].Available || !strings.Contains(entries[0].Reason, "系统渠道中转") {
		t.Fatalf("entries = %#v", entries)
	}

	_, err = api.agentStart(json.RawMessage(`{"prompt":"生成图片","model":"deepseek-flash"}`))
	if err == nil || !strings.Contains(err.Error(), "本地工作区不支持系统渠道中转") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentStartSurfacesBusinessError(t *testing.T) {
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":402,"data":null,"msg":"insufficient credits","reason":"余额不足"}`))
	})
	_, err := api.agentStart(json.RawMessage(`{"prompt":"生成视频","model":"gpt-4o"}`))
	if err == nil || !strings.Contains(err.Error(), "余额不足") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentStatusCompactsApprovalAndEvents(t *testing.T) {
	events := make([]any, 0, 12)
	for i := 1; i <= 12; i++ {
		events = append(events, map[string]any{"eventId": "e", "runId": "run-1", "seq": i, "type": "plan_update", "payload": map[string]any{"index": i}, "createdAt": "2026-01-01T00:00:00Z"})
	}
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/runs/run-1" {
			http.NotFound(w, r)
			return
		}
		writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
			"id": "run-1", "status": "waiting_approval", "events": events,
			"approval": map[string]any{
				"approvalId": "ap-1", "modelName": "图片模型",
				"call":    map[string]any{"id": "call-1", "function": map[string]any{"name": "generate_media", "arguments": `{"prompt":"海边"}`}},
				"preview": map[string]any{"kind": "generate_media", "title": "生成图片", "items": []any{map[string]any{"summary": "生成 1 张图"}}},
			},
		}})
	})

	result, err := api.agentStatus(json.RawMessage(`{"runId":"run-1","eventLimit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	run := out["run"].(map[string]any)
	approval := run["approval"].(map[string]any)
	arguments := approval["arguments"].(map[string]any)
	if arguments["prompt"] != "海边" {
		t.Fatalf("approval arguments = %#v", arguments)
	}
	if got := run["events"].([]any); len(got) != 3 {
		t.Fatalf("events = %d", len(got))
	}
	if run["eventsOmitted"].(int) != 9 {
		t.Fatalf("eventsOmitted = %#v", run["eventsOmitted"])
	}
	if out["waitingApproval"] != true || out["nextTool"] != agentToolDecide {
		t.Fatalf("status result = %#v", out)
	}
}

func TestAgentWaitReturnsOnWaitingApproval(t *testing.T) {
	calls := 0
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		run := map[string]any{"id": "run-1", "status": "running"}
		if calls >= 2 {
			run["status"] = "waiting_approval"
			run["approval"] = map[string]any{"approvalId": "ap-1", "call": map[string]any{"id": "call-1", "function": map[string]any{"name": "add_node", "arguments": "{}"}}}
		}
		writeTestEnvelope(t, w, map[string]any{"run": run})
	})

	result, err := api.agentWait(json.RawMessage(`{"runId":"run-1","timeoutSeconds":2,"pollIntervalMs":250}`))
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	if out["timedOut"] != false || out["waitingApproval"] != true || calls < 2 {
		t.Fatalf("result = %#v calls=%d", out, calls)
	}
}

func TestAgentWaitTimesOut(t *testing.T) {
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeTestEnvelope(t, w, map[string]any{"run": map[string]any{"id": "run-1", "status": "running"}})
	})
	result, err := api.agentWait(json.RawMessage(`{"runId":"run-1","timeoutSeconds":1,"pollIntervalMs":250}`))
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	if out["timedOut"] != true || out["nextTool"] != agentToolWait {
		t.Fatalf("result = %#v", out)
	}
}

func TestAgentModelsRedactsCredentials(t *testing.T) {
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/workspace/model-config":
			writeTestEnvelope(t, w, map[string]any{"config": map[string]any{"channels": []any{
				map[string]any{
					"id": "local-openai", "name": "OpenAI", "enabled": true, "apiKey": "super-secret-key",
					"models":        []string{"gpt-4o"},
					"modelProfiles": []any{map[string]any{"model": "gpt-4o", "capability": "text"}},
				},
			}}})
		default:
			http.NotFound(w, r)
		}
	})
	result, err := api.agentModels(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-key") {
		t.Fatalf("credential leaked: %s", raw)
	}
	if !strings.Contains(string(raw), `"textModels":["gpt-4o"]`) {
		t.Fatalf("missing text model: %s", raw)
	}
}

func TestAgentContinueInheritsParentSettings(t *testing.T) {
	var submitted map[string]any
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/runs/parent-1":
			writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
				"id": "parent-1", "conversationId": "conv-1", "canvasId": "canvas-1",
				"status": "completed", "permissionMode": "auto", "model": "gpt-4o",
			}})
		case "/agent/runs/parent-1/messages":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode continue request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
				"id": "run-2", "conversationId": "conv-1", "canvasId": "canvas-1",
				"status": "queued", "permissionMode": "auto", "model": "gpt-4o",
			}})
		default:
			http.NotFound(w, r)
		}
	})
	if _, err := api.agentContinue(json.RawMessage(`{"runId":"parent-1","prompt":"继续"}`)); err != nil {
		t.Fatal(err)
	}
	if submitted["conversationId"] != "conv-1" || submitted["canvasId"] != "canvas-1" {
		t.Fatalf("submitted = %#v", submitted)
	}
	if submitted["permissionMode"] != "auto" || submitted["model"] != "gpt-4o" {
		t.Fatalf("submitted = %#v", submitted)
	}
}

func TestAgentDecideUsesCurrentApproval(t *testing.T) {
	var decision map[string]any
	api := testAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/runs/run-1":
			writeTestEnvelope(t, w, map[string]any{"run": map[string]any{
				"id": "run-1", "status": "waiting_approval",
				"approval": map[string]any{"approvalId": "ap-1", "call": map[string]any{"id": "call-1", "function": map[string]any{"name": "add_node", "arguments": "{}"}}},
			}})
		case "/agent/runs/run-1/approvals/ap-1/decision":
			if err := json.NewDecoder(r.Body).Decode(&decision); err != nil {
				t.Errorf("decode decision: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			writeTestEnvelope(t, w, map[string]any{"accepted": true})
		default:
			http.NotFound(w, r)
		}
	})
	result, err := api.agentDecide(json.RawMessage(`{"runId":"run-1","decision":"approve","reason":"用户同意"}`))
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	if out["approvalId"] != "ap-1" || out["nextTool"] != agentToolWait {
		t.Fatalf("result = %#v", out)
	}
	if decision["decision"] != "approve" || decision["reason"] != "用户同意" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestAgentToolDefinitionsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, raw := range agentToolDefinitions() {
		tool := raw.(map[string]any)
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, name := range []string{agentToolStart, agentToolStatus, agentToolWait, agentToolContinue, agentToolDecide, agentToolCancel, agentToolModels, agentToolTaskGet} {
		if !names[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}
