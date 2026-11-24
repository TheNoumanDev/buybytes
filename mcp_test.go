package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
)

func TestMCPRefusesUnknownIDsAndListsTools(t *testing.T) {
	s := &mcpServer{home: t.TempDir(), known: map[string]sweep.Item{}}
	res, rerr := s.handle(rpcRequest{Method: "tools/list"})
	if rerr != nil {
		t.Fatal(rerr)
	}
	b, _ := json.Marshal(res)
	for _, name := range []string{`"scan"`, `"clean"`, `"free_space"`, `"destructiveHint":true`} {
		if !strings.Contains(string(b), name) {
			t.Errorf("tools/list missing %s", name)
		}
	}
	out, err := s.call("clean", json.RawMessage(`{"ids":["abc"]}`))
	if err != nil || !strings.Contains(out, "unknown id") {
		t.Errorf("clean with an unknown id should be refused, got %s %v", out, err)
	}
	if _, err := s.call("clean", json.RawMessage(`{}`)); err == nil {
		t.Error("clean without ids should fail")
	}
	if _, rerr := s.handle(rpcRequest{Method: "nope"}); rerr == nil || rerr.Code != -32601 {
		t.Error("unknown method should be -32601")
	}
	init, _ := s.handle(rpcRequest{Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2024-11-05"}`)})
	if init.(map[string]any)["protocolVersion"] != "2024-11-05" {
		t.Error("should answer with the client's supported protocol version")
	}
}
