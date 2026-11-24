package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/TheNoumanDev/buybytes/internal/sweep"
)

// A minimal MCP server over stdio (newline-delimited JSON-RPC 2.0), so coding
// agents can call buybytes as a tool: scan, then clean only what the user approved.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

var mcpTools = []map[string]any{
	{
		"name": "scan",
		"description": "Scan this Mac for reclaimable disk space. Returns items with id, title, size_bytes, verdict, " +
			"recommended, read_only and detail. Show the user what you found and let them choose before calling clean.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":     map[string]any{"type": "string", "enum": []string{"dev", "media", "all"}, "description": "dev: developer leftovers (default). media: recordings, videos, archives, apps. all: everything."},
				"min_size": map[string]any{"type": "string", "description": "Hide items smaller than this, like 100MB (default) or 1GB."},
			},
		},
		"annotations": map[string]any{"readOnlyHint": true},
	},
	{
		"name": "clean",
		"description": "Remove the items with these ids (from scan). Only pass ids the user explicitly approved. " +
			"Items go to the Trash unless purge is true; only set purge if the user asked for permanent deletion. Read-only items are refused.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ids":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1},
				"purge":   map[string]any{"type": "boolean", "description": "Delete permanently instead of moving to the Trash."},
				"dry_run": map[string]any{"type": "boolean", "description": "Report what would be removed without removing anything."},
			},
			"required": []string{"ids"},
		},
		"annotations": map[string]any{"destructiveHint": true},
	},
	{
		"name":        "free_space",
		"description": "How much disk space is free right now.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": map[string]any{"readOnlyHint": true},
	},
}

type mcpServer struct {
	home  string
	mu    sync.Mutex
	known map[string]sweep.Item // items from scans in this session, by id
}

func runMCP(home string) {
	quiet = true // stdout carries protocol messages only
	s := &mcpServer{home: home, known: map[string]sweep.Item{}}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcError{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification (initialized, cancelled): no reply
		}
		result, rerr := s.handle(req)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rerr != nil {
			resp["error"] = rerr
		} else {
			resp["result"] = result
		}
		_ = out.Encode(resp)
	}
}

func (s *mcpServer) handle(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := mcpVersions[0]
		for _, x := range mcpVersions {
			if x == p.ProtocolVersion {
				v = x
			}
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "buybytes", "version": version},
			"instructions":    "Scan first, show the user the items grouped by verdict, and clean only the ids they approve.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "bad params"}
		}
		text, err := s.call(p.Name, p.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func (s *mcpServer) call(name string, raw json.RawMessage) (string, error) {
	switch name {
	case "free_space":
		free := sweep.FreeBytes(s.home)
		return fmt.Sprintf(`{"free_bytes": %d, "free": %q}`, free, sweep.HumanSize(free)), nil
	case "scan":
		var a struct {
			Mode    string `json:"mode"`
			MinSize string `json:"min_size"`
		}
		_ = json.Unmarshal(raw, &a)
		m, err := sweep.ParseMode(a.Mode)
		if err != nil {
			return "", err
		}
		minSize := int64(100 << 20)
		if a.MinSize != "" {
			if minSize, err = parseSize(a.MinSize); err != nil {
				return "", fmt.Errorf("bad min_size: %v", err)
			}
		}
		env := sweep.NewEnvMode(s.home, minSize, m, nil)
		items := sweep.Scan(env, m, nil)
		s.mu.Lock()
		for _, it := range items {
			s.known[it.ID] = it
		}
		s.mu.Unlock()
		r := jsonReport{FreeBytes: sweep.FreeBytes(s.home), Items: items, SkippedFolders: env.Blocked}
		for _, it := range items {
			if !it.ReadOnly && !it.InUse {
				r.ReclaimableBytes += it.Size
				if it.Recommended {
					r.RecommendedBytes += it.Size
				}
			}
		}
		b, _ := json.Marshal(r)
		return string(b), nil
	case "clean":
		var a struct {
			IDs    []string `json:"ids"`
			Purge  bool     `json:"purge"`
			DryRun bool     `json:"dry_run"`
		}
		if err := json.Unmarshal(raw, &a); err != nil || len(a.IDs) == 0 {
			return "", fmt.Errorf("clean needs ids from scan")
		}
		type row struct {
			ID    string `json:"id"`
			Title string `json:"title,omitempty"`
			Size  int64  `json:"size_bytes"`
			Error string `json:"error,omitempty"`
		}
		var rows []row
		var removed []sweep.Item
		before := sweep.FreeBytes(s.home)
		for _, id := range a.IDs {
			s.mu.Lock()
			it, ok := s.known[id]
			s.mu.Unlock()
			if !ok {
				rows = append(rows, row{ID: id, Error: "unknown id: run scan first"})
				continue
			}
			r := sweep.Remove(s.home, it, a.Purge, a.DryRun)
			out := row{ID: id, Title: it.Title, Size: it.Size}
			if r.Err != nil {
				out.Error = r.Err.Error()
			} else {
				removed = append(removed, it)
				s.mu.Lock()
				delete(s.known, id)
				s.mu.Unlock()
			}
			rows = append(rows, out)
		}
		if !a.DryRun && len(removed) > 0 {
			_ = sweep.SaveLast(s.home, sweep.Summarize(removed))
		}
		b, _ := json.Marshal(map[string]any{
			"dry_run": a.DryRun, "purge": a.Purge, "freed_bytes": sweep.FreeBytes(s.home) - before, "results": rows,
			"note": "Items moved to the Trash free their space when the Trash is emptied.",
		})
		return string(b), nil
	}
	return "", fmt.Errorf("unknown tool %q", name)
}
