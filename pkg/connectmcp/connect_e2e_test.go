package connectmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/protoc-gen-go-mcp/internal/mcptest"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// A generated connect-go v2 client has the handler's method signatures, so it
// can be registered as the handler of a connect server, which then forwards.
var _ testdataconnect.TestServiceHandler = testdataconnect.TestServiceClient(nil)

// connectTestService is a connect-go v2 handler implementation.
type connectTestService struct {
	testdataconnect.UnimplementedTestServiceHandler
}

func (*connectTestService) CreateItem(_ context.Context, req *testdata.CreateItemRequest) (*testdata.CreateItemResponse, error) {
	return &testdata.CreateItemResponse{Id: "created-" + req.GetName()}, nil
}

func (*connectTestService) GetItem(_ context.Context, req *testdata.GetItemRequest) (*testdata.GetItemResponse, error) {
	detail, err := connectproto.NewErrorDetail(&errdetails.ResourceInfo{
		ResourceType: "item",
		ResourceName: req.GetId(),
	})
	if err != nil {
		return nil, err
	}
	return nil, connect.NewError(connect.CodeNotFound, "no such item").WithDetail(detail)
}

// trustUpstreamErrors is the README's interceptor for a forwarding server: it
// re-authors a remote connect error as the server's own, so its code, message
// and details reach the client instead of a bare INTERNAL.
func trustUpstreamErrors(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		if cerr, ok := errors.AsType[*connect.Error](err); ok && cerr != nil && cerr.IsRemote() {
			local := connect.NewError(cerr.Code(), cerr.Message()).WithCause(err)
			for _, detail := range cerr.Details() {
				local = local.WithDetail(detail)
			}
			return local
		}
		return err
	}
}

// TestConnectV2_E2E drives MCP tool calls through a generated connect-go v2
// client into a connect server, over both the in-process transport and HTTP.
func TestConnectV2_E2E(t *testing.T) {
	var procedures []string
	record := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			procedures = append(procedures, spec.Procedure)
			return next(ctx, spec, stream)
		}
	}

	server := connect.NewServer(record)
	testdataconnect.RegisterTestServiceHandler(server, &connectTestService{})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	for name, transport := range map[string]connect.Transport{
		"in-process": connectinprocess.New(server),
		"http":       connecthttp.NewTransport(httpServer.Client(), httpServer.URL),
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			procedures = nil

			client := testdataconnect.NewTestServiceClient(connect.NewClient(transport))
			mcpServer := mcptest.NewServer(t)
			registerTestService(t, mcpServer, client)

			// Success: the response message comes back as structured content.
			created := callTool(t, mcpServer, "testdata_TestService_CreateItem", map[string]any{"name": "Widget"})
			g.Expect(created["isError"]).To(BeNil())
			g.Expect(created["structuredContent"]).To(Equal(map[string]any{"id": "created-Widget"}))

			// Failure: the upstream's error is remote to the forwarding server,
			// so, as over connect-go's own transports, the model only learns
			// that the call failed.
			failed := callTool(t, mcpServer, "testdata_TestService_GetItem", map[string]any{"id": "item-1"})
			g.Expect(failed["isError"]).To(Equal(true))
			var toolErr map[string]any
			g.Expect(json.Unmarshal([]byte(firstTextContent(t, failed)), &toolErr)).To(Succeed())
			g.Expect(toolErr).To(Equal(map[string]any{"code": "INTERNAL"}))

			// A forwarding server that re-authors upstream errors as its own
			// (the README recipe) passes their code, message and details on.
			trusting := mcptest.NewServer(t)
			forwarder := connect.NewServer(trustUpstreamErrors)
			testdataconnect.RegisterTestServiceHandler(forwarder, client)
			g.Expect(Register(trusting, forwarder)).To(Succeed())

			failed = callTool(t, trusting, "testdata_TestService_GetItem", map[string]any{"id": "item-1"})
			g.Expect(failed["isError"]).To(Equal(true))
			toolErr = nil
			g.Expect(json.Unmarshal([]byte(firstTextContent(t, failed)), &toolErr)).To(Succeed())
			g.Expect(toolErr).To(Equal(map[string]any{
				"code":    "NOT_FOUND",
				"message": "no such item",
				"details": []any{map[string]any{
					"@type":        "type.googleapis.com/google.rpc.ResourceInfo",
					"resourceType": "item",
					"resourceName": "item-1",
				}},
			}))

			// Unimplemented methods are reported, not dropped.
			unimplemented := callTool(t, trusting, "testdata_TestService_TestValidation", map[string]any{})
			g.Expect(unimplemented["isError"]).To(Equal(true))
			g.Expect(firstTextContent(t, unimplemented)).To(ContainSubstring(`"code":"UNIMPLEMENTED"`))

			// Every tool call went through the connect server's interceptors.
			g.Expect(procedures).To(Equal([]string{
				"/testdata.TestService/CreateItem",
				"/testdata.TestService/GetItem",
				"/testdata.TestService/GetItem",
				"/testdata.TestService/TestValidation",
			}))
		})
	}
}
