// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package gosdk provides an adapter from modelcontextprotocol/go-sdk to
// the runtime.MCPServer interface.
package gosdk

import (
	"context"

	"github.com/advdv/connectmcp-go/pkg/runtime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type server struct {
	s *mcp.Server
}

// Wrap returns a runtime.MCPServer backed by a go-sdk Server.
func Wrap(s *mcp.Server) runtime.MCPServer {
	return &server{s: s}
}

// NewServer creates a new go-sdk Server and returns it alongside the
// runtime.MCPServer adapter. Callers use the raw *mcp.Server for
// transport setup (e.g. s.Run with mcp.NewStdioTransport()) and the
// adapter for tool registration.
func NewServer(name, version string) (*mcp.Server, runtime.MCPServer) {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)
	return s, Wrap(s)
}

func (w *server) AddTool(tool runtime.Tool, handler runtime.ToolHandler) {
	mcpTool := &mcp.Tool{
		Name:        tool.Name,
		Description: tool.Description,
		// InputSchema/OutputSchema accept any JSON-marshalable value; json.RawMessage works.
		InputSchema: tool.RawInputSchema,
	}
	if len(tool.RawOutputSchema) > 0 {
		mcpTool.OutputSchema = tool.RawOutputSchema
	}
	if a := tool.Annotations; !a.IsZero() {
		// The title goes in both places: Tool.title is what current clients
		// display, annotations.title is where 2025-03-26 clients look.
		mcpTool.Title = a.Title
		mcpTool.Annotations = &mcp.ToolAnnotations{
			Title:           a.Title,
			ReadOnlyHint:    a.ReadOnlyHint,
			DestructiveHint: a.DestructiveHint,
			IdempotentHint:  a.IdempotentHint,
			OpenWorldHint:   a.OpenWorldHint,
		}
	}

	w.s.AddTool(mcpTool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// The arguments are passed on undecoded: the handler limits their size
		// before decoding them.
		toolRequest := &runtime.CallToolRequest{Arguments: request.Params.Arguments}
		if request.Extra != nil {
			toolRequest.Header = request.Extra.Header
		}
		result, err := handler(ctx, toolRequest)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, nil
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: result.Text}},
			StructuredContent: result.StructuredContent,
			IsError:           result.IsError,
		}, nil
	})
}
