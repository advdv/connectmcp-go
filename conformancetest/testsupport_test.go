//go:build external_llm

package conformancetest

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/protoc-gen-go-mcp/pkg/connectmcp"
	"github.com/advdv/protoc-gen-go-mcp/pkg/gen"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// registerTestService registers srv on a fresh connect server and exposes its
// annotated methods as tools on s.
func registerTestService(t *testing.T, s runtime.MCPServer, srv testdataconnect.TestServiceHandler) {
	t.Helper()
	server := connect.NewServer()
	testdataconnect.RegisterTestServiceHandler(server, srv)
	if err := connectmcp.Register(s, server); err != nil {
		t.Fatalf("register tools: %v", err)
	}
}

// testTool returns the tool definition connectmcp serves for a method of one
// of the test services.
func testTool(service, method string) runtime.Tool {
	md := testdata.File_testdata_test_service_proto.Services().ByName(protoreflect.Name(service))
	if md == nil {
		md = testdata.File_testdata_edge_cases_proto.Services().ByName(protoreflect.Name(service))
	}
	m := md.Methods().ByName(protoreflect.Name(method))
	description, _ := gen.ConfiguredToolDescription(m)
	return gen.ToolForMethod(m, description)
}
