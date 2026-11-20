package connectmcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/connectmcp-go"
	"github.com/advdv/connectmcp-go/pkg/connectmcpv1"
	"github.com/advdv/connectmcp-go/pkg/mcpserver/gosdk"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	"github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

// withToolServer returns a server with the annotated TestService, and the
// RuleCasesService's Strings method, which has no tool options, registered by
// hand as read-only in its connect.Spec, like a service whose protos you do
// not own.
func withToolServer() *connect.Server {
	server := connect.NewServer()
	testdataconnect.RegisterTestServiceHandler(server, testdataconnect.UnimplementedTestServiceHandler{})
	server.Register(connect.Method{
		Spec: connect.Spec{
			StreamType:       connect.StreamTypeUnary,
			Procedure:        testdataconnect.RuleCasesServiceStringsProcedure,
			Schema:           testdata.File_testdata_validation_proto.Services().ByName("RuleCasesService").Methods().ByName("Strings"),
			IdempotencyLevel: connect.IdempotencyNoSideEffects,
		},
		Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			var req testdata.StringRulesCases
			if err := stream.Receive(&req); err != nil {
				return err
			}
			return stream.Send(&testdata.RuleCasesResponse{})
		},
	})
	return server
}

// checkStrings is the tool WithTool makes of the Strings method.
var checkStrings = connectmcp.WithTool(testdataconnect.RuleCasesServiceStringsProcedure, connectmcp.ToolOptions{
	Name:        "check_strings",
	Description: "Check the string rules.",
	Annotations: &connectmcpv1.ToolAnnotations{Title: "Check strings", OpenWorldHint: proto.Bool(false)},
})

// TestWithTool checks that WithTool makes a tool of a method without tool
// options, named, described and annotated by it, with the read-only hints of
// the method's connect.Spec, and that calling the tool calls the method.
func TestWithTool(t *testing.T) {
	g := NewWithT(t)

	for name, tc := range map[string]struct {
		opts []connectmcp.Option
		want bool
	}{
		"without WithTool": {},
		"with WithTool":    {opts: []connectmcp.Option{checkStrings}, want: true},
	} {
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
		g.Expect(connectmcp.Register(gosdk.Wrap(mcpServer), withToolServer(), tc.opts...)).To(Succeed(), name)
		session := inMemorySession(t, mcpServer)

		listed, err := session.ListTools(t.Context(), nil)
		g.Expect(err).ToNot(HaveOccurred())
		var tool *mcp.Tool
		for _, listedTool := range listed.Tools {
			if listedTool.Name == "check_strings" {
				tool = listedTool
			}
		}
		if !tc.want {
			g.Expect(tool).To(BeNil(), name)
			continue
		}

		g.Expect(tool).ToNot(BeNil(), name)
		g.Expect(tool.Description).To(Equal("Check the string rules."))
		annotations, err := json.Marshal(tool.Annotations)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(annotations).To(MatchJSON(`{"title":"Check strings","readOnlyHint":true,"idempotentHint":true,
			"destructiveHint":false,"openWorldHint":false}`))

		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "check_strings", Arguments: map[string]any{}})
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(res.IsError).To(BeFalse(), text(res))
		g.Expect(res.StructuredContent).To(Equal(map[string]any{}))
	}
}

// TestWithToolRejects checks that Register checks what WithTool sets like the
// options it stands for, and fails for a procedure that is not registered.
func TestWithToolRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		opt  connectmcp.Option
		want string
	}{
		"unregistered procedure": {
			opt:  connectmcp.WithTool("/testdata.RuleCasesService/Numerics", connectmcp.ToolOptions{Description: "Check numerics."}),
			want: "connectmcp: WithTool: no method of /testdata.RuleCasesService/Numerics, with a protobuf method descriptor, is registered on the connect server",
		},
		"empty description": {
			opt:  connectmcp.WithTool(testdataconnect.RuleCasesServiceStringsProcedure, connectmcp.ToolOptions{}),
			want: "connectmcp: /testdata.RuleCasesService/Strings: (connectmcp.v1.tool_description) must not be empty",
		},
		"invalid name": {
			opt: connectmcp.WithTool(testdataconnect.RuleCasesServiceStringsProcedure, connectmcp.ToolOptions{
				Name: "check strings", Description: "Check the string rules.",
			}),
			want: "connectmcp: /testdata.RuleCasesService/Strings: (connectmcp.v1.tool_name): ",
		},
		"destructive read": {
			opt: connectmcp.WithTool(testdataconnect.RuleCasesServiceStringsProcedure, connectmcp.ToolOptions{
				Description: "Check the string rules.",
				Annotations: &connectmcpv1.ToolAnnotations{DestructiveHint: proto.Bool(true)},
			}),
			want: "connectmcp: /testdata.RuleCasesService/Strings: (connectmcp.v1.tool_annotations): destructive_hint is true on a method with idempotency_level NO_SIDE_EFFECTS",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			err := connectmcp.Register(gosdk.Wrap(mcpServer), withToolServer(), tc.opt)
			g.Expect(err).To(MatchError(HavePrefix(tc.want)))
		})
	}
}

// TestWithToolTransport checks that NewTransport, given the same WithTool
// options, calls the tool NewHandler serves for a method without tool
// options, and that without them it calls the derived name, which is no tool.
func TestWithToolTransport(t *testing.T) {
	g := NewWithT(t)
	handler, err := connectmcp.NewHandler(withToolServer(), checkStrings)
	g.Expect(err).ToNot(HaveOccurred())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for name, tc := range map[string]struct {
		opts []connectmcp.Option
		want connect.Code
	}{
		"with WithTool":    {opts: []connectmcp.Option{checkStrings}},
		"without WithTool": {want: connect.CodeUnavailable},
	} {
		transport := connectmcp.NewTransport(srv.Client(), srv.URL, tc.opts...)
		t.Cleanup(func() { _ = transport.Close() })
		client := testdataconnect.NewRuleCasesServiceClient(connect.NewClient(transport))

		_, err := client.Strings(t.Context(), &testdata.StringRulesCases{})
		if tc.want == 0 {
			g.Expect(err).ToNot(HaveOccurred(), name)
			continue
		}
		g.Expect(connect.CodeOf(err)).To(Equal(tc.want), name)
	}
}

// inputAndOutputSchemas registers the tools of server and returns the input
// and output schema of the tool named tool, decoded.
func inputAndOutputSchemas(t *testing.T, server *connect.Server, tool string, opts ...connectmcp.Option) (input, output map[string]any) {
	t.Helper()
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	if err := connectmcp.Register(gosdk.Wrap(mcpServer), server, opts...); err != nil {
		t.Fatal(err)
	}
	listed, err := inMemorySession(t, mcpServer).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listedTool := range listed.Tools {
		if listedTool.Name != tool {
			continue
		}
		return decode(t, listedTool.InputSchema), decode(t, listedTool.OutputSchema)
	}
	t.Fatalf("no tool %s", tool)
	return nil, nil
}

// decode returns schema as the JSON object it marshals to.
func decode(t *testing.T, schema any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// TestWithField checks that WithField describes a request field and makes it
// required, and describes a field of a message nested in the response, in the
// schemas of the tools that have them.
func TestWithField(t *testing.T) {
	g := NewWithT(t)
	in, _ := inputAndOutputSchemas(t, withToolServer(), getItemTool)
	g.Expect(in).To(HaveKeyWithValue("required", BeEmpty()))
	g.Expect(in).To(HaveKeyWithValue("properties", HaveKeyWithValue("id", Not(HaveKey("description")))))

	in, out := inputAndOutputSchemas(t, withToolServer(), getItemTool,
		connectmcp.WithField("testdata.GetItemRequest.id", connectmcp.FieldOptions{Description: "The item's ID.", Required: true}),
		connectmcp.WithField("testdata.Item.name", connectmcp.FieldOptions{Description: "The item's name."}))
	g.Expect(in).To(HaveKeyWithValue("required", ConsistOf("id")))
	g.Expect(in).To(HaveKeyWithValue("properties", HaveKeyWithValue("id", HaveKeyWithValue("description", "The item's ID."))))
	g.Expect(out).To(HaveKeyWithValue("properties", HaveKeyWithValue("item",
		HaveKeyWithValue("properties", HaveKeyWithValue("name", HaveKeyWithValue("description", "The item's name."))))))
}

// TestWithFieldRejects checks that Register fails for a field no tool has,
// and for a description on a field that has the field_description option.
func TestWithFieldRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		opt  connectmcp.Option
		want string
	}{
		"no tool has the field": {
			opt:  connectmcp.WithField("testdata.GetItemRequest.missing", connectmcp.FieldOptions{Description: "Missing."}),
			want: "connectmcp: WithField: no tool's request or response has the field testdata.GetItemRequest.missing",
		},
		"described already": {
			opt:  connectmcp.WithField("testdata.CreateReservationRequest.parent", connectmcp.FieldOptions{Description: "Another."}),
			want: "connectmcp: WithField: testdata.CreateReservationRequest.parent has the (connectmcp.v1.field_description) option already",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			server := withToolServer()
			testdataconnect.RegisterReservationServiceHandler(server, testdataconnect.UnimplementedReservationServiceHandler{})
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			err := connectmcp.Register(gosdk.Wrap(mcpServer), server, tc.opt)
			g.Expect(err).To(MatchError(tc.want))
		})
	}
}
