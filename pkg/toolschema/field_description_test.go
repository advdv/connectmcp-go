package toolschema

import (
	"encoding/json"
	"testing"

	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestFieldDescription_Schemas checks where the text of
// (connectmcp.v1.field_description) ends up for each shape of field: it is
// the description of the property, ahead of the description the schema has
// by itself and of the rules.
func TestFieldDescription_Schemas(t *testing.T) {
	md := (&testdata.FieldDescriptionCases{}).ProtoReflect().Descriptor()
	// A recursion depth of 1 turns the recursive "child" into a placeholder.
	props, _ := MessageSchema(md, SchemaOptions{MaxRecursionDepth: 1})["properties"].(map[string]any)

	nested := `"properties": {"a": {"type": "string", "description": "The first."}, "b": {"type": "string"}},
		"required": [], "type": "object"`

	for name, want := range map[string]string{
		"plain":       `{"type": "string", "description": "A plain field."}`,
		"undescribed": `{"type": "string"}`,
		"empty":       `{"type": "string"}`,
		"padded":      `{"type": "string", "description": "Padded."}`,
		"multiline":   `{"type": "string", "description": "First line.\nSecond line."}`,
		"with_rules": `{
			"type": "string",
			"description": "Starts with x-.\nRules: string.prefix = \"x-\"",
			"maxLength": 10,
			"examples": ["x-1"]
		}`,
		// The description of a list or a map is on the array or object.
		"list": `{
			"type": "array",
			"description": "A list.",
			"items": {"type": "string", "description": "Rules: string.prefix = \"x-\""}
		}`,
		"counts": `{
			"type": "object",
			"description": "Counts by name.",
			"propertyNames": {"type": "string"},
			"additionalProperties": {"type": "integer"}
		}`,
		// A message field: the field, then the rules of the message.
		"nested":       `{"description": "The nested thing.\nRules: a and b must differ", ` + nested + `}`,
		"other_nested": `{"description": "Rules: a and b must differ", ` + nested + `}`,
		"urgency": `{
			"type": "string",
			"description": "How urgent it is.",
			"enum": ["URGENCY_UNSPECIFIED", "URGENCY_LOW", "URGENCY_NORMAL", "URGENCY_HIGH"]
		}`,
		"at": `{"type": ["string", "null"], "format": "date-time", "description": "When it happened."}`,
		// Schemas with a description of their own keep it, after the field's.
		"value": `{"description": "Any value.\nrepresents a google.protobuf.Value, a dynamic JSON value (string, number, boolean, array, object)."}`,
		"child": `{"type": "string", "description": "A child.\nJSON-encoded FieldDescriptionCases. Provide a JSON object as a string."}`,
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(props).To(HaveKey(name))
			got, err := json.Marshal(props[name])
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(MatchJSON(want))
		})
	}
}

// TestFieldDescription_OneofMember checks a described member of a oneof: the
// line that ties it to the discriminator, the description, then the rules.
func TestFieldDescription_OneofMember(t *testing.T) {
	g := NewWithT(t)
	md := (&testdata.FieldDescriptionCases{}).ProtoReflect().Descriptor()

	schema := MessageSchema(md, SchemaOptions{})
	choice, _ := schema["properties"].(map[string]any)["choice"].(map[string]any)
	got, err := json.Marshal(choice["properties"])
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(MatchJSON(`{
		"which": {"type": "string", "enum": ["by_name", "by_number"], "description": "Which field of the \"choice\" oneof is set."},
		"by_name": {
			"type": "string",
			"description": "The value when which=\"by_name\".\nThe name to look up.\nRules: string.prefix = \"names/\""
		},
		"by_number": {"type": "integer", "description": "The value when which=\"by_number\"."}
	}`))
}

// TestConfiguredFieldDescription checks reading the option: whitespace
// around the text is dropped, and an empty text counts as no option.
func TestConfiguredFieldDescription(t *testing.T) {
	md := (&testdata.FieldDescriptionCases{}).ProtoReflect().Descriptor()

	for name, tc := range map[string]struct {
		want string
		ok   bool
	}{
		"plain":       {"A plain field.", true},
		"padded":      {"Padded.", true},
		"multiline":   {"First line.\nSecond line.", true},
		"empty":       {"", false},
		"undescribed": {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			got, ok := ConfiguredFieldDescription(md.Fields().ByName(protoreflect.Name(name)))
			g.Expect(got).To(Equal(tc.want))
			g.Expect(ok).To(Equal(tc.ok))
		})
	}
}

// TestFieldDescription_OutputSchema checks that descriptions are in the
// output schema of a tool as well: the same messages, the same schema.
func TestFieldDescription_OutputSchema(t *testing.T) {
	g := NewWithT(t)
	md := (&testdata.FieldDescriptionCases{}).ProtoReflect().Descriptor()

	raw := marshalTopLevelSchema(md, SchemaOptions{})
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	g.Expect(json.Unmarshal(raw, &schema)).To(Succeed())
	g.Expect(schema.Properties["plain"].Description).To(Equal("A plain field."))

	_, err := compileJSONSchema(MessageSchema(md, SchemaOptions{}))
	g.Expect(err).ToNot(HaveOccurred())
}
