package connectmcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/protoc-gen-go-mcp/pkg/connectmcp"
	"github.com/advdv/protoc-gen-go-mcp/pkg/mcpv1"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime/gosdk"
	testdata "github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata"
	"github.com/advdv/protoc-gen-go-mcp/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	createItemTool = "testdata_TestService_CreateItem"
	getItemTool    = "testdata_TestService_GetItem"
)

// service implements TestService, whose methods all carry the
// (mcp.v1.tool_description) option in test_service.proto.
type service struct {
	testdataconnect.UnimplementedTestServiceHandler
	calls int
}

func (s *service) CreateItem(_ context.Context, req *testdata.CreateItemRequest) (*testdata.CreateItemResponse, error) {
	s.calls++
	return &testdata.CreateItemResponse{Id: "created-" + req.GetName()}, nil
}

// authorize is a header-based auth interceptor as one would write for connect.
func authorize(seen *[]*connect.CallInfo) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			*seen = append(*seen, info)
			if info.RequestHeader().Get("Authorization") != "Bearer secret" {
				return connect.NewError(connect.CodeUnauthenticated, "missing or wrong token")
			}
			return next(ctx, spec, stream)
		}
	}
}

type headerRoundTripper struct{ header http.Header }

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for key, values := range rt.header {
		req.Header[key] = values
	}
	return http.DefaultTransport.RoundTrip(req)
}

func text(res *mcp.CallToolResult) string {
	return res.Content[0].(*mcp.TextContent).Text
}

// TestNewHandler serves a connect server over MCP's streamable HTTP transport,
// in the default and in the stateless JSON configuration, and checks that the
// annotated methods are tools and that tool calls run through the interceptors
// with the HTTP request's headers.
func TestNewHandler(t *testing.T) {
	for name, opts := range map[string][]connectmcp.Option{
		"default": nil,
		"stateless json": {
			connectmcp.WithStreamableHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []*connect.CallInfo
			svc := &service{}
			server := connect.NewServer(authorize(&seen))
			testdataconnect.RegisterTestServiceHandler(server, svc)
			// ToolNamingService has no annotated methods: it must not be exposed.
			testdataconnect.RegisterToolNamingServiceHandler(server, testdataconnect.UnimplementedToolNamingServiceHandler{})

			handler, err := connectmcp.NewHandler(server, append(opts, connectmcp.WithServerInfo("items", "1.2.3"))...)
			if err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)

			connectSession := func(t *testing.T, header http.Header) *mcp.ClientSession {
				t.Helper()
				client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
				session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
					Endpoint:   httpServer.URL,
					HTTPClient: &http.Client{Transport: headerRoundTripper{header}},
				}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				return session
			}
			createItem := &mcp.CallToolParams{Name: createItemTool, Arguments: map[string]any{"name": "Widget"}}

			t.Run("authorized", func(t *testing.T) {
				g := NewWithT(t)
				seen = nil
				session := connectSession(t, http.Header{"Authorization": {"Bearer secret"}})

				g.Expect(session.InitializeResult().ServerInfo.Name).To(Equal("items"))
				g.Expect(session.InitializeResult().ServerInfo.Version).To(Equal("1.2.3"))

				// Exactly the annotated methods are tools, described by the option.
				listed, err := session.ListTools(t.Context(), nil)
				g.Expect(err).ToNot(HaveOccurred())
				descriptions := map[string]string{}
				for _, tool := range listed.Tools {
					descriptions[tool.Name] = tool.Description
					g.Expect(tool.InputSchema).ToNot(BeNil())
					g.Expect(tool.OutputSchema).ToNot(BeNil())
				}
				g.Expect(descriptions).To(Equal(map[string]string{
					createItemTool: "CreateItem creates a new item",
					getItemTool:    "GetItem retrieves an item by ID",
					"testdata_TestService_ProcessWellKnownTypes": "Test well-known types handling",
					"testdata_TestService_TestValidation":        "Test protovalidate constraints",
				}))

				res, err := session.CallTool(t.Context(), createItem)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(res.IsError).To(BeFalse(), text(res))
				g.Expect(res.StructuredContent).To(Equal(map[string]any{"id": "created-Widget"}))

				g.Expect(seen).To(HaveLen(1))
				g.Expect(seen[0].Protocol).To(Equal(connectmcp.Protocol))
				g.Expect(seen[0].Spec.Procedure).To(Equal("/testdata.TestService/CreateItem"))

				// A connect error reaches the model as a structured tool error.
				res, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: getItemTool, Arguments: map[string]any{"id": "1"}})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(res.IsError).To(BeTrue())
				g.Expect(text(res)).To(ContainSubstring(`"code":"UNIMPLEMENTED"`))
			})

			t.Run("unauthorized", func(t *testing.T) {
				g := NewWithT(t)
				calls := svc.calls
				session := connectSession(t, nil)

				res, err := session.CallTool(t.Context(), createItem)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(res.IsError).To(BeTrue())
				g.Expect(text(res)).To(MatchJSON(`{"code":"UNAUTHENTICATED","message":"missing or wrong token"}`))
				g.Expect(svc.calls).To(Equal(calls), "the interceptor must stop the call")
			})
		})
	}
}

// inMemorySession connects a client to mcpServer over an in-memory transport.
func inMemorySession(t *testing.T, mcpServer *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientT, serverT := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(t.Context(), serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil).Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = serverSession.Wait()
	})
	return session
}

// toolError is the JSON every failed tool call returns, see runtime.HandleError.
type toolError struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details []map[string]any `json:"details"`
}

func parseToolError(g *WithT, res *mcp.CallToolResult) toolError {
	g.Expect(res.IsError).To(BeTrue())
	var out toolError
	g.Expect(json.Unmarshal([]byte(text(res)), &out)).To(Succeed(), "tool error is not JSON: %s", text(res))
	return out
}

// TestRegister_BadArguments checks that undecodable arguments are reported as
// an INVALID_ARGUMENT tool error without reaching the service, also when an
// interceptor rewrites the error.
func TestRegister_BadArguments(t *testing.T) {
	svc := &service{}
	server := connect.NewServer(func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if err := next(ctx, spec, stream); err != nil {
				return connect.NewError(connect.CodeInternal, "redacted")
			}
			return nil
		}
	})
	testdataconnect.RegisterTestServiceHandler(server, svc)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	if err := connectmcp.Register(gosdk.Wrap(mcpServer), server); err != nil {
		t.Fatal(err)
	}
	session := inMemorySession(t, mcpServer)

	for name, args := range map[string]any{
		"wrong field type":          map[string]any{"name": 42},
		"malformed oneof":           map[string]any{"name": "x", "item_type": map[string]any{"which": "product", "service": map[string]any{}}},
		"arguments not JSON object": []any{"name"},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: createItemTool, Arguments: args})
			g.Expect(err).ToNot(HaveOccurred())
			toolErr := parseToolError(g, res)
			g.Expect(toolErr.Code).To(Equal("INVALID_ARGUMENT"))
			g.Expect(toolErr.Message).To(HavePrefix("invalid tool arguments: "))
			g.Expect(toolErr.Message).To(ContainSubstring("call the tool again"))
			g.Expect(toolErr.Message).ToNot(ContainSubstring("redacted"))
			g.Expect(svc.calls).To(BeZero())
		})
	}

	t.Run("names the field in a BadRequest detail", func(t *testing.T) {
		g := NewWithT(t)
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: createItemTool, Arguments: map[string]any{"name": 42}})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(text(res)).To(MatchJSON(`{
			"code": "INVALID_ARGUMENT",
			"message": "invalid tool arguments: field \"name\" must be a JSON string, but got the number 42. Fix this field and call the tool again.",
			"details": [{
				"@type": "type.googleapis.com/google.rpc.BadRequest",
				"fieldViolations": [{"field": "name", "description": "must be a JSON string, but got the number 42."}]
			}]
		}`))
	})
}

// validatingService rejects a negative age the way a validation interceptor
// (such as protovalidate's) would: INVALID_ARGUMENT, with a detail.
type validatingService struct {
	testdataconnect.UnimplementedTestServiceHandler
}

func (validatingService) TestValidation(_ context.Context, req *testdata.TestValidationRequest) (*testdata.TestValidationResponse, error) {
	if req.GetAge() < 0 {
		detail, err := connectproto.NewErrorDetail(&errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "age", Description: "must be greater than or equal to 0"}},
		})
		if err != nil {
			return nil, err
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, "validation error: age: must be greater than or equal to 0").WithDetail(detail)
	}
	return &testdata.TestValidationResponse{}, nil
}

// TestRegister_ArgumentErrorsLikeRPCErrors checks that a tool call rejected
// while decoding its arguments and one rejected by the RPC come back in one
// shape, and that the interceptors see the error the client is sent.
func TestRegister_ArgumentErrorsLikeRPCErrors(t *testing.T) {
	var intercepted error
	server := connect.NewServer(func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			intercepted = next(ctx, spec, stream)
			return intercepted
		}
	})
	testdataconnect.RegisterTestServiceHandler(server, validatingService{})

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	if err := connectmcp.Register(gosdk.Wrap(mcpServer), server); err != nil {
		t.Fatal(err)
	}
	session := inMemorySession(t, mcpServer)
	const tool = "testdata_TestService_TestValidation"

	for name, tc := range map[string]struct {
		args          map[string]any
		field         string
		inMessage     string
		notInMessage  string
		fromValidator bool
	}{
		"fails validation in the RPC": {
			args: map[string]any{"age": -1}, field: "age", inMessage: "validation error", fromValidator: true,
		},
		"fails decoding": {
			args: map[string]any{"age": "ten"}, field: "age", inMessage: `must be an integer, but got the string "ten"`,
		},
		// The model may send protojson's name for a field; the error still
		// uses the name the input schema showed it.
		"fails decoding, sent by JSON name": {
			args: map[string]any{"resourceGroupId": 5}, field: "resource_group_id",
			inMessage: `field "resource_group_id"`, notInMessage: "resourceGroupId",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			intercepted = nil
			res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: tc.args})
			g.Expect(err).ToNot(HaveOccurred())

			toolErr := parseToolError(g, res)
			g.Expect(toolErr.Code).To(Equal("INVALID_ARGUMENT"))
			g.Expect(toolErr.Message).To(ContainSubstring(tc.inMessage))
			if tc.notInMessage != "" {
				g.Expect(text(res)).ToNot(ContainSubstring(tc.notInMessage))
			}
			g.Expect(toolErr.Details).To(HaveLen(1))
			g.Expect(toolErr.Details[0]).To(HaveKeyWithValue("@type", "type.googleapis.com/google.rpc.BadRequest"))
			g.Expect(toolErr.Details[0]["fieldViolations"]).To(ConsistOf(HaveKeyWithValue("field", tc.field)))

			// What the interceptor saw is what the client got.
			cerr, ok := errors.AsType[*connect.Error](intercepted)
			g.Expect(ok).To(BeTrue(), "interceptor saw %v", intercepted)
			g.Expect(cerr.Code()).To(Equal(connect.CodeInvalidArgument))
			g.Expect(cerr.Message()).To(Equal(toolErr.Message))
			g.Expect(cerr.Details()).To(HaveLen(len(toolErr.Details)))
		})
	}
}

// createItemWith returns the descriptor of TestService.CreateItem with its
// method options changed by mutate.
func createItemWith(t *testing.T, mutate func(*descriptorpb.MethodOptions)) protoreflect.MethodDescriptor {
	t.Helper()
	fdp := protodesc.ToFileDescriptorProto(testdata.File_testdata_test_service_proto)
	for _, svc := range fdp.GetService() {
		for _, method := range svc.GetMethod() {
			if svc.GetName() == "TestService" && method.GetName() == "CreateItem" {
				if method.Options == nil {
					method.Options = &descriptorpb.MethodOptions{}
				}
				mutate(method.Options)
			}
		}
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Services().ByName("TestService").Methods().ByName("CreateItem")
}

func TestRegister_Errors(t *testing.T) {
	createItem := testdata.File_testdata_test_service_proto.Services().ByName("TestService").Methods().ByName("CreateItem")
	method := func(procedure string, streamType connect.StreamType, schema any) connect.Method {
		return connect.Method{
			Spec:    connect.Spec{Procedure: procedure, StreamType: streamType, Schema: schema},
			Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
		}
	}

	for name, tc := range map[string]struct {
		methods []connect.Method
		want    string
	}{
		"nothing registered": {
			want: "no method registered on the connect server carries the (mcp.v1.tool_description) option",
		},
		"no annotated method": {
			methods: []connect.Method{
				method("/a.B/C", connect.StreamTypeUnary, createItemWith(t, func(o *descriptorpb.MethodOptions) {
					proto.ClearExtension(o, mcpv1.E_ToolDescription)
				})),
				method("/a.B/NoSchema", connect.StreamTypeUnary, nil),
			},
			want: "no method registered on the connect server carries the (mcp.v1.tool_description) option",
		},
		"empty description": {
			methods: []connect.Method{
				method("/a.B/C", connect.StreamTypeUnary, createItemWith(t, func(o *descriptorpb.MethodOptions) {
					proto.SetExtension(o, mcpv1.E_ToolDescription, "")
				})),
			},
			want: "/a.B/C: (mcp.v1.tool_description) must not be empty",
		},
		"streaming method": {
			methods: []connect.Method{method("/a.B/C", connect.StreamTypeServer, createItem)},
			want:    "only unary methods can be tools",
		},
		"invalid tool name": {
			methods: []connect.Method{
				method("/a.B/C", connect.StreamTypeUnary, createItemWith(t, func(o *descriptorpb.MethodOptions) {
					proto.SetExtension(o, mcpv1.E_ToolName, "Not Valid")
				})),
			},
			want: "/a.B/C: (mcp.v1.tool_name): invalid tool name",
		},
		"duplicate tool name": {
			methods: []connect.Method{
				method("/a.B/C", connect.StreamTypeUnary, createItem),
				method("/a.B/D", connect.StreamTypeUnary, createItem),
			},
			want: `duplicate tool name "testdata_TestService_CreateItem": claimed by both /a.B/C and /a.B/D`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			server := connect.NewServer()
			server.Register(tc.methods...)

			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			g.Expect(connectmcp.Register(gosdk.Wrap(mcpServer), server)).To(MatchError(ContainSubstring(tc.want)))

			_, err := connectmcp.NewHandler(server)
			g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
		})
	}
}

// TestRegister_ToolName checks that a valid (mcp.v1.tool_name) names the tool.
func TestRegister_ToolName(t *testing.T) {
	g := NewWithT(t)
	server := connect.NewServer()
	server.Register(connect.Method{
		Spec: connect.Spec{
			Procedure: "/testdata.TestService/CreateItem",
			Schema: createItemWith(t, func(o *descriptorpb.MethodOptions) {
				proto.SetExtension(o, mcpv1.E_ToolName, "create_item")
			}),
		},
		Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
	})

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	g.Expect(connectmcp.Register(gosdk.Wrap(mcpServer), server)).To(Succeed())

	listed, err := inMemorySession(t, mcpServer).ListTools(t.Context(), nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(listed.Tools).To(HaveLen(1))
	g.Expect(listed.Tools[0].Name).To(Equal("create_item"))
}

// createItemArgs returns CreateItem arguments whose JSON is exactly size bytes.
func createItemArgs(size int) json.RawMessage {
	const overhead = len(`{"name":""}`)
	return json.RawMessage(`{"name":"` + strings.Repeat("x", size-overhead) + `"}`)
}

// TestRegister_ReadMaxBytes checks that arguments larger than the configured
// limit fail the call with connecthttp's RESOURCE_EXHAUSTED error before
// reaching the service, that interceptors see that error, and that the client
// gets it even when an interceptor rewrites it.
func TestRegister_ReadMaxBytes(t *testing.T) {
	const limit = 64
	for name, tc := range map[string]struct {
		opts     []connectmcp.Option
		size     int
		max      int // 0: the call succeeds
		redacted bool
	}{
		"at the limit":              {opts: []connectmcp.Option{connectmcp.WithReadMaxBytes(limit)}, size: limit},
		"over the limit":            {opts: []connectmcp.Option{connectmcp.WithReadMaxBytes(limit)}, size: limit + 1, max: limit},
		"over the limit, rewritten": {opts: []connectmcp.Option{connectmcp.WithReadMaxBytes(limit)}, size: limit + 1, max: limit, redacted: true},
		"default, at the limit":     {size: connectmcp.DefaultReadMaxBytes},
		"default, over the limit":   {size: connectmcp.DefaultReadMaxBytes + 1, max: connectmcp.DefaultReadMaxBytes},
		"zero allows any size":      {opts: []connectmcp.Option{connectmcp.WithReadMaxBytes(0)}, size: connectmcp.DefaultReadMaxBytes + 1},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			var intercepted error
			svc := &service{}
			server := connect.NewServer(func(next connect.ServerFunc) connect.ServerFunc {
				return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
					intercepted = next(ctx, spec, stream)
					if intercepted != nil && tc.redacted {
						return connect.NewError(connect.CodeInternal, "redacted")
					}
					return intercepted
				}
			})
			testdataconnect.RegisterTestServiceHandler(server, svc)
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			g.Expect(connectmcp.Register(gosdk.Wrap(mcpServer), server, tc.opts...)).To(Succeed())
			session := inMemorySession(t, mcpServer)

			res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: createItemTool, Arguments: createItemArgs(tc.size)})
			g.Expect(err).ToNot(HaveOccurred())
			if tc.max == 0 {
				g.Expect(res.IsError).To(BeFalse(), text(res))
				g.Expect(svc.calls).To(Equal(1))
				return
			}
			want := fmt.Sprintf("message is larger than configured max %d", tc.max)
			g.Expect(text(res)).To(MatchJSON(fmt.Sprintf(`{"code":"RESOURCE_EXHAUSTED","message":%q}`, want)))
			g.Expect(svc.calls).To(BeZero())
			cerr, ok := errors.AsType[*connect.Error](intercepted)
			g.Expect(ok).To(BeTrue(), "interceptor saw %v", intercepted)
			g.Expect(cerr.Code()).To(Equal(connect.CodeResourceExhausted))
			g.Expect(cerr.Message()).To(Equal(want))
		})
	}
}
