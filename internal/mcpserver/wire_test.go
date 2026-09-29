package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wireCaller talks JSON-RPC to a server on the current protocol, so a test sees
// the result exactly as a client on that protocol does (the SDK's typed client
// hides the resultType a real client requires).
func wireCaller(t *testing.T, srv *Server) func(name string, args map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.MCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	conn, err := ct.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	next := 0
	return func(name string, args map[string]any) map[string]any {
		t.Helper()
		next++
		params, _ := json.Marshal(map[string]any{"name": name, "arguments": args, "_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "wire", "version": "0"},
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}})
		id, _ := jsonrpc.MakeID(float64(next))
		if err := conn.Write(ctx, &jsonrpc.Request{ID: id, Method: "tools/call", Params: params}); err != nil {
			t.Fatal(err)
		}
		msg, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		data, err := jsonrpc.EncodeMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		if env.Error != nil || env.Result == nil {
			t.Fatalf("%s: a protocol error instead of a result: %s", name, data)
		}
		return env.Result
	}
}

func wireText(res map[string]any) string {
	content, _ := res["content"].([]any)
	var b strings.Builder
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			b.WriteString(m["text"].(string))
		}
	}
	return b.String()
}

// D7: an argument a tool does not have is an invalid_input result that is
// complete on the wire (the resultType a client on the current protocol
// requires), for every tool.
func TestUnknownArgumentOnEveryToolIsAWellFormedInvalidInput(t *testing.T) {
	pf := newProjFixture(t)
	call := wireCaller(t, pf.srv)
	for _, name := range toolTextNames() {
		res := call(name, map[string]any{"zz_unknown_argument": 1})
		if res["resultType"] != "complete" {
			t.Errorf("%s: resultType = %v", name, res["resultType"])
		}
		if res["isError"] != true || !strings.Contains(wireText(res), "invalid_input") {
			t.Errorf("%s: %s", name, wireText(res))
		}
	}
	// A call that names its required arguments and one extra: the extra one
	// is the problem the message names.
	id := pf.create(t, "wire")
	res := call("set_layer_actions", map[string]any{"project": id, "actions": []map[string]any{{"z": 5, "type": "pause"}}, "include_screenshot": true})
	if res["resultType"] != "complete" || !strings.Contains(wireText(res), "include_screenshot") || !strings.Contains(wireText(res), "invalid_input") {
		t.Errorf("set_layer_actions with include_screenshot: %v %s", res["resultType"], wireText(res))
	}
	// The project was not changed by the refused call.
	info, err := pf.store.GetProject(id)
	if err != nil || info.Revision != 1 {
		t.Errorf("revision after a refused call: %v, %v", info, err)
	}
	// A good call is complete too.
	if ok := call("list_projects", map[string]any{}); ok["resultType"] != "complete" || ok["isError"] == true {
		t.Errorf("list_projects: %v", ok)
	}
}

// MC2: a panic in a handler is an internal_error result, complete on the wire,
// and the server keeps serving.
func TestAPanicInAHandlerIsAWellFormedInternalError(t *testing.T) {
	srv := newServer(Config{Version: "1.2.3", Deps: newFixture(t).cfg.Deps}, sampleTools(t))
	call := wireCaller(t, srv)
	res := call("sample_fail", map[string]any{"mode": "panic"})
	if res["resultType"] != "complete" || res["isError"] != true || !strings.Contains(wireText(res), "internal_error") || !strings.Contains(wireText(res), "report it") {
		t.Fatalf("panic result: %v %s", res["resultType"], wireText(res))
	}
	if ok := call("sample_ok", map[string]any{"name": "still here"}); ok["isError"] == true {
		t.Errorf("the server stopped serving: %s", wireText(ok))
	}
}
