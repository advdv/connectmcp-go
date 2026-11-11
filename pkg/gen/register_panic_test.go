package gen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime/mark3labs"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestRegisterService_PanicOnNilNewMessage demonstrates that if the
// user-provided NewMessage function returns nil for a given descriptor,
// the tool handler panics inside protojson.Unmarshal instead of returning
// an error. RegisterService should guard against this.
func TestRegister_PanicOnNilNewMessage(t *testing.T) {
	file := (&testdata.CreateItemRequest{}).ProtoReflect().Descriptor().ParentFile()
	sd := file.Services().ByName("TestService")
	if sd == nil {
		t.Fatal("TestService descriptor not found")
	}

	handler := func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		return newTestMessage(method.Output()), nil
	}

	// NewMessage that returns nil -- simulates a partial implementation
	// that doesn't know about a particular message type.
	nilNewMessage := func(md protoreflect.MessageDescriptor) proto.Message {
		return nil
	}

	server := mcpserver.NewMCPServer("test", "1.0")
	RegisterService(mark3labs.Wrap(server), sd, handler, RegisterServiceOptions{
		NewMessage: nilNewMessage,
	})

	// After fix: should return an error response, not panic.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		_ = server.HandleMessage(context.Background(), json.RawMessage(`{
			"jsonrpc": "2.0",
			"id": 1,
			"method": "tools/call",
			"params": {
				"name": "testdata_TestService_GetItem",
				"arguments": {"id": "test-123"}
			}
		}`))
	}()

	if didPanic {
		t.Error("Nil NewMessage return should produce an error, not a panic")
	}
}

// TestRegisterService_PanicOnNilArguments_WithExtraProperties verifies
// that when GetArguments() returns nil (e.g., arguments not provided as
// a map) and ExtraProperties are configured, the iteration over
// ExtraProperties doesn't panic when indexing into the nil map.
//
// Reading from a nil map is safe in Go, so this test documents that the
// nil arguments path does NOT panic but does produce a protojson error
// (because json.Marshal(nil) yields "null" which protojson rejects).
func TestRegister_PanicOnNilArguments_WithExtraProperties(t *testing.T) {
	file := (&testdata.CreateItemRequest{}).ProtoReflect().Descriptor().ParentFile()
	sd := file.Services().ByName("TestService")
	if sd == nil {
		t.Fatal("TestService descriptor not found")
	}

	type apiKeyCtx struct{}
	handlerCalled := false

	handler := func(ctx context.Context, method protoreflect.MethodDescriptor, req proto.Message) (proto.Message, error) {
		handlerCalled = true
		return newTestMessage(method.Output()), nil
	}

	server := mcpserver.NewMCPServer("test", "1.0")
	RegisterService(mark3labs.Wrap(server), sd, handler, RegisterServiceOptions{
		NewMessage: newTestMessage,
		ExtraProperties: []runtime.ExtraProperty{
			{
				Name:        "api_key",
				Description: "API key",
				Required:    true,
				ContextKey:  apiKeyCtx{},
			},
		},
	})

	// Send a tool call where arguments is a string (not a map),
	// causing GetArguments() to return nil.
	result := server.HandleMessage(context.Background(), json.RawMessage(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {
			"name": "testdata_TestService_GetItem",
			"arguments": "not-a-map"
		}
	}`))

	// The handler should NOT have been called: a nil arguments map is rejected
	// before unmarshaling.
	if handlerCalled {
		t.Error("Handler should not have been called with nil arguments")
	}

	// Verify we got an error response, not a success.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(resultBytes, &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	// Bad arguments come back as a tool error result (isError: true) the
	// model can read and correct, not as a JSON-RPC protocol error.
	toolResult, _ := resp["result"].(map[string]any)
	if isError, _ := toolResult["isError"].(bool); !isError {
		t.Errorf("Expected a tool error result for nil arguments, got: %s", string(resultBytes))
	}
	if !strings.Contains(string(resultBytes), "must be a JSON object") {
		t.Errorf("Expected a model-readable explanation, got: %s", string(resultBytes))
	}
}
