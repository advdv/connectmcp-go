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

// Handler-level round-trip tests: a model-shaped tools/call (discriminated oneof
// wrapper, OpenAI-strict null siblings, malformed oneof) goes through the actual
// generated/registered handler -> DecodeArguments -> protojson -> handler ->
// EncodeMessage -> tool result, on the go-sdk MCP adapter. This is the full
// schema->read->proto round trip without a live model.
package connectmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/advdv/connectmcp-go/internal/mcptest"
	"github.com/advdv/connectmcp-go/pkg/runtime/gosdk"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callTool issues a tools/call over the JSON-RPC surface and returns the
// parsed "result" object (content / structuredContent / isError).
func callTool(t *testing.T, raw *mcptest.Server, name string, args map[string]any) map[string]any {
	t.Helper()
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	resp := raw.HandleMessage(context.Background(), req)
	rb, _ := json.Marshal(resp)
	var parsed struct {
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, rb)
	}
	if parsed.Error != nil {
		t.Fatalf("transport-level JSON-RPC error (should be a tool result instead): %v", parsed.Error)
	}
	if parsed.Result == nil {
		t.Fatalf("no result in response: %s", rb)
	}
	return parsed.Result
}

// firstTextContent returns the text of the first content block of a tool result.
func firstTextContent(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in result: %v", result)
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

func TestRTT_OneofWrapper(t *testing.T) {
	g := NewWithT(t)
	srv := &fullTestServer{}
	raw := mcptest.NewServer(t)
	adapter := raw
	registerTestService(t, adapter, srv)

	t.Run("happy discriminated wrapper", func(t *testing.T) {
		callTool(t, raw, "testdata_TestService_CreateItem", map[string]any{
			"name":      "Widget",
			"item_type": map[string]any{"which": "product", "product": map[string]any{"price": 9.99, "quantity": 5}},
		})
		g.Expect(srv.lastCreateReq.GetProduct()).ToNot(BeNil())
		g.Expect(srv.lastCreateReq.GetProduct().GetPrice()).To(Equal(9.99))
	})

	t.Run("OpenAI-strict shape with null siblings", func(t *testing.T) {
		// OpenAI strict sends every member; unused ones null.
		callTool(t, raw, "testdata_TestService_CreateItem", map[string]any{
			"name":        "W2",
			"description": nil,
			"item_type":   map[string]any{"which": "service", "product": nil, "service": map[string]any{"duration": "1h", "recurring": true}},
		})
		g.Expect(srv.lastCreateReq.GetService()).ToNot(BeNil())
		g.Expect(srv.lastCreateReq.GetService().GetDuration()).To(Equal("1h"))
	})

	t.Run("malformed oneof returns IsError result, not transport error", func(t *testing.T) {
		result := callTool(t, raw, "testdata_TestService_CreateItem", map[string]any{
			"name":      "bad",
			"item_type": map[string]any{"which": "product", "product": nil, "service": map[string]any{"duration": "1h"}},
		})
		g.Expect(result["isError"]).To(Equal(true))
		// The message must name the fix so the model can self-correct.
		content, _ := json.Marshal(result["content"])
		g.Expect(string(content)).To(ContainSubstring("product"))
	})
}

func TestRTT_GoSDK_OneofWrapper(t *testing.T) {
	g := NewWithT(t)
	srv := &fullTestServer{}
	rawSrv, adapter := gosdk.NewServer("t", "1")
	registerTestService(t, adapter, srv)

	ctx := context.Background()
	clientT, serverT := mcp.NewInMemoryTransports()
	go func() { _ = rawSrv.Run(ctx, serverT) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	g.Expect(err).ToNot(HaveOccurred())
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "testdata_TestService_CreateItem",
		Arguments: map[string]any{
			"name":      "Gadget",
			"item_type": map[string]any{"which": "product", "product": map[string]any{"price": 1.5, "quantity": 2}},
		},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(res.IsError).To(BeFalse())
	g.Expect(srv.lastCreateReq.GetProduct()).ToNot(BeNil())
	g.Expect(srv.lastCreateReq.GetProduct().GetQuantity()).To(Equal(int32(2)))

	// Malformed -> IsError result (one-turn self-correction), not a transport error.
	res2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "testdata_TestService_CreateItem",
		Arguments: map[string]any{
			"name":      "bad",
			"item_type": map[string]any{"which": "service", "product": map[string]any{"price": 1}},
		},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(res2.IsError).To(BeTrue())
}
