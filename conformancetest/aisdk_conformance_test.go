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

// Package conformancetest submits the generated MCP tool schemas to real LLM
// providers through ai-sdk-go (the production calling path) and asserts that
// the schema is accepted (no 400) and that the model fills it correctly, which
// round-trips back into the proto through the runtime decode transform.
//
// These tests are nightly and non-blocking. They are gated behind the
// `external_llm` build tag AND per-provider API-key presence, so a normal
// `go test ./...` never runs them.
package conformancetest

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/advdv/connectmcp-go/pkg/runtime"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	"github.com/redpanda-data/ai-sdk-go/llm"
	"github.com/redpanda-data/ai-sdk-go/providers/anthropic"
	"github.com/redpanda-data/ai-sdk-go/providers/google"
	"github.com/redpanda-data/ai-sdk-go/providers/openai"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// provider names a live provider plus a factory that builds an ai-sdk-go model,
// skipping the (sub)test when the provider's API key is absent.
type provider struct {
	name     string
	envKey   string
	newModel func(ctx context.Context, t *testing.T) llm.Model
}

func providersUnderTest() []provider {
	return []provider{
		{
			name:   "openai",
			envKey: "OPENAI_API_KEY",
			newModel: func(_ context.Context, t *testing.T) llm.Model {
				p, err := openai.NewProvider(os.Getenv("OPENAI_API_KEY"))
				if err != nil {
					t.Fatalf("openai provider: %v", err)
				}
				m, err := p.NewModel("gpt-5.5")
				if err != nil {
					t.Fatalf("openai model: %v", err)
				}
				return m
			},
		},
		{
			// Anthropic doubles as Bedrock coverage: Bedrock validates the same
			// Anthropic tool input_schema.
			name:   "anthropic",
			envKey: "ANTHROPIC_API_KEY",
			newModel: func(_ context.Context, t *testing.T) llm.Model {
				p, err := anthropic.NewProvider(os.Getenv("ANTHROPIC_API_KEY"))
				if err != nil {
					t.Fatalf("anthropic provider: %v", err)
				}
				m, err := p.NewModel("claude-sonnet-4-6")
				if err != nil {
					t.Fatalf("anthropic model: %v", err)
				}
				return m
			},
		},
		{
			name:   "gemini",
			envKey: "GOOGLE_API_KEY",
			newModel: func(ctx context.Context, t *testing.T) llm.Model {
				p, err := google.NewProvider(ctx, os.Getenv("GOOGLE_API_KEY"))
				if err != nil {
					t.Fatalf("google provider: %v", err)
				}
				m, err := p.NewModel("gemini-3.5-flash")
				if err != nil {
					t.Fatalf("google model: %v", err)
				}
				return m
			},
		},
	}
}

// runOnAllProviders runs fn as a subtest per provider, skipping providers whose
// key is unset.
func runOnAllProviders(t *testing.T, fn func(t *testing.T, m llm.Model)) {
	t.Helper()
	ctx := context.Background()
	for _, p := range providersUnderTest() {
		p := p
		t.Run(p.name, func(t *testing.T) {
			if os.Getenv(p.envKey) == "" {
				t.Skipf("%s not set", p.envKey)
			}
			fn(t, p.newModel(ctx, t))
		})
	}
}

// callTool submits the tool to the model with ToolChoice=required. A nil error
// proves the provider accepted the input_schema (the original bug surfaced as a
// 400 here). It returns the raw arguments of the first tool call.
func callTool(t *testing.T, m llm.Model, tool runtime.Tool, prompt string) json.RawMessage {
	t.Helper()
	resp, err := m.Generate(context.Background(), &llm.Request{
		Messages: []llm.Message{llm.NewMessage(llm.RoleUser, llm.NewTextPart(prompt))},
		Tools: []llm.ToolDefinition{{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.RawInputSchema,
		}},
		ToolChoice: &llm.ToolChoice{Type: llm.ToolChoiceRequired},
	})
	if err != nil {
		t.Fatalf("provider rejected schema or call failed: %v", err)
	}
	reqs := resp.ToolRequests()
	if len(reqs) == 0 {
		t.Fatalf("no tool call returned")
	}
	if reqs[0].Name != tool.Name {
		t.Fatalf("called %q, want %q", reqs[0].Name, tool.Name)
	}
	return reqs[0].Arguments
}

// decodeInto runs the production decode pipeline on the model's arguments.
func decodeInto(t *testing.T, args json.RawMessage, msg proto.Message) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if err := runtime.DecodeArguments(msg.ProtoReflect().Descriptor(), m); err != nil {
		t.Fatalf("DecodeArguments: %v (args: %s)", err, args)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, msg); err != nil {
		t.Fatalf("protojson: %v (decoded: %s)", err, b)
	}
}

// TestConformance_Basic is a sanity check on a tool with no oneof.
func TestConformance_Basic(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("TestService", "GetItem"), "Get the item with ID 'item-123'.")
		var req testdata.GetItemRequest
		decodeInto(t, args, &req)
		if req.GetId() != "item-123" {
			t.Fatalf("id = %q, want item-123", req.GetId())
		}
	})
}

// TestConformance_Oneof is the core regression: a tool whose input has a oneof.
// Before the fix this schema was rejected (HTTP 400) by OpenAI/Anthropic. The
// discriminated-object rendering must be accepted and correctly filled.
func TestConformance_Oneof(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("EdgeCaseService", "MultipleOneofs"),
			"Create a job named 'export'. Its source should be the URL https://example.com/data, and the output format should be JSON.")
		var req testdata.MultipleOneofsRequest
		decodeInto(t, args, &req)
		if req.GetName() == "" {
			t.Fatalf("name not filled: %+v", &req)
		}
		if _, ok := req.GetSource().(*testdata.MultipleOneofsRequest_Url); !ok {
			t.Fatalf("source oneof not set to url: %#v", req.GetSource())
		}
	})
}

// TestConformance_MessageOneof exercises a oneof whose member is a message.
func TestConformance_MessageOneof(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("TestService", "CreateItem"),
			"Create an item named 'Widget'. It is a product priced at 9.99 with quantity 5.")
		var req testdata.CreateItemRequest
		decodeInto(t, args, &req)
		if req.GetName() == "" {
			t.Fatalf("name not filled")
		}
		if req.GetProduct() == nil {
			t.Fatalf("item_type oneof not set to product: %#v", req.GetItemType())
		}
	})
}

// TestConformance_RecursiveOneof exercises a recursive message nested inside a
// oneof (the discriminated wrapper plus recursion-depth placeholders).
func TestConformance_RecursiveOneof(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("EdgeCaseService", "OneofRecursive"),
			"Build a tree node whose value is 'root' with a single child whose value is 'child'.")
		var req testdata.OneofRecursiveRequest
		decodeInto(t, args, &req)
		if req.GetNode() == nil {
			t.Fatalf("node oneof not set: %+v", &req)
		}
	})
}

// TestConformance_WellKnownTypes exercises dynamic well-known types
// (google.protobuf.Struct and google.protobuf.Value). OpenAI and Anthropic both
// reject the open-ended native schema, so the ai-sdk-go adapters downgrade those
// nodes to a JSON-encoded string; the runtime decode then parses them back. This
// proves that round-trip works on every provider, including the Gemini path
// where the schema is not downgraded.
func TestConformance_WellKnownTypes(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("TestService", "ProcessWellKnownTypes"),
			"Process a record. Set metadata to a JSON object with exactly these keys: "+
				`environment="production" and replicas=3. Set config to the string "verbose". `+
				"Set timestamp to 2026-01-02T03:04:05Z.")
		var req testdata.ProcessWellKnownTypesRequest
		decodeInto(t, args, &req)

		md := req.GetMetadata()
		if md == nil || md.GetFields() == nil {
			t.Fatalf("metadata Struct not decoded (stringified WKT lift failed?): %+v", &req)
		}
		if _, ok := md.GetFields()["environment"]; !ok {
			t.Fatalf("metadata missing 'environment' key; got %v", md.GetFields())
		}
		if req.GetConfig() == nil {
			t.Fatalf("config Value not decoded: %+v", &req)
		}
	})
}

// TestConformance_ValidationRules checks that what (buf.validate) rules add to
// a schema reaches the model. The prompt gives a bare workspace ID and plain
// words for the priority; the "workspaces/" prefix of the parent is only in
// the schema, as a predefined rule in the description and as an example.
func TestConformance_ValidationRules(t *testing.T) {
	runOnAllProviders(t, func(t *testing.T, m llm.Model) {
		args := callTool(t, m, testTool("ReservationService", "CreateReservation"),
			"Reserve 8 seats for 'Quarterly planning' in the workspace with ID "+
				"01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d, from 2030-01-01T10:00:00Z to 2030-01-01T11:00:00Z, "+
				"with high priority. Invite a@example.com and b@example.com.")
		var req testdata.CreateReservationRequest
		decodeInto(t, args, &req)

		if got, want := req.GetParent(), "workspaces/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d"; got != want {
			t.Errorf("parent = %q, want %q (args: %s)", got, want, args)
		}
		if req.GetTitle() != "Quarterly planning" || req.GetSeats() != 8 {
			t.Errorf("title = %q, seats = %d (args: %s)", req.GetTitle(), req.GetSeats(), args)
		}
		if req.GetPriority() != testdata.Urgency_URGENCY_HIGH {
			t.Errorf("priority = %v, want URGENCY_HIGH (args: %s)", req.GetPriority(), args)
		}
		if len(req.GetAttendees()) != 2 {
			t.Errorf("attendees = %v, want two (args: %s)", req.GetAttendees(), args)
		}
		if !req.GetEndTime().AsTime().After(req.GetStartTime().AsTime()) {
			t.Errorf("end_time is not after start_time (args: %s)", args)
		}
	})
}

// TestConformance_ValidationRuleSchemasAccepted submits a schema with every
// kind of (buf.validate) rule to each provider: const, enum, examples,
// formats, bounds, the rules of items, keys and values, and descriptions. A
// provider that rejects a keyword fails the request.
func TestConformance_ValidationRuleSchemasAccepted(t *testing.T) {
	for _, method := range []string{
		"Strings", "Numerics", "Scalars", "Enums", "Collections", "WellKnowns", "Cels", "Modifiers",
	} {
		t.Run(method, func(t *testing.T) {
			runOnAllProviders(t, func(t *testing.T, m llm.Model) {
				tool := testTool("RuleCasesService", method)
				tool.Description = "Records the given values."
				callTool(t, m, tool, "Call the tool once. Fill in a few fields with values that satisfy the schema.")
			})
		})
	}
}
