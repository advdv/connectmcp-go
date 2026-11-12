package toolschema

import (
	"encoding/json"
	"testing"

	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	. "github.com/onsi/gomega"
)

func TestToolForMethod_EdgeCaseService(t *testing.T) {
	g := NewWithT(t)

	file := (&testdata.DeepNestingRequest{}).ProtoReflect().Descriptor().ParentFile()
	svc := file.Services().ByName("EdgeCaseService")
	g.Expect(svc).ToNot(BeNil())

	// Verify all edge case methods produce valid tools
	for i := 0; i < svc.Methods().Len(); i++ {
		method := svc.Methods().Get(i)
		t.Run(string(method.Name()), func(t *testing.T) {
			g := NewWithT(t)
			tool := ToolForMethod(method, "Test "+string(method.Name()))

			g.Expect(len(tool.Name)).To(BeNumerically("<=", 64))
			g.Expect(tool.Description).To(HavePrefix("Test "))

			// Input and output schemas are valid JSON with a plain object root
			// and carry no top-level union keyword.
			for _, raw := range []json.RawMessage{tool.RawInputSchema, tool.RawOutputSchema} {
				var schema map[string]any
				g.Expect(json.Unmarshal(raw, &schema)).To(Succeed())
				g.Expect(schema["type"]).To(Equal("object"))
				g.Expect(schema).ToNot(HaveKey("anyOf"))
				g.Expect(schema).ToNot(HaveKey("oneOf"))
				g.Expect(schema).ToNot(HaveKey("allOf"))
			}
		})
	}
}
