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

package connectmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
	. "github.com/onsi/gomega"
)

// doubleEncodedStructArgs is the exact payload Claude sent in a failing live
// conformance run: the Struct field "metadata" was JSON-encoded twice.
const doubleEncodedStructArgs = `{"config":"\"verbose\"",` +
	`"metadata":"\"{\\\"environment\\\": \\\"production\\\", \\\"replicas\\\": 3}\"",` +
	`"timestamp":"2026-01-02T03:04:05Z"}`

// handlerRecorder is a runtime.MCPServer that captures the registered tool
// handlers so tests can call them directly.
type handlerRecorder struct {
	handlers map[string]runtime.ToolHandler
}

func (r *handlerRecorder) AddTool(tool runtime.Tool, handler runtime.ToolHandler) {
	if r.handlers == nil {
		r.handlers = map[string]runtime.ToolHandler{}
	}
	r.handlers[tool.Name] = handler
}

// wktServer is a connect handler for TestService, recording calls.
type wktServer struct {
	testdataconnect.UnimplementedTestServiceHandler
	called bool
}

func (s *wktServer) ProcessWellKnownTypes(context.Context, *testdata.ProcessWellKnownTypesRequest) (*testdata.ProcessWellKnownTypesResponse, error) {
	s.called = true
	return &testdata.ProcessWellKnownTypesResponse{Success: true}, nil
}

func callWithJSON(g *WithT, h runtime.ToolHandler, args string) *runtime.CallToolResult {
	var m map[string]any
	g.Expect(json.Unmarshal([]byte(args), &m)).To(Succeed())
	res, err := h(context.Background(), &runtime.CallToolRequest{Arguments: m})
	// Bad arguments must never surface as a protocol-level error: the model
	// would not see it and could not correct itself.
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(res).ToNot(BeNil())
	return res
}

func expectModelReadableArgumentError(g *WithT, res *runtime.CallToolResult) {
	g.Expect(res.IsError).To(BeTrue())
	g.Expect(res.Text).To(ContainSubstring(`field "metadata" must be a JSON object (google.protobuf.Struct)`))
	g.Expect(res.Text).To(ContainSubstring("exactly once, not twice"))
	g.Expect(res.Text).To(ContainSubstring("call the tool again"))
}

func TestGeneratedHandler_BadArgumentsReturnToolError(t *testing.T) {
	g := NewWithT(t)
	rec := &handlerRecorder{}
	srv := &wktServer{}
	registerTestService(t, rec, srv)
	h := rec.handlers[testServiceTool("ProcessWellKnownTypes").Name]
	g.Expect(h).ToNot(BeNil())

	expectModelReadableArgumentError(g, callWithJSON(g, h, doubleEncodedStructArgs))
	g.Expect(srv.called).To(BeFalse(), "the service must not be called with bad arguments")

	// The corrected call goes through.
	res := callWithJSON(g, h, `{"metadata":"{\"environment\":\"production\"}","timestamp":"2026-01-02T03:04:05Z"}`)
	g.Expect(res.IsError).To(BeFalse(), res.Text)
	g.Expect(srv.called).To(BeTrue())
}
