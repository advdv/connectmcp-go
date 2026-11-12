// Package mcptest provides an in-memory MCP server for tests, backed by the
// official go-sdk, that is driven with raw JSON-RPC request messages.
package mcptest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/advdv/connectmcp-go/pkg/runtime"
	"github.com/advdv/connectmcp-go/pkg/runtime/gosdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is a runtime.MCPServer whose tools can be exercised with
// HandleMessage. Requests travel through a real go-sdk client session over an
// in-memory transport, so they see exactly what an MCP client would.
type Server struct {
	runtime.MCPServer

	tb      testing.TB
	raw     *mcp.Server
	connect sync.Once
	session *mcp.ClientSession
}

// NewServer returns a server with no tools registered.
func NewServer(tb testing.TB) *Server {
	tb.Helper()
	raw, adapter := gosdk.NewServer("test", "1.0")
	return &Server{MCPServer: adapter, tb: tb, raw: raw}
}

// HandleMessage executes one JSON-RPC request ("initialize", "tools/list" or
// "tools/call") and returns the JSON-RPC response object: it marshals to
// {"result": ...} on success and {"error": {"message": ...}} on a
// protocol-level failure. A failing tool is a success carrying isError.
func (s *Server) HandleMessage(ctx context.Context, msg json.RawMessage) any {
	s.tb.Helper()

	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg, &req); err != nil {
		s.tb.Fatalf("mcptest: invalid JSON-RPC request: %v", err)
	}

	session := s.connected(ctx)

	var (
		result any
		err    error
	)
	switch req.Method {
	case "initialize":
		result = session.InitializeResult()
	case "tools/list":
		result, err = session.ListTools(ctx, nil)
	case "tools/call":
		params := &mcp.CallToolParams{Name: req.Params.Name}
		if len(req.Params.Arguments) > 0 {
			params.Arguments = req.Params.Arguments
		}
		result, err = session.CallTool(ctx, params)
	default:
		s.tb.Fatalf("mcptest: unsupported method %q", req.Method)
	}

	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if err != nil {
		resp["error"] = map[string]any{"message": err.Error()}
	} else {
		resp["result"] = result
	}
	return resp
}

// connected lazily connects a client session, so that tools registered after
// NewServer are all in place by the first request.
func (s *Server) connected(ctx context.Context) *mcp.ClientSession {
	s.tb.Helper()
	s.connect.Do(func() {
		clientT, serverT := mcp.NewInMemoryTransports()
		serverSession, err := s.raw.Connect(ctx, serverT, nil)
		if err != nil {
			s.tb.Fatalf("mcptest: server connect: %v", err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
		s.session, err = client.Connect(ctx, clientT, nil)
		if err != nil {
			s.tb.Fatalf("mcptest: client connect: %v", err)
		}
		s.tb.Cleanup(func() {
			_ = s.session.Close()
			_ = serverSession.Wait()
		})
	})
	return s.session
}
