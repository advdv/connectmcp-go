package connectmcp

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/connectmcp-go/pkg/gen"
	"github.com/advdv/connectmcp-go/pkg/runtime"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	"github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata/testdataconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This file is the test wiring that the protoc plugin and its generated code
// used to provide to the behavioral tests in this package, now backed by the
// runtime implementation.

// registerTestService registers srv on a fresh connect server and exposes its
// annotated methods as tools on s. srv may be a service implementation or a
// generated connect client, which forwards.
func registerTestService(t *testing.T, s runtime.MCPServer, srv testdataconnect.TestServiceHandler, opts ...Option) {
	t.Helper()
	server := connect.NewServer()
	testdataconnect.RegisterTestServiceHandler(server, srv)
	if err := Register(s, server, opts...); err != nil {
		t.Fatalf("register tools: %v", err)
	}
}

// testServiceTool returns the tool definition of a TestService method.
func testServiceTool(method string) runtime.Tool {
	md := testdata.File_testdata_test_service_proto.Services().ByName("TestService").Methods().ByName(protoreflect.Name(method))
	description, _ := gen.ConfiguredToolDescription(md)
	return gen.ToolForMethod(md, description)
}

// FileGenerator gives the schema tests their original entry points into the
// schema generation in pkg/gen.
type FileGenerator struct{}

func (*FileGenerator) messageSchema(md protoreflect.MessageDescriptor) map[string]any {
	return gen.MessageSchema(md, gen.SchemaOptions{})
}

func (*FileGenerator) getType(fd protoreflect.FieldDescriptor) map[string]any {
	return gen.FieldSchema(fd, gen.SchemaOptions{})
}

var (
	kindToType          = gen.KindToType
	cleanComment        = gen.CleanComment
	MangleHeadIfTooLong = gen.MangleHeadIfTooLong
	Base36String        = gen.Base36String
)
