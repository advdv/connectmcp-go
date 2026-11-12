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
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/connectmcp-go/pkg/runtime"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

// A failure on one field is an *ArgumentsError naming the field as the input
// schema does (its proto name), even when the model sent the JSON name, and
// without protojson's own text, which uses the JSON name.
func TestUnmarshalArguments_ArgumentsError(t *testing.T) {
	for name, args := range map[string]string{
		"proto name": `{"resource_group_id":5}`,
		"JSON name":  `{"resourceGroupId":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			var req testdata.TestValidationRequest
			err := runtime.UnmarshalArguments(mustJSON(t, args), &req)

			argsErr, ok := errors.AsType[*runtime.ArgumentsError](err)
			g.Expect(ok).To(BeTrue(), "error is %T", err)
			g.Expect(argsErr.Field).To(Equal("resource_group_id"))
			g.Expect(argsErr.Description).To(Equal("must be a JSON string, but got the number 5."))
			g.Expect(err.Error()).To(Equal(`invalid tool arguments: field "resource_group_id" ` +
				`must be a JSON string, but got the number 5. Fix this field and call the tool again.`))
			g.Expect(err.Error()).ToNot(ContainSubstring("resourceGroupId"))
			g.Expect(err.Error()).ToNot(ContainSubstring("parser error"))
		})
	}
}

func TestUnmarshalArguments_NotAnObject(t *testing.T) {
	g := NewWithT(t)
	err := runtime.UnmarshalArguments(nil, &testdata.TestValidationRequest{})
	argsErr, ok := errors.AsType[*runtime.ArgumentsError](err)
	g.Expect(ok).To(BeTrue(), "error is %T", err)
	g.Expect(argsErr.Field).To(BeEmpty())
	g.Expect(err.Error()).To(ContainSubstring("must be a JSON object"))
}

// ConnectError reports bad arguments like any other failed call: as
// INVALID_ARGUMENT, with a google.rpc.BadRequest detail when one field is to
// blame.
func TestArgumentsError_ConnectError(t *testing.T) {
	t.Run("one field", func(t *testing.T) {
		g := NewWithT(t)
		argsErr := &runtime.ArgumentsError{Field: "items[2].page_size", Description: `must be an integer, but got the string "ten".`}
		cerr := argsErr.ConnectError()

		g.Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
		g.Expect(cerr.Message()).To(Equal(argsErr.Error()))
		g.Expect(cerr.Details()).To(HaveLen(1))
		detail, err := connectproto.UnmarshalErrorDetail(cerr.Details()[0])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(proto.Equal(detail, &errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{{
				Field:       "items[2].page_size",
				Description: `must be an integer, but got the string "ten".`,
			}},
		})).To(BeTrue(), "detail: %v", detail)

		res, err := runtime.HandleError(cerr)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res.Text).To(MatchJSON(`{
			"code": "INVALID_ARGUMENT",
			"message": "invalid tool arguments: field \"items[2].page_size\" must be an integer, but got the string \"ten\". Fix this field and call the tool again.",
			"details": [{
				"@type": "type.googleapis.com/google.rpc.BadRequest",
				"fieldViolations": [{"field": "items[2].page_size", "description": "must be an integer, but got the string \"ten\"."}]
			}]
		}`))
	})

	t.Run("no single field", func(t *testing.T) {
		g := NewWithT(t)
		cerr := (&runtime.ArgumentsError{Description: "two members of oneof item_type are set"}).ConnectError()
		g.Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
		g.Expect(cerr.Details()).To(BeEmpty())
	})
}
