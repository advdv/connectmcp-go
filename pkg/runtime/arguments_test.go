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

package runtime_test

import (
	"strings"
	"testing"

	"github.com/redpanda-data/protoc-gen-go-mcp/pkg/runtime"
	testdata "github.com/redpanda-data/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"google.golang.org/protobuf/proto"
)

// DoubleEncodedStructArgs is the exact payload Claude sent in a failing live
// conformance run: the Struct field "metadata" (downgraded to a string by the
// client) was JSON-encoded twice.
const doubleEncodedStructArgs = `{"config":"\"verbose\"",` +
	`"metadata":"\"{\\\"environment\\\": \\\"production\\\", \\\"replicas\\\": 3}\"",` +
	`"timestamp":"2026-01-02T03:04:05Z"}`

// decodeAndUnmarshal runs the full production argument pipeline.
func decodeAndUnmarshal(t *testing.T, msg proto.Message, args string) error {
	t.Helper()
	m := mustJSON(t, args)
	if err := runtime.DecodeArguments(msg.ProtoReflect().Descriptor(), m); err != nil {
		return err
	}
	return runtime.UnmarshalArguments(m, msg)
}

func expectErrorContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%s", want, err)
		}
	}
}

func TestUnmarshalArguments_Valid(t *testing.T) {
	var req testdata.ProcessWellKnownTypesRequest
	err := decodeAndUnmarshal(t, &req, `{"metadata":{"environment":"production"},"timestamp":"2026-01-02T03:04:05Z"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.GetMetadata().GetFields()["environment"].GetStringValue() != "production" {
		t.Fatalf("metadata not decoded: %v", req.GetMetadata())
	}
}

func TestUnmarshalArguments_DoubleEncodedStruct(t *testing.T) {
	var req testdata.ProcessWellKnownTypesRequest
	err := decodeAndUnmarshal(t, &req, doubleEncodedStructArgs)
	expectErrorContains(t, err,
		`field "metadata" must be a JSON object (google.protobuf.Struct)`,
		`but got the string "{\"environment\"`,
		"The string contains JSON text",
		"exactly once, not twice",
		"call the tool again",
	)
}

func TestUnmarshalArguments_BadTimestamp(t *testing.T) {
	var req testdata.ProcessWellKnownTypesRequest
	err := decodeAndUnmarshal(t, &req, `{"timestamp":"yesterday"}`)
	expectErrorContains(t, err,
		`field "timestamp" must be an RFC 3339 timestamp string`,
		`but got the string "yesterday"`,
	)
	if strings.Contains(err.Error(), "contains JSON text") {
		t.Errorf("unexpected JSON-text hint for a plain string: %s", err)
	}
}

func TestUnmarshalArguments_NestedFieldPath(t *testing.T) {
	var req testdata.DeepNestingRequest
	err := decodeAndUnmarshal(t, &req, `{"middle":{"inner":{"id":"a","metadata":"oops"}}}`)
	expectErrorContains(t, err,
		`field "middle.inner.metadata" must be a JSON object (google.protobuf.Struct)`,
		`but got the string "oops"`,
	)
}

func TestUnmarshalArguments_ListElementPath(t *testing.T) {
	var req testdata.DeepNestingRequest
	err := decodeAndUnmarshal(t, &req, `{"middles":[{"inner":{"id":"a"}},{"inner":{"id":5}}]}`)
	expectErrorContains(t, err,
		`field "middles[1].inner.id" must be a JSON string`,
		"but got the number 5",
	)
}

func TestUnmarshalArguments_ListNotArray(t *testing.T) {
	var req testdata.DeepNestingRequest
	err := decodeAndUnmarshal(t, &req, `{"middles":"[{\"inner\":{\"id\":\"a\"}}]"}`)
	expectErrorContains(t, err,
		`field "middles" must be a JSON array`,
		"The string contains JSON text",
	)
}

// An unknown enum name is silently discarded (DiscardUnknown), but a value of
// the wrong JSON type is an error that lists the valid names.
func TestUnmarshalArguments_BadEnum(t *testing.T) {
	var req testdata.EnumFieldsRequest
	err := decodeAndUnmarshal(t, &req, `{"priority":true}`)
	expectErrorContains(t, err,
		`field "priority" must be one of "PRIORITY_UNSPECIFIED", "PRIORITY_LOW"`,
		`"PRIORITY_CRITICAL"`,
		"but got the boolean true",
	)
}

// When no single field is to blame (here: two members of one oneof), the
// error still reaches the model with the parser's explanation.
func TestUnmarshalArguments_NoSingleFieldToBlame(t *testing.T) {
	var req testdata.CreateItemRequest
	err := runtime.UnmarshalArguments(mustJSON(t, `{"name":"n","product":{},"service":{}}`), &req)
	expectErrorContains(t, err,
		"invalid tool arguments:",
		"item_type",
		"call the tool again",
	)
	if strings.Contains(err.Error(), "field \"") {
		t.Errorf("no single field should be named: %s", err)
	}
}
