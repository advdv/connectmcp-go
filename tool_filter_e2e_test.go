package connectmcp

import (
	"testing"

	"github.com/advdv/connectmcp-go/pkg/mcpserver"
	. "github.com/onsi/gomega"
)

// recordingServer captures registered tools so tests can assert which tools
// made it past the filter.
type recordingServer struct {
	tools []mcpserver.Tool
}

func (r *recordingServer) AddTool(tool mcpserver.Tool, _ mcpserver.ToolHandler) {
	r.tools = append(r.tools, tool)
}

func (r *recordingServer) names() []string {
	names := make([]string, 0, len(r.tools))
	for _, tool := range r.tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestGeneratedHandlerToolFilter verifies that WithToolFilter drops filtered
// tools at registration time and that the filter is matched against the
// generated tool descriptors' names.
func TestGeneratedHandlerToolFilter(t *testing.T) {
	g := NewWithT(t)

	excluded := map[string]bool{
		testServiceTool("CreateItem").Name: true,
	}

	rec := &recordingServer{}
	registerTestService(t, rec, &fullTestServer{}, WithToolFilter(func(toolName string) bool {
		return !excluded[toolName]
	}))

	g.Expect(rec.names()).To(ConsistOf(
		testServiceTool("GetItem").Name,
		testServiceTool("ProcessWellKnownTypes").Name,
		testServiceTool("TestValidation").Name,
	))
}

// TestGeneratedHandlerNoFilter verifies that omitting WithToolFilter keeps
// the previous behavior: every tool is registered.
func TestGeneratedHandlerNoFilter(t *testing.T) {
	g := NewWithT(t)

	rec := &recordingServer{}
	registerTestService(t, rec, &fullTestServer{})

	g.Expect(rec.names()).To(ConsistOf(
		testServiceTool("CreateItem").Name,
		testServiceTool("GetItem").Name,
		testServiceTool("ProcessWellKnownTypes").Name,
		testServiceTool("TestValidation").Name,
	))
}
