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

//go:build external_llm

package conformancetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/advdv/connectmcp-go/pkg/runtime"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	"github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/redpanda-data/ai-sdk-go/llm"
)

// doubleEncodedStructArgs is the exact payload Claude sent in a failing live run
// of TestConformance_WellKnownTypes: the Struct field "metadata" (downgraded to
// a string by the client) was JSON-encoded twice.
const doubleEncodedStructArgs = `{"config":"\"verbose\"",` +
	`"metadata":"\"{\\\"environment\\\": \\\"production\\\", \\\"replicas\\\": 3}\"",` +
	`"timestamp":"2026-01-02T03:04:05Z"}`

// geminiThoughtSignatureKey is the ai-sdk-go Part metadata key the Google
// provider reads a function call's thought signature from. Gemini 3 rejects a
// function call in the history without one; Google documents this sentinel
// value for function calls that the model did not itself produce.
const (
	geminiThoughtSignatureKey   = "gemini_thought_signature"
	geminiSkipThoughtValidation = "skip_thought_signature_validator"
)

// recordingMCPServer captures the handlers connectmcp registers, so the
// test can drive the real production handler.
type recordingMCPServer struct {
	handlers map[string]runtime.ToolHandler
}

func (s *recordingMCPServer) AddTool(tool runtime.Tool, h runtime.ToolHandler) {
	if s.handlers == nil {
		s.handlers = map[string]runtime.ToolHandler{}
	}
	s.handlers[tool.Name] = h
}

// wktService is a connect handler for TestService that records the request
// the tool call passes through.
type wktService struct {
	testdataconnect.UnimplementedTestServiceHandler
	got *testdata.ProcessWellKnownTypesRequest
}

func (s *wktService) ProcessWellKnownTypes(_ context.Context, req *testdata.ProcessWellKnownTypesRequest) (*testdata.ProcessWellKnownTypesResponse, error) {
	s.got = req
	return &testdata.ProcessWellKnownTypesResponse{Success: true}, nil
}

func invokeHandler(t *testing.T, h runtime.ToolHandler, args json.RawMessage) *runtime.CallToolResult {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		t.Fatalf("model sent arguments that are not a JSON object: %v (args: %s)", err, args)
	}
	res, err := h(context.Background(), &runtime.CallToolRequest{Arguments: args})
	if err != nil {
		t.Fatalf("handler returned a protocol-level error instead of a tool result: %v", err)
	}
	return res
}

// TestConformance_SelfCorrectsFromToolError checks that when a tool call fails
// on bad arguments, the tool error our handler returns lets the model fix the
// arguments on its next call.
//
// The failing first call is scripted rather than waited for (the real mistake
// is intermittent): the conversation history contains an assistant call with
// the double-encoded payload, followed by the tool error the real generated
// handler returns for it. The live model then continues, and its retry is run
// through the same handler.
func TestConformance_SelfCorrectsFromToolError(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		mcp := &recordingMCPServer{}
		svc := &wktService{}
		registerTestService(t, mcp, svc)
		tool := testTool("TestService", "ProcessWellKnownTypes")
		handler := mcp.handlers[tool.Name]

		// 1. The scripted bad call, and the error our handler returns for it.
		bad := invokeHandler(t, handler, json.RawMessage(doubleEncodedStructArgs))
		if !bad.IsError {
			t.Fatalf("handler accepted the double-encoded payload; nothing to self-correct from")
		}
		t.Logf("tool error sent to the model: %s", bad.Text)

		const callID = "call_selfcorrect_1"
		badCall := llm.NewToolRequestPart(&llm.ToolRequest{
			ID:        callID,
			Name:      tool.Name,
			Arguments: json.RawMessage(doubleEncodedStructArgs),
		})
		badCall.Metadata = map[string]any{geminiThoughtSignatureKey: []byte(geminiSkipThoughtValidation)}

		// 2. Let the live model continue from the failed call.
		resp, err := m.Generate(context.Background(), &llm.Request{
			Messages: []llm.Message{
				llm.NewMessage(llm.RoleUser, llm.NewTextPart(
					"Process a record. Set metadata to a JSON object with exactly these keys: "+
						`environment="production" and replicas=3. Set config to the string "verbose". `+
						"Set timestamp to 2026-01-02T03:04:05Z.")),
				llm.NewMessage(llm.RoleAssistant, badCall),
				llm.NewMessage(llm.RoleUser, llm.NewToolResponsePart(&llm.ToolResponse{
					ID:    callID,
					Name:  tool.Name,
					Error: bad.Text,
				})),
			},
			Tools: []llm.ToolDefinition{{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.RawInputSchema,
			}},
			ToolChoice: &llm.ToolChoice{Type: llm.ToolChoiceRequired},
		})
		if err != nil {
			t.Fatalf("provider rejected the conversation: %v", err)
		}
		reqs := resp.ToolRequests()
		if len(reqs) == 0 {
			t.Fatalf("model did not retry the tool call")
		}
		retry := reqs[0].Arguments

		// 3. The retry must now pass the real handler.
		res := invokeHandler(t, handler, retry)
		if res.IsError {
			t.Fatalf("model did not correct the arguments.\n  retry args: %s\n  tool error: %s", retry, res.Text)
		}
		if _, ok := svc.got.GetMetadata().GetFields()["environment"]; !ok {
			t.Fatalf("retry decoded without metadata.environment (args: %s)", retry)
		}
		t.Logf("corrected retry args: %s", retry)
	})
}
