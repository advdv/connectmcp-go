package connectmcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/connectmcp-go"
	"github.com/advdv/connectmcp-go/pkg/connectmcpv1"
	"github.com/advdv/connectmcp-go/pkg/mcpserver"
	"github.com/advdv/connectmcp-go/pkg/mcpserver/gosdk"
	"github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata/testdataconnect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// listToolsJSON registers the tools of server and returns each tool's JSON as
// a client receives it in tools/list, keyed by tool name.
func listToolsJSON(t *testing.T, server *connect.Server, opts ...connectmcp.Option) map[string]string {
	t.Helper()
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	if err := connectmcp.Register(gosdk.Wrap(mcpServer), server, opts...); err != nil {
		t.Fatal(err)
	}
	listed, err := inMemorySession(t, mcpServer).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]string{}
	for _, tool := range listed.Tools {
		// Only the keys under test: schemas and description are tested elsewhere.
		raw, err := json.Marshal(struct {
			Title       string               `json:"title,omitempty"`
			Annotations *mcp.ToolAnnotations `json:"annotations,omitempty"`
		}{tool.Title, tool.Annotations})
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = string(raw)
	}
	return tools
}

// TestRegister_Annotations checks the annotations of the WorkspaceService
// tools: side-effect hints from idempotency_level, the title, destructive and
// open-world hints from (connectmcp.v1.tool_annotations), and none without either.
func TestRegister_Annotations(t *testing.T) {
	g := NewWithT(t)
	server := connect.NewServer()
	testdataconnect.RegisterWorkspaceServiceHandler(server, testdataconnect.UnimplementedWorkspaceServiceHandler{})

	tools := listToolsJSON(t, server)
	g.Expect(tools).To(HaveLen(6))
	for name, want := range map[string]string{
		"get_workspace": `{"title":"Get workspace","annotations":{
			"title":"Get workspace","readOnlyHint":true,"idempotentHint":true,"destructiveHint":false,"openWorldHint":false}}`,
		"list_workspaces": `{"title":"List workspaces","annotations":{
			"title":"List workspaces","readOnlyHint":true,"idempotentHint":true,"destructiveHint":false,"openWorldHint":false}}`,
		"create_workspace": `{"title":"Create workspace","annotations":{
			"title":"Create workspace","readOnlyHint":false,"idempotentHint":false,"destructiveHint":false,"openWorldHint":false}}`,
		"update_workspace": `{"title":"Update workspace","annotations":{
			"title":"Update workspace","readOnlyHint":false,"idempotentHint":true,"openWorldHint":false}}`,
		"delete_workspace": `{"title":"Delete workspace","annotations":{
			"title":"Delete workspace","readOnlyHint":false,"idempotentHint":true,"destructiveHint":true,"openWorldHint":false}}`,
		// Neither option: no annotations key at all, MCP's defaults apply.
		"archive_workspace": `{}`,
	} {
		g.Expect(tools[name]).To(MatchJSON(want), name)
	}
}

// TestRegister_AnnotationsFromIdempotencyLevel checks the hints derived from
// each idempotency_level without an (connectmcp.v1.tool_annotations) option.
func TestRegister_AnnotationsFromIdempotencyLevel(t *testing.T) {
	for level, want := range map[descriptorpb.MethodOptions_IdempotencyLevel]string{
		descriptorpb.MethodOptions_NO_SIDE_EFFECTS:     `{"annotations":{"readOnlyHint":true,"idempotentHint":true,"destructiveHint":false}}`,
		descriptorpb.MethodOptions_IDEMPOTENT:          `{"annotations":{"readOnlyHint":false,"idempotentHint":true}}`,
		descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN: `{}`,
	} {
		t.Run(level.String(), func(t *testing.T) {
			g := NewWithT(t)
			server := connect.NewServer()
			server.Register(connect.Method{
				Spec: connect.Spec{
					Procedure: "/testdata.TestService/CreateItem",
					Schema: createItemWith(t, func(o *descriptorpb.MethodOptions) {
						proto.ClearExtension(o, connectmcpv1.E_ToolAnnotations)
						o.IdempotencyLevel = level.Enum()
					}),
				},
				Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
			})
			g.Expect(listToolsJSON(t, server)[createItemTool]).To(MatchJSON(want))
		})
	}
}

// TestRegister_AnnotationsContradiction checks that a destructive_hint
// contradicting idempotency_level is a registration error, and that a
// redundant one is accepted.
func TestRegister_AnnotationsContradiction(t *testing.T) {
	register := func(t *testing.T, destructive bool) error {
		server := connect.NewServer()
		server.Register(connect.Method{
			Spec: connect.Spec{
				Procedure: "/a.B/C",
				Schema: createItemWith(t, func(o *descriptorpb.MethodOptions) {
					o.IdempotencyLevel = descriptorpb.MethodOptions_NO_SIDE_EFFECTS.Enum()
					proto.SetExtension(o, connectmcpv1.E_ToolAnnotations, &connectmcpv1.ToolAnnotations{DestructiveHint: proto.Bool(destructive)})
				}),
			},
			Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
		})
		_, err := connectmcp.NewHandler(server)
		return err
	}

	g := NewWithT(t)
	g.Expect(register(t, true)).To(MatchError(
		"connectmcp: /a.B/C: (connectmcp.v1.tool_annotations): destructive_hint is true on a method with idempotency_level NO_SIDE_EFFECTS"))
	g.Expect(register(t, false)).To(Succeed())
}

// TestWithToolAnnotations checks that the option adjusts the derived
// annotations of every registered tool, and can remove them all.
func TestWithToolAnnotations(t *testing.T) {
	g := NewWithT(t)
	server := connect.NewServer()
	testdataconnect.RegisterWorkspaceServiceHandler(server, testdataconnect.UnimplementedWorkspaceServiceHandler{})

	var seen []string
	tools := listToolsJSON(t, server, connectmcp.WithToolAnnotations(
		func(name string, method protoreflect.MethodDescriptor, a *mcpserver.ToolAnnotations) {
			seen = append(seen, string(method.Name()))
			switch name {
			case "archive_workspace":
				a.Title = "Archive workspace"
				a.OpenWorldHint = proto.Bool(false)
			case "update_workspace":
				*a = mcpserver.ToolAnnotations{}
			}
		}))
	g.Expect(seen).To(ConsistOf("GetWorkspace", "ListWorkspaces", "CreateWorkspace",
		"UpdateWorkspace", "DeleteWorkspace", "ArchiveWorkspace"))
	g.Expect(tools["archive_workspace"]).To(MatchJSON(
		`{"title":"Archive workspace","annotations":{"title":"Archive workspace","openWorldHint":false,"readOnlyHint":false,"idempotentHint":false}}`))
	g.Expect(tools["update_workspace"]).To(MatchJSON(`{}`))
	g.Expect(tools["get_workspace"]).To(ContainSubstring(`"readOnlyHint":true`))
}
