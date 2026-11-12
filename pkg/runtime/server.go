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

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
)

// MCPServer is the minimal MCP server surface that tool registration
// (connectmcp.Register) programs against. The gosdk
// package adapts the official modelcontextprotocol/go-sdk server to it; other
// MCP libraries can be supported by implementing this one method.
type MCPServer interface {
	AddTool(tool Tool, handler ToolHandler)
}

// Tool describes an MCP tool independent of any MCP library.
type Tool struct {
	Name            string
	Description     string
	RawInputSchema  json.RawMessage
	RawOutputSchema json.RawMessage
}

// ToolHandler is the callback invoked when an MCP client calls a tool.
type ToolHandler func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error)

// CallToolRequest carries the decoded arguments from an MCP tool call.
type CallToolRequest struct {
	Arguments map[string]any
	// Header holds the headers of the HTTP request that carried the tool
	// call, or nil when the MCP transport is not HTTP (e.g. stdio).
	Header http.Header
}

// CallToolResult is the response from a tool handler.
type CallToolResult struct {
	Text              string
	StructuredContent any
	IsError           bool
}

// NewToolResultText creates a successful text result.
func NewToolResultText(text string) *CallToolResult {
	return &CallToolResult{Text: text}
}

// NewToolResultJSON creates a successful result that carries the same JSON
// payload as both unstructured text content (for backward compatibility) and
// structured content (matching the tool's output schema). The caller is
// responsible for passing JSON-encoded bytes; no validation is performed.
// The slice is copied into StructuredContent so callers may safely reuse
// the buffer afterwards.
func NewToolResultJSON(jsonBytes []byte) *CallToolResult {
	structured := append(json.RawMessage(nil), jsonBytes...)
	return &CallToolResult{
		Text:              string(jsonBytes),
		StructuredContent: structured,
	}
}

// NewToolResultError creates an error text result.
func NewToolResultError(text string) *CallToolResult {
	return &CallToolResult{Text: text, IsError: true}
}
