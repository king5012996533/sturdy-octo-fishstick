// Command mcp exposes a tiny stdio MCP-compatible adapter for BeefTV's
// existing local HTTP runtime. It is intentionally additive: it does not
// register routes, alter the Wails UI, or replace the desktop service.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm/logger"

	"infinite-canvas/backend/internal/bootstrap"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// mcpLocalRuntime is true when the adapter boots its own desktop-profile
// backend. Hosted remotes can execute system-relay channels; local runtimes
// deliberately cannot.
var mcpLocalRuntime = true

type authTransport struct {
	base   http.RoundTripper
	token  string
	bearer string
	cookie string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Del("X-Desktop-Token")
	switch {
	case t.cookie != "":
		clone.Header.Set("Cookie", t.cookie)
	case t.bearer != "":
		clone.Header.Set("Authorization", "Bearer "+t.bearer)
	case t.token != "":
		clone.Header.Set("X-Desktop-Token", t.token)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func normalizeMCPBaseURL(raw string) string {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if strings.HasSuffix(value, "/api") {
		return value
	}
	return value + "/api"
}

// resolveMCPLocalRuntime decides whether system-relay channels are executable.
// A session cookie implies a hosted service; a desktop launch token implies a
// local desktop backend. KINOTV_MCP_PROFILE overrides both when a deployment
// uses a different auth combination.
func resolveMCPLocalRuntime(profile, token, bearer, cookie string) bool {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "local", "desktop":
		return true
	case "hosted", "server":
		return false
	}
	if bearer != "" || cookie != "" {
		return false
	}
	return token != ""
}

// resolveRuntime returns the base URL, auth material and cleanup function for
// either an existing Kinotv service or an embedded desktop runtime.
func resolveRuntime() (string, string, string, string, func()) {
	if remote := strings.TrimSpace(os.Getenv("KINOTV_MCP_BASE_URL")); remote != "" {
		token := strings.TrimSpace(os.Getenv("KINOTV_MCP_TOKEN"))
		bearer := strings.TrimSpace(os.Getenv("KINOTV_MCP_ACCESS_TOKEN"))
		if bearer == "" {
			bearer = strings.TrimSpace(os.Getenv("KINOTV_MCP_BEARER_TOKEN"))
		}
		cookie := strings.TrimSpace(os.Getenv("KINOTV_MCP_COOKIE"))
		if cookie == "" {
			cookie = strings.TrimSpace(os.Getenv("KINOTV_MCP_SESSION_COOKIE"))
		}
		mcpLocalRuntime = resolveMCPLocalRuntime(os.Getenv("KINOTV_MCP_PROFILE"), token, bearer, cookie)
		base := normalizeMCPBaseURL(remote)
		fmt.Fprintf(os.Stderr, "kinotv mcp: remote mode %s\n", base)
		if token == "" && bearer == "" && cookie == "" {
			fmt.Fprintln(os.Stderr, "kinotv mcp: warning: no KINOTV_MCP_ACCESS_TOKEN, KINOTV_MCP_COOKIE or KINOTV_MCP_TOKEN set; requests may be rejected")
		}
		return base, token, bearer, cookie, func() {}
	}

	dataDir := strings.TrimSpace(os.Getenv("CANVAS_DATA_DIR"))
	if dataDir == "" {
		dataDir = strings.TrimSpace(os.Getenv("CANVAS_DESKTOP_DATA_DIR"))
	}
	if dataDir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		dataDir = filepath.Join(root, "BeefTV")
	}
	rt, err := bootstrap.Open(context.Background(), bootstrap.Config{Profile: bootstrap.ProfileDesktop, DataDir: dataDir, ListenAddr: "127.0.0.1:0", AutoMigrate: true})
	if err != nil {
		panic(err)
	}
	if err = rt.Start(); err != nil {
		panic(err)
	}
	// The desktop profile does not start the Agent scheduler with its normal
	// worker. An external MCP client has no UI to drive that loop, so the
	// adapter starts it here for headless Agent runs.
	rt.StartAgentScheduler()
	return rt.BaseURL(), rt.LaunchToken(), "", "", func() { _ = rt.Close(context.Background()) }
}

func main() {
	// stdout is the JSON-RPC transport. Gin's access log and GORM's default
	// logger also target stdout, which would corrupt every tool response.
	gin.DefaultWriter = os.Stderr
	gin.DefaultErrorWriter = os.Stderr
	logger.Default = logger.Default.LogMode(logger.Silent)

	baseURL, token, bearer, cookie, cleanup := resolveRuntime()
	defer cleanup()
	client := &http.Client{Timeout: 30 * time.Second, Transport: authTransport{base: http.DefaultTransport, token: token, bearer: bearer, cookie: cookie}}

	s := bufio.NewScanner(os.Stdin)
	// MCP tool arguments can carry large canvas batches or long prompts. The
	// default 64KB scanner limit silently ends the server on a big request.
	s.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for s.Scan() {
		var req rpcRequest
		if json.Unmarshal(s.Bytes(), &req) != nil {
			continue
		}
		resp := handle(req, baseURL, token, client)
		if req.ID == nil {
			// JSON-RPC notifications never receive a response.
			continue
		}
		b, _ := json.Marshal(resp)
		fmt.Println(string(b))
	}
}

func handle(req rpcRequest, base, token string, client *http.Client) rpcResponse {
	r := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		r.Result = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]any{"name": "beeftv-canvas", "version": "0.1.0"}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "notifications/initialized", "notifications/cancelled":
		r.Result = map[string]any{}
	case "ping":
		r.Result = map[string]any{}
	case "tools/list":
		r.Result = map[string]any{"tools": []any{map[string]any{"name": "canvas_list", "description": "List local BeefTV canvases", "inputSchema": map[string]any{"type": "object"}}, map[string]any{"name": "canvas_get", "description": "Read one local canvas by id", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}}, "required": []string{"canvasId"}}}, map[string]any{"name": "canvas_add_text_node", "description": "Add a text node; defaults to isolated MCP test data", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string", "enum": []string{"test", "production"}}, "runId": map[string]any{"type": "string"}}, "required": []string{"canvasId", "title", "content"}}}, map[string]any{"name": "canvas_add_node", "description": "Add an image, video, or text node; defaults to isolated MCP test data", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "type": map[string]any{"type": "string", "enum": []string{"text", "image", "video"}}, "title": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string", "enum": []string{"test", "production"}}, "runId": map[string]any{"type": "string"}}, "required": []string{"canvasId", "type", "title"}}}, map[string]any{"name": "canvas_delete_node", "description": "Delete one node atomically by id", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "nodeId": map[string]any{"type": "string"}}, "required": []string{"canvasId", "nodeId"}}}, map[string]any{"name": "canvas_update_node", "description": "Update a node title, position, or metadata atomically", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "nodeId": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "position": map[string]any{"type": "object"}, "metadata": map[string]any{"type": "object"}}, "required": []string{"canvasId", "nodeId"}}}, map[string]any{"name": "canvas_connect_nodes", "description": "Connect two nodes atomically", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "fromNodeId": map[string]any{"type": "string"}, "toNodeId": map[string]any{"type": "string"}}, "required": []string{"canvasId", "fromNodeId", "toNodeId"}}}, map[string]any{"name": "canvas_clear_test_nodes", "description": "Remove MCP test nodes from a canvas, optionally by runId", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "runId": map[string]any{"type": "string"}}, "required": []string{"canvasId"}}}}}
	case "tools/call":
		r.Result = callTool(req.Params, base, token, client)
	default:
		r.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	if req.Method == "tools/list" {
		if result, ok := r.Result.(map[string]any); ok {
			if tools, ok := result["tools"].([]any); ok {
				tools = append(tools, map[string]any{"name": "canvas_batch_apply", "description": "Apply multiple atomic canvas operations in one MCP call", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"canvasId": map[string]any{"type": "string"}, "operations": map[string]any{"type": "array"}}, "required": []string{"canvasId", "operations"}}})
				result["tools"] = append(tools, agentToolDefinitions()...)
			}
		}
	}
	return r
}

func callTool(raw json.RawMessage, base, token string, client *http.Client) any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(raw, &p)
	if encoded, err := json.Marshal(p.Arguments); err == nil {
		if result, handled := callAgentTool(p.Name, encoded, base, token, client); handled {
			return result
		}
	}
	path := "/canvas-projects"
	if p.Name == "canvas_add_text_node" {
		return addTextNode(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_add_node" {
		return addNode(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_clear_test_nodes" {
		return clearTestNodes(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_delete_node" {
		return deleteNode(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_update_node" {
		return updateNode(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_connect_nodes" {
		return connectNodes(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_batch_apply" {
		return batchApply(p.Arguments, base, token, client)
	}
	if p.Name == "canvas_get" {
		id, _ := p.Arguments["canvasId"].(string)
		path += "/" + id
	}
	if p.Name != "canvas_list" && p.Name != "canvas_get" {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "unsupported tool"}}}
	}
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	req.Header.Set("X-Desktop-Token", token)
	res, err := client.Do(req)
	if err != nil {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": string(body)}}}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(body)}}}
}

func deleteNode(args map[string]any, base, token string, client *http.Client) any {
	canvasID, _ := args["canvasId"].(string)
	nodeID, _ := args["nodeId"].(string)
	if canvasID == "" || nodeID == "" {
		return toolError(fmt.Errorf("canvasId and nodeId are required"))
	}
	req, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(base, "/")+"/canvas-projects/"+canvasID+"/nodes/"+nodeID, nil)
	req.Header.Set("X-Desktop-Token", token)
	res, err := client.Do(req)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return toolError(fmt.Errorf("node delete failed: %s", body))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(body)}}}
}

func updateNode(args map[string]any, base, token string, client *http.Client) any {
	canvasID, _ := args["canvasId"].(string)
	nodeID, _ := args["nodeId"].(string)
	if canvasID == "" || nodeID == "" {
		return toolError(fmt.Errorf("canvasId and nodeId are required"))
	}
	patch := map[string]any{}
	for _, key := range []string{"title", "position", "metadata"} {
		if value, ok := args[key]; ok {
			patch[key] = value
		}
	}
	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, strings.TrimRight(base, "/")+"/canvas-projects/"+canvasID+"/nodes/"+nodeID, strings.NewReader(string(body)))
	req.Header.Set("X-Desktop-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return toolError(fmt.Errorf("node update failed: %s", out))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}}
}

func connectNodes(args map[string]any, base, token string, client *http.Client) any {
	canvasID, _ := args["canvasId"].(string)
	fromID, _ := args["fromNodeId"].(string)
	toID, _ := args["toNodeId"].(string)
	body, _ := json.Marshal(map[string]any{"fromNodeId": fromID, "toNodeId": toID})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/canvas-projects/"+canvasID+"/connections", strings.NewReader(string(body)))
	req.Header.Set("X-Desktop-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return toolError(fmt.Errorf("node connect failed: %s", out))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}}
}

func batchApply(args map[string]any, base, token string, client *http.Client) any {
	canvasID, _ := args["canvasId"].(string)
	operations, _ := args["operations"].([]any)
	scope, _ := args["scope"].(string)
	source := "mcp-test"
	if scope == "production" {
		source = "mcp-production"
	}
	if canvasID == "" || len(operations) == 0 {
		return toolError(fmt.Errorf("canvasId and operations are required"))
	}
	project, err := readProject(canvasID, base, token, client)
	if err != nil {
		return toolError(err)
	}
	nodes, _ := project["nodes"].([]any)
	connections, _ := project["connections"].([]any)
	for _, raw := range operations {
		op, ok := raw.(map[string]any)
		if !ok {
			return toolError(fmt.Errorf("invalid batch operation"))
		}
		name, _ := op["name"].(string)
		switch name {
		case "add":
			nodeType, _ := op["type"].(string)
			if nodeType == "" {
				nodeType = "text"
			}
			if nodeType != "text" && nodeType != "image" && nodeType != "video" {
				return toolError(fmt.Errorf("invalid node type"))
			}
			title, _ := op["title"].(string)
			content, _ := op["content"].(string)
			runID, _ := op["runId"].(string)
			if runID == "" {
				runID = "mcp-" + uuid.NewString()
			}
			nodes = append(nodes, map[string]any{"id": uuid.NewString(), "type": nodeType, "title": title, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "updatedAt": time.Now().UTC().Format(time.RFC3339Nano), "position": map[string]any{"x": 0, "y": 0}, "width": 360, "height": 240, "metadata": map[string]any{"content": content, "status": "success", "source": source, "runId": runID}})
		case "update":
			nodeID, _ := op["nodeId"].(string)
			found := false
			for _, rawNode := range nodes {
				node, _ := rawNode.(map[string]any)
				if node["id"] != nodeID {
					continue
				}
				found = true
				for _, key := range []string{"title", "position", "metadata"} {
					if value, ok := op[key]; ok {
						node[key] = value
					}
				}
				break
			}
			if !found {
				return toolError(fmt.Errorf("node not found: %s", nodeID))
			}
		case "delete":
			nodeID, _ := op["nodeId"].(string)
			filtered := make([]any, 0, len(nodes))
			found := false
			for _, rawNode := range nodes {
				node, _ := rawNode.(map[string]any)
				if node["id"] == nodeID {
					found = true
					continue
				}
				filtered = append(filtered, rawNode)
			}
			if !found {
				return toolError(fmt.Errorf("node not found: %s", nodeID))
			}
			nodes = filtered
			keptConnections := make([]any, 0, len(connections))
			for _, rawConn := range connections {
				conn, _ := rawConn.(map[string]any)
				if conn["fromNodeId"] == nodeID || conn["toNodeId"] == nodeID {
					continue
				}
				keptConnections = append(keptConnections, rawConn)
			}
			connections = keptConnections
		case "connect":
			fromID, _ := op["fromNodeId"].(string)
			toID, _ := op["toNodeId"].(string)
			existsFrom, existsTo := false, false
			for _, rawNode := range nodes {
				node, _ := rawNode.(map[string]any)
				if node["id"] == fromID {
					existsFrom = true
				}
				if node["id"] == toID {
					existsTo = true
				}
			}
			if !existsFrom || !existsTo {
				return toolError(fmt.Errorf("connection node not found"))
			}
			connections = append(connections, map[string]any{"id": uuid.NewString(), "fromNodeId": fromID, "toNodeId": toID})
		default:
			return toolError(fmt.Errorf("unsupported batch operation: %s", name))
		}
	}
	project["nodes"] = nodes
	project["connections"] = connections
	project["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, _ := json.Marshal(map[string]any{"project": project})
	req, _ := http.NewRequest(http.MethodPut, strings.TrimRight(base, "/")+"/canvas-projects/"+canvasID, strings.NewReader(string(payload)))
	req.Header.Set("X-Desktop-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return toolError(fmt.Errorf("batch commit failed: %s", out))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("applied %d operations in one commit: %s", len(operations), out)}}}
}

func addNode(args map[string]any, base, token string, client *http.Client) any {
	nodeType, _ := args["type"].(string)
	if nodeType != "text" && nodeType != "image" && nodeType != "video" {
		return toolError(fmt.Errorf("type must be text, image, or video"))
	}
	result := addTextNode(map[string]any{"canvasId": args["canvasId"], "title": args["title"], "content": args["content"], "scope": args["scope"], "runId": args["runId"]}, base, token, client)
	// addTextNode performs the safe read/modify/write transaction. For non-text
	// nodes, rewrite the newly appended node type in a second guarded update.
	if nodeType == "text" {
		return result
	}
	return rewriteLastNodeType(args, nodeType, base, token, client)
}

func rewriteLastNodeType(args map[string]any, nodeType, base, token string, client *http.Client) any {
	id, _ := args["canvasId"].(string)
	get, _ := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/canvas-projects/"+id, nil)
	get.Header.Set("X-Desktop-Token", token)
	res, err := client.Do(get)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var envelope struct {
		Data struct {
			Project map[string]any `json:"project"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return toolError(fmt.Errorf("invalid canvas response"))
	}
	nodes, _ := envelope.Data.Project["nodes"].([]any)
	if len(nodes) == 0 {
		return toolError(fmt.Errorf("canvas has no nodes"))
	}
	last, _ := nodes[len(nodes)-1].(map[string]any)
	last["type"] = nodeType
	payload, _ := json.Marshal(map[string]any{"project": envelope.Data.Project})
	put, _ := http.NewRequest(http.MethodPut, strings.TrimRight(base, "/")+"/canvas-projects/"+id, strings.NewReader(string(payload)))
	put.Header.Set("X-Desktop-Token", token)
	put.Header.Set("Content-Type", "application/json")
	outRes, err := client.Do(put)
	if err != nil {
		return toolError(err)
	}
	defer outRes.Body.Close()
	out, _ := io.ReadAll(outRes.Body)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}}
}

func addTextNode(args map[string]any, base, token string, client *http.Client) any {
	id, _ := args["canvasId"].(string)
	title, _ := args["title"].(string)
	content, _ := args["content"].(string)
	if id == "" || title == "" {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "canvasId and title are required"}}}
	}
	get, _ := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/canvas-projects/"+id, nil)
	get.Header.Set("X-Desktop-Token", token)
	res, err := client.Do(get)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return toolError(fmt.Errorf("canvas read failed: %s", body))
	}
	var envelope struct {
		Data struct {
			Project map[string]any `json:"project"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data.Project == nil {
		return toolError(fmt.Errorf("invalid canvas response"))
	}
	project := envelope.Data.Project
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "test"
	}
	runID, _ := args["runId"].(string)
	if runID == "" {
		runID = "mcp-" + uuid.NewString()
	}
	node := map[string]any{"id": uuid.NewString(), "type": "text", "title": title, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "updatedAt": time.Now().UTC().Format(time.RFC3339Nano), "position": map[string]any{"x": 0, "y": 0}, "width": 360, "height": 240, "metadata": map[string]any{"content": content, "status": "success", "source": "mcp-" + scope, "runId": runID}}
	nodes, _ := project["nodes"].([]any)
	project["nodes"] = append(nodes, node)
	project["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, _ := json.Marshal(map[string]any{"project": project})
	put, _ := http.NewRequest(http.MethodPut, strings.TrimRight(base, "/")+"/canvas-projects/"+id, strings.NewReader(string(payload)))
	put.Header.Set("X-Desktop-Token", token)
	put.Header.Set("Content-Type", "application/json")
	res2, err := client.Do(put)
	if err != nil {
		return toolError(err)
	}
	defer res2.Body.Close()
	out, _ := io.ReadAll(res2.Body)
	if res2.StatusCode >= 300 {
		return toolError(fmt.Errorf("canvas write failed: %s", out))
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}}
}

func clearTestNodes(args map[string]any, base, token string, client *http.Client) any {
	id, _ := args["canvasId"].(string)
	runID, _ := args["runId"].(string)
	project, err := readProject(id, base, token, client)
	if err != nil {
		return toolError(err)
	}
	nodes, _ := project["nodes"].([]any)
	kept := make([]any, 0, len(nodes))
	removedIDs := map[string]bool{}
	removed := 0
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		meta, _ := node["metadata"].(map[string]any)
		source, _ := meta["source"].(string)
		batch, _ := meta["runId"].(string)
		if source == "mcp-test" && (runID == "" || batch == runID) {
			if nodeID, ok := node["id"].(string); ok {
				removedIDs[nodeID] = true
			}
			removed++
			continue
		}
		kept = append(kept, raw)
	}
	project["nodes"] = kept
	connections, _ := project["connections"].([]any)
	filteredConnections := make([]any, 0, len(connections))
	for _, raw := range connections {
		connection, _ := raw.(map[string]any)
		from, _ := connection["fromNodeId"].(string)
		to, _ := connection["toNodeId"].(string)
		if removedIDs[from] || removedIDs[to] {
			continue
		}
		filteredConnections = append(filteredConnections, raw)
	}
	project["connections"] = filteredConnections
	payload, _ := json.Marshal(map[string]any{"project": project})
	req, _ := http.NewRequest(http.MethodPut, strings.TrimRight(base, "/")+"/canvas-projects/"+id, strings.NewReader(string(payload)))
	req.Header.Set("X-Desktop-Token", token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return toolError(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("removed %d test nodes: %s", removed, out)}}}
}

func readProject(id, base, token string, client *http.Client) (map[string]any, error) {
	req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/canvas-projects/"+id, nil)
	req.Header.Set("X-Desktop-Token", token)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var envelope struct {
		Data struct {
			Project map[string]any `json:"project"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data.Project == nil {
		return nil, fmt.Errorf("invalid canvas response")
	}
	return envelope.Data.Project, nil
}

func toolError(err error) any {
	return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}
}
