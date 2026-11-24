package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
)

// TestAgentRunExecutesHeadlessly exercises the real bootstrap, task worker and
// Agent scheduler against a local fake chat-completions upstream. It is the
// end-to-end contract behind the MCP tools: no browser or desktop UI drives it.
func TestAgentRunExecutesHeadlessly(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"完成\"},\"finish_reason\":null}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	dataDir := t.TempDir()
	config := map[string]any{"channels": []any{
		map[string]any{
			"id": "fake", "name": "Fake", "baseUrl": upstream.URL, "apiKey": "test-key", "enabled": true,
			"models":        []string{"fake-text"},
			"modelProfiles": []any{map[string]any{"model": "fake-text", "capability": "text", "protocol": "chat-completion"}},
		},
	}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dataDir, "local-model-config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	rt, err := bootstrap.Open(context.Background(), bootstrap.Config{
		Profile: bootstrap.ProfileDesktop, DataDir: dataDir, ListenAddr: "127.0.0.1:0", AutoMigrate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close(context.Background()) }()
	rt.StartAgentScheduler()

	api := newMCPAPI(rt.BaseURL(), rt.LaunchToken(), &http.Client{Timeout: 30 * time.Second})
	started, err := api.agentStart(json.RawMessage(`{"prompt":"只回复：完成","model":"fake-text","permissionMode":"read_only"}`))
	if err != nil {
		t.Fatal(err)
	}
	startedMap, _ := started.(map[string]any)
	runMap, _ := startedMap["run"].(map[string]any)
	runID, _ := runMap["id"].(string)
	if runID == "" {
		t.Fatalf("started = %#v", started)
	}
	waited, err := api.agentWait(json.RawMessage(`{"runId":"` + runID + `","timeoutSeconds":20,"pollIntervalMs":250}`))
	if err != nil {
		t.Fatal(err)
	}
	out := waited.(map[string]any)
	run := out["run"].(map[string]any)
	if run["status"] != "completed" {
		t.Fatalf("run = %#v", run)
	}
	if requests.Load() == 0 {
		t.Fatal("fake upstream was never called")
	}
}
