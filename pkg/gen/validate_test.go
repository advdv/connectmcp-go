package gen

import (
	"encoding/json"
	"fmt"
	"testing"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	testdata "github.com/advdv/connectmcp-go/pkg/testdata/gen/go/testdata"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// validateCases are the messages of validation.proto that have one field per
// kind of rule.
var validateCases = []proto.Message{
	&testdata.StringRulesCases{},
	&testdata.NumericRulesCases{},
	&testdata.ScalarRulesCases{},
	&testdata.EnumRulesCases{},
	&testdata.CollectionRulesCases{},
	&testdata.WellKnownRulesCases{},
	&testdata.CelRulesCases{},
	&testdata.ModifierRulesCases{},
}

// TestValidateRules_FieldSchemas checks, for every kind of (buf.validate)
// rule, the schema of a field that carries it: a keyword where JSON Schema
// has an exact one, a line in the description otherwise. Every field of the
// messages is listed, so a rule cannot go missing unnoticed.
func TestValidateRules_FieldSchemas(t *testing.T) {
	for _, tc := range []struct {
		message proto.Message
		fields  map[string]string
	}{
		{
			message: &testdata.StringRulesCases{},
			fields: map[string]string{
				"fixed":         `{"const":"fixed","type":"string"}`,
				"exact_len":     `{"maxLength":5,"minLength":5,"type":"string"}`,
				"one_of":        `{"enum":["a","b"],"type":"string"}`,
				"none_of":       `{"description":"Rules: string.not_in = [\"x\", \"y\"]","type":"string"}`,
				"affixes":       `{"description":"Rules:\n- string.prefix = \"pre_\"\n- string.suffix = \"_suf\"\n- string.contains = \"mid\"\n- string.not_contains = \"bad\"","type":"string"}`,
				"byte_lengths":  `{"description":"Rules:\n- string.min_bytes = 1\n- string.max_bytes = 8\n- string.len_bytes = 4","type":"string"}`,
				"hostname":      `{"format":"hostname","type":"string"}`,
				"ipv4":          `{"format":"ipv4","type":"string"}`,
				"ipv6":          `{"format":"ipv6","type":"string"}`,
				"uri":           `{"format":"uri","type":"string"}`,
				"uri_ref":       `{"format":"uri-reference","type":"string"}`,
				"ip":            `{"description":"Rules: string.ip","type":"string"}`,
				"tuuid":         `{"description":"Rules: string.tuuid","type":"string"}`,
				"host_and_port": `{"description":"Rules: string.host_and_port","type":"string"}`,
				"header_name":   `{"description":"Rules:\n- string.well_known_regex = \"KNOWN_REGEX_HTTP_HEADER_NAME\"\n- string.strict = false","type":"string"}`,
				"ulid":          `{"description":"Rules: string.ulid","type":"string"}`,
				"examples":      `{"examples":["first","second"],"type":"string"}`,
				"disabled":      `{"description":"Rules: string.email = false","type":"string"}`,
				"quoted":        `{"description":"Rules: string.prefix = \"say \\\"hi\\\"\\n\"","type":"string"}`,
			},
		},
		{
			message: &testdata.NumericRulesCases{},
			fields: map[string]string{
				"fixed":              `{"const":42,"type":"integer"}`,
				"one_of":             `{"enum":[1,2,3],"type":"integer"}`,
				"none_of":            `{"description":"Rules: int32.not_in = [4, 5]","type":"integer"}`,
				"signed":             `{"maximum":9,"minimum":-9,"type":"integer"}`,
				"fixed_width":        `{"maximum":9,"minimum":1,"type":"integer"}`,
				"signed_fixed_width": `{"maximum":5,"minimum":-5,"type":"integer"}`,
				"big":                `{"description":"Rules:\n- int64.lte = \"9007199254740993\"\n- int64.gte = \"1\"","examples":["9007199254740993"],"type":"string"}`,
				"big_fixed":          `{"const":"18446744073709551615","type":"string"}`,
				"big_one_of":         `{"enum":["-1","1"],"type":"string"}`,
				"big_none_of":        `{"description":"Rules: fixed64.not_in = [\"0\"]","type":"string"}`,
				"big_bound":          `{"description":"Rules: sfixed64.lt = \"0\"","type":"string"}`,
				"ratio":              `{"description":"Rules: float.finite","examples":[0.05],"exclusiveMinimum":0,"maximum":0.1,"type":"number"}`,
				"fixed_double":       `{"const":1.5,"type":"number"}`,
				"one_of_double":      `{"enum":[0.25,0.5],"type":"number"}`,
				"outside":            `{"description":"Rules:\n- int32.lt = 5\n- int32.gt = 10","type":"integer"}`,
				"outside_double":     `{"description":"Rules:\n- double.lte = 5\n- double.gte = 10","type":"number"}`,
				"exactly":            `{"maximum":7,"minimum":7,"type":"integer"}`,
				"not_finite":         `{"description":"Rules: double.lt = \"Infinity\"","examples":["NaN","Infinity","-Infinity",1],"type":"number"}`,
				"unsigned":           `{"examples":[7],"maximum":4294967294,"minimum":1,"type":"integer"}`,
			},
		},
		{
			message: &testdata.ScalarRulesCases{},
			fields: map[string]string{
				"accepted": `{"const":true,"examples":[true],"type":"boolean"}`,
				"fixed":    `{"const":"aGk=","contentEncoding":"base64","format":"byte","type":"string"}`,
				"sized":    `{"contentEncoding":"base64","description":"Rules:\n- bytes.min_len = 1\n- bytes.max_len = 32\n- bytes.len = 16","format":"byte","type":"string"}`,
				"affixes":  `{"contentEncoding":"base64","description":"Rules:\n- bytes.pattern = \"^[a-z]+$\"\n- bytes.prefix = \"AQI=\"\n- bytes.suffix = \"Aw==\"\n- bytes.contains = \"BA==\"","format":"byte","type":"string"}`,
				"one_of":   `{"contentEncoding":"base64","enum":["YQ==","Yg=="],"examples":["YQ=="],"format":"byte","type":"string"}`,
				"address":  `{"contentEncoding":"base64","description":"Rules: bytes.ip","format":"byte","type":"string"}`,
			},
		},
		{
			message: &testdata.EnumRulesCases{},
			fields: map[string]string{
				"fixed":      `{"enum":["URGENCY_NORMAL"],"type":"string"}`,
				"one_of":     `{"enum":["URGENCY_LOW","URGENCY_HIGH"],"type":"string"}`,
				"none_of":    `{"enum":["URGENCY_LOW","URGENCY_NORMAL","URGENCY_HIGH"],"type":"string"}`,
				"defined":    `{"enum":["URGENCY_UNSPECIFIED","URGENCY_LOW","URGENCY_NORMAL","URGENCY_HIGH"],"type":"string"}`,
				"combined":   `{"enum":["URGENCY_LOW","URGENCY_NORMAL"],"examples":["URGENCY_NORMAL"],"type":"string"}`,
				"list":       `{"items":{"enum":["URGENCY_LOW","URGENCY_NORMAL","URGENCY_HIGH"],"type":"string"},"type":"array"}`,
				"by_name":    `{"additionalProperties":{"enum":["URGENCY_NORMAL","URGENCY_HIGH"],"type":"string"},"propertyNames":{"type":"string"},"type":"object"}`,
				"impossible": `{"description":"Rules:\n- enum.in = [\"URGENCY_LOW\"]\n- enum.not_in = [\"URGENCY_LOW\"]","enum":["URGENCY_UNSPECIFIED","URGENCY_LOW","URGENCY_NORMAL","URGENCY_HIGH"],"type":"string"}`,
				"undefined":  `{"enum":["URGENCY_LOW"],"examples":["URGENCY_LOW"],"type":"string"}`,
			},
		},
		{
			message: &testdata.CollectionRulesCases{},
			fields: map[string]string{
				"emails":      `{"items":{"examples":["a@example.com"],"format":"email","minLength":3,"type":"string"},"description":"Rules: repeated.unique","maxItems":3,"minItems":1,"type":"array"}`,
				"big_numbers": `{"items":{"description":"Rules: int64.gt = \"0\"","type":"string"},"type":"array"}`,
				"labels":      `{"additionalProperties":{"maxLength":64,"type":"string"},"maxProperties":4,"minProperties":1,"propertyNames":{"maxLength":10,"pattern":"^[a-z]+$","type":"string"},"type":"object"}`,
				"by_number":   `{"additionalProperties":{"type":"string"},"propertyNames":{"description":"Rules: int32.gt = \"0\"","enum":["1","2"],"pattern":"^-?(0|[1-9]\\d*)$","type":"string"},"type":"object"}`,
				"by_flag":     `{"additionalProperties":{"type":"string"},"propertyNames":{"const":"true","enum":["true","false"],"type":"string"},"type":"object"}`,
				"items":       `{"items":{"description":"Rules: every item must have a name","properties":{"id":{"type":"string"},"name":{"type":"string"}},"required":[],"type":"object"},"maxItems":2,"type":"array"}`,
				"items_by_id": `{"additionalProperties":{"description":"Rules: every item must have an id","properties":{"id":{"type":"string"},"name":{"type":"string"}},"required":[],"type":"object"},"propertyNames":{"type":"string"},"type":"object"}`,
				"not_unique":  `{"description":"Rules: repeated.unique = false","items":{"type":"string"},"type":"array"}`,
			},
		},
		{
			message: &testdata.WellKnownRulesCases{},
			fields: map[string]string{
				"future":         `{"description":"Rules: timestamp.gt_now","format":"date-time","type":["string","null"]}`,
				"recent":         `{"description":"Rules:\n- timestamp.lt_now\n- timestamp.within = \"3600s\"","format":"date-time","type":["string","null"]}`,
				"bounded":        `{"description":"Rules:\n- timestamp.lt = \"2027-01-15T08:00:00Z\"\n- timestamp.gte = \"2023-11-14T22:13:20Z\"","examples":["2025-06-15T15:06:40Z"],"format":"date-time","type":["string","null"]}`,
				"timeout":        `{"description":"Rules:\n- duration.lte = \"300s\"\n- duration.gt = \"0s\"","examples":["1.500s"],"pattern":"^-?[0-9]+(\\.[0-9]+)?s$","type":["string","null"]}`,
				"fixed_timeout":  `{"description":"Rules: duration.in = [\"1s\", \"60s\"]","pattern":"^-?[0-9]+(\\.[0-9]+)?s$","type":["string","null"]}`,
				"empty_mask":     `{"const":"","type":"string"}`,
				"fixed_mask":     `{"const":"displayName,labels","type":"string"}`,
				"mask":           `{"description":"Rules: field_mask.in = [\"display_name\", \"labels\"]","examples":["displayName"],"type":"string"}`,
				"detail":         `{"description":"Rules:\n- any.in = [\"type.googleapis.com/google.protobuf.Duration\"]\n- any.not_in = [\"type.googleapis.com/google.protobuf.Timestamp\"]","properties":{"@type":{"type":"string"},"value":{}},"required":["@type"],"type":["object","null"]}`,
				"wrapped_number": `{"maximum":5,"minimum":1,"type":["number","null"]}`,
				"wrapped_string": `{"maxLength":8,"type":["string","null"]}`,
				"wrapped_big":    `{"description":"Rules: int64.gte = \"1\"","type":["string","null"]}`,
			},
		},
		{
			message: &testdata.CelRulesCases{},
			fields: map[string]string{
				"low":             `{"type":"integer"}`,
				"high":            `{"type":"integer"}`,
				"name":            `{"description":"Rules: uuid_name = \"workspaces\": value must be a resource name with a UUID in lower case as its ID","type":"string"}`,
				"slug":            `{"description":"Rules: slug: this.matches('^[a-z0-9-]+$') ? '' : 'value must be a slug'","type":"string"}`,
				"step":            `{"description":"Rules: multiple_of_any = [5, 7]: value must be a multiple of one of the given numbers; value must be positive","minimum":0,"type":"integer"}`,
				"undocumented":    `{"description":"Rules: undocumented = 3","type":"string"}`,
				"with_message":    `{"description":"Rules: value must not be \"root\"","type":"string"}`,
				"without_message": `{"description":"Rules: this != 'admin' ? '' : 'value must not be admin'","type":"string"}`,
				"only_id":         `{"description":"Rules: field.only_id","type":"string"}`,
				"bare_expression": `{"description":"Rules: this.size() % 2 == 0","type":"string"}`,
				"several":         `{"description":"Rules:\n- string.prefix = \"x-\"\n- slug: this.matches('^[a-z0-9-]+$') ? '' : 'value must be a slug'\n- first\n- second","maxLength":20,"type":"string"}`,
				"nested":          `{"description":"Rules: name and alias must differ\nRules: nested.name is required","properties":{"alias":{"type":"string"},"name":{"type":"string"}},"required":[],"type":"object"}`,
			},
		},
		{
			message: &testdata.ModifierRulesCases{},
			fields: map[string]string{
				"must_set":          `{"type":"string"}`,
				"behavior_required": `{"type":"string"}`,
				"if_set":            `{"description":"Rules, skipped when the value is unset or zero: string.prefix = \"x-\"","minLength":3,"type":"string"}`,
				"never":             `{"examples":["x-ample"],"type":"string"}`,
				"only_ignore":       `{"type":"string"}`,
				"plain":             `{"type":"string"}`,
				"child":             `{"description":"JSON-encoded ModifierRulesCases. Provide a JSON object as a string.\nRules: child must be plain","type":"string"}`,
			},
		},
	} {
		md := tc.message.ProtoReflect().Descriptor()
		t.Run(string(md.Name()), func(t *testing.T) {
			// A recursion depth of 1 keeps the recursive "child" field small.
			schema := MessageSchema(md, SchemaOptions{MaxRecursionDepth: 1})
			props, _ := schema["properties"].(map[string]any)

			for i := 0; i < md.Fields().Len(); i++ {
				fd := md.Fields().Get(i)
				if oo := fd.ContainingOneof(); oo != nil && !oo.IsSynthetic() {
					continue // oneof members: TestValidateRules_OneofMember
				}
				name := string(fd.Name())
				t.Run(name, func(t *testing.T) {
					g := NewWithT(t)
					want, ok := tc.fields[name]
					g.Expect(ok).To(BeTrue(), "no expectation for field %s", name)
					got, err := json.Marshal(props[name])
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(got).To(MatchJSON(want))
				})
			}
		})
	}
}

// TestValidateRules_Message checks the rules on a message, which are across
// fields and so are all described: CEL rules by their message, or by their
// expression when they have none, and (buf.validate.message).oneof.
func TestValidateRules_Message(t *testing.T) {
	g := NewWithT(t)

	md := (&testdata.CelRulesCases{}).ProtoReflect().Descriptor()
	want := "Rules:\n" +
		"- low must not exceed high\n" +
		"- this.low >= 0 ? '' : 'low must not be negative'\n" +
		"- exactly one of low, high must be set\n" +
		"- at most one of name, slug must be set\n" +
		"- this.high < 1000"
	g.Expect(ExtractValidateMessageConstraints(md)).To(Equal(map[string]any{"description": want}))
	g.Expect(MessageSchema(md, SchemaOptions{})["description"]).To(Equal(want))

	// A single rule is on one line.
	single := (&testdata.CreateReservationRequest{}).ProtoReflect().Descriptor()
	g.Expect(ExtractValidateMessageConstraints(single)).To(Equal(map[string]any{
		"description": "Rules: end_time must be after start_time",
	}))

	// No rules: an empty result and no description.
	none := (&testdata.StringRulesCases{}).ProtoReflect().Descriptor()
	g.Expect(ExtractValidateMessageConstraints(none)).To(BeEmpty())
	g.Expect(ExtractValidateMessageConstraints(none)).ToNot(BeNil())
	g.Expect(MessageSchema(none, SchemaOptions{})).ToNot(HaveKey("description"))
}

// TestValidateRules_Required checks that (buf.validate.field).required puts a
// field in "required" like the REQUIRED field behavior does, unless the rules
// of the field are ignored, and that a required oneof still gets there.
func TestValidateRules_Required(t *testing.T) {
	g := NewWithT(t)
	md := (&testdata.ModifierRulesCases{}).ProtoReflect().Descriptor()

	for name, want := range map[string]bool{
		"must_set":          true,
		"behavior_required": true,
		"if_set":            false,
		"never":             false, // required, but IGNORE_ALWAYS
		"plain":             false,
	} {
		g.Expect(IsFieldRequired(md.Fields().ByName(protoreflect.Name(name)))).To(Equal(want), name)
	}
	g.Expect(MessageSchema(md, SchemaOptions{})["required"]).To(Equal([]string{"must_set", "behavior_required", "choice"}))
}

// TestValidateRules_OneofMember checks that the rules of a oneof member are
// added to the description that ties the member to the discriminator.
func TestValidateRules_OneofMember(t *testing.T) {
	g := NewWithT(t)
	md := (&testdata.ModifierRulesCases{}).ProtoReflect().Descriptor()

	schema := MessageSchema(md, SchemaOptions{})
	got, err := json.Marshal(schema["properties"].(map[string]any)["choice"].(map[string]any)["properties"])
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(got).To(MatchJSON(`{
		"which": {"type": "string", "enum": ["by_name", "by_number"], "description": "Which field of the \"choice\" oneof is set."},
		"by_name": {
			"type": "string",
			"maxLength": 40,
			"description": "The value when which=\"by_name\".\nRules: string.prefix = \"names/\""
		},
		"by_number": {"type": "integer", "description": "The value when which=\"by_number\"."}
	}`))
}

// TestExtractValidateConstraints_Shape checks what the function returns for
// the field as a whole: the rules of a list are those of the array with the
// rules of its items under "items", and those of a map have the rules of its
// keys and values under "propertyNames" and "additionalProperties".
func TestExtractValidateConstraints_Shape(t *testing.T) {
	collections := (&testdata.CollectionRulesCases{}).ProtoReflect().Descriptor()
	modifiers := (&testdata.ModifierRulesCases{}).ProtoReflect().Descriptor()

	for name, tc := range map[string]struct {
		fd   protoreflect.FieldDescriptor
		want map[string]any
	}{
		"list": {
			fd: collections.Fields().ByName("emails"),
			want: map[string]any{
				"minItems":    1,
				"maxItems":    3,
				"description": "Rules: repeated.unique",
				"items": map[string]any{
					"format":    "email",
					"minLength": 3,
					"examples":  []any{"a@example.com"},
				},
			},
		},
		"list with item rules only": {
			fd:   collections.Fields().ByName("big_numbers"),
			want: map[string]any{"items": map[string]any{"description": `Rules: int64.gt = "0"`}},
		},
		"map": {
			fd: collections.Fields().ByName("labels"),
			want: map[string]any{
				"minProperties":        1,
				"maxProperties":        4,
				"propertyNames":        map[string]any{"pattern": "^[a-z]+$", "maxLength": 10},
				"additionalProperties": map[string]any{"maxLength": 64},
			},
		},
		"map with numeric keys": {
			// A key is a JSON string: "in" lists strings, a bound is described.
			fd: collections.Fields().ByName("by_number"),
			want: map[string]any{"propertyNames": map[string]any{
				"enum":        []any{"1", "2"},
				"description": `Rules: int32.gt = "0"`,
			}},
		},
		"no rules": {
			fd:   modifiers.Fields().ByName("plain"),
			want: map[string]any{},
		},
		"ignore without rules": {
			fd:   modifiers.Fields().ByName("only_ignore"),
			want: map[string]any{},
		},
		"ignore always keeps examples only": {
			fd:   modifiers.Fields().ByName("never"),
			want: map[string]any{"examples": []any{"x-ample"}},
		},
		"ignore if zero value": {
			fd: modifiers.Fields().ByName("if_set"),
			want: map[string]any{
				"minLength":   3,
				"description": `Rules, skipped when the value is unset or zero: string.prefix = "x-"`,
			},
		},
		"required only": {
			fd:   modifiers.Fields().ByName("must_set"),
			want: map[string]any{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			got := ExtractValidateConstraints(tc.fd)
			g.Expect(got).ToNot(BeNil())
			g.Expect(got).To(Equal(tc.want))
		})
	}
}

// TestExtractValidateConstraints_IgnoreIfZeroKeywordsOnly checks the note on
// a field whose rules all have keywords: the keywords do not hold for a zero
// value, which the description says.
func TestExtractValidateConstraints_IgnoreIfZeroKeywordsOnly(t *testing.T) {
	g := NewWithT(t)
	fd := (&testdata.TestValidationRequest{}).ProtoReflect().Descriptor().Fields().ByName("resource_group_id")
	g.Expect(ExtractValidateConstraints(fd)).To(Equal(map[string]any{
		"format":      "uuid",
		"description": "Rules are skipped when the value is unset or zero.",
	}))
}

// TestValidateRules_Deterministic checks that the schema, with its
// description lines, is the same on every call: rules are walked in field
// number order, not in map order.
func TestValidateRules_Deterministic(t *testing.T) {
	g := NewWithT(t)
	for _, message := range validateCases {
		md := message.ProtoReflect().Descriptor()
		first, err := json.Marshal(MessageSchema(md, SchemaOptions{}))
		g.Expect(err).ToNot(HaveOccurred())
		for range 20 {
			again, err := json.Marshal(MessageSchema(md, SchemaOptions{}))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(string(again)).To(Equal(string(first)), string(md.Name()))
		}
	}
}

// TestValidateRules_ValidJSONSchema checks that the schemas with every kind
// of rule are valid JSON Schema documents.
func TestValidateRules_ValidJSONSchema(t *testing.T) {
	for _, message := range append([]proto.Message{&testdata.CreateReservationRequest{}}, validateCases...) {
		md := message.ProtoReflect().Descriptor()
		t.Run(string(md.Name()), func(t *testing.T) {
			_, err := compileJSONSchema(MessageSchema(md, SchemaOptions{}))
			NewWithT(t).Expect(err).ToNot(HaveOccurred())
		})
	}
}

// TestValidateRules_SchemaAcceptsWhatTheRulesAccept validates arguments
// against the schema of a realistic request. The keywords must accept every
// value the rules accept, and reject what the rules with a keyword reject.
func TestValidateRules_SchemaAcceptsWhatTheRulesAccept(t *testing.T) {
	g := NewWithT(t)
	md := (&testdata.CreateReservationRequest{}).ProtoReflect().Descriptor()
	compiled, err := compileJSONSchema(MessageSchema(md, SchemaOptions{}))
	g.Expect(err).ToNot(HaveOccurred())

	valid := func() map[string]any {
		return map[string]any{
			"parent":     "workspaces/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d",
			"title":      "Quarterly planning",
			"seats":      8,
			"start_time": "2030-01-01T10:00:00Z",
			"end_time":   "2030-01-01T11:00:00Z",
			"attendees":  []any{"a@example.com", "b@example.com"},
			"labels":     map[string]any{"team": "platform"},
			"priority":   "URGENCY_HIGH",
		}
	}
	g.Expect(compiled.Validate(valid())).To(Succeed())

	for name, mutate := range map[string]func(map[string]any){
		"missing required field": func(m map[string]any) { delete(m, "parent") },
		"title too short":        func(m map[string]any) { m["title"] = "" },
		"title too long":         func(m map[string]any) { m["title"] = string(make([]byte, 121)) },
		"too few seats":          func(m map[string]any) { m["seats"] = 0 },
		"too many seats":         func(m map[string]any) { m["seats"] = 51 },
		"too many attendees": func(m map[string]any) {
			attendees := make([]any, 21)
			for i := range attendees {
				attendees[i] = fmt.Sprintf("%d@example.com", i)
			}
			m["attendees"] = attendees
		},
		"bad label key":        func(m map[string]any) { m["labels"] = map[string]any{"Team": "platform"} },
		"label value too long": func(m map[string]any) { m["labels"] = map[string]any{"team": string(make([]byte, 65))} },
		"excluded enum value":  func(m map[string]any) { m["priority"] = "URGENCY_UNSPECIFIED" },
	} {
		arguments := valid()
		mutate(arguments)
		g.Expect(compiled.Validate(arguments)).ToNot(Succeed(), name)
	}
}

// TestToolForMethod_ValidateRules checks the whole input schema of a tool
// whose request combines field descriptions and most kinds of rules, and
// that examples reach the output schema as well.
func TestToolForMethod_ValidateRules(t *testing.T) {
	g := NewWithT(t)
	method := testdata.File_testdata_validation_proto.Services().ByName("ReservationService").Methods().ByName("CreateReservation")
	tool := ToolForMethod(method, "Reserve seats in a workspace.")

	g.Expect(tool.RawInputSchema).To(MatchJSON(`{
		"type": "object",
		"description": "Rules: end_time must be after start_time",
		"required": ["parent", "title", "start_time", "end_time"],
		"properties": {
			"parent": {
				"type": "string",
				"description": "The workspace to book in.\nRules: uuid_name = \"workspaces\": value must be a resource name with a UUID in lower case as its ID",
				"examples": ["workspaces/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d"]
			},
			"title": {"type": "string", "minLength": 1, "maxLength": 120, "examples": ["Quarterly planning"]},
			"seats": {
				"type": "integer",
				"description": "Number of seats. The server uses 1 when unset.",
				"minimum": 1,
				"maximum": 50,
				"examples": [8]
			},
			"start_time": {
				"type": ["string", "null"],
				"format": "date-time",
				"description": "When the reservation starts.\nRules: timestamp.gt_now"
			},
			"end_time": {"type": ["string", "null"], "format": "date-time"},
			"attendees": {
				"type": "array",
				"maxItems": 20,
				"description": "Rules: repeated.unique",
				"items": {"type": "string", "format": "email"}
			},
			"labels": {
				"type": "object",
				"maxProperties": 8,
				"propertyNames": {"type": "string", "pattern": "^[a-z][a-z0-9_]{0,30}$"},
				"additionalProperties": {"type": "string", "maxLength": 64}
			},
			"priority": {"type": "string", "enum": ["URGENCY_LOW", "URGENCY_NORMAL", "URGENCY_HIGH"]}
		}
	}`))

	g.Expect(tool.RawOutputSchema).To(MatchJSON(`{
		"type": "object",
		"required": [],
		"properties": {
			"name": {"type": "string", "examples": ["reservations/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d"]}
		}
	}`))
}

// TestMergeConstraints checks how the keywords of rules are merged into a
// schema: a description is added to the existing one, nested schemas are
// merged, everything else is set.
func TestMergeConstraints(t *testing.T) {
	g := NewWithT(t)

	schema := map[string]any{
		"type":                 "array",
		"description":          "existing",
		"enum":                 []string{"A", "B"},
		"items":                map[string]any{"type": "string", "description": "item"},
		"additionalProperties": true,
	}
	mergeConstraints(schema, map[string]any{
		"description":          "Rules: added",
		"enum":                 []string{"A"},
		"maxItems":             2,
		"items":                map[string]any{"format": "email", "description": "Rules: item rule"},
		"propertyNames":        map[string]any{"pattern": "^a$"},
		"additionalProperties": map[string]any{"maxLength": 1},
	})
	g.Expect(schema).To(Equal(map[string]any{
		"type":        "array",
		"description": "existing\nRules: added",
		"enum":        []string{"A"},
		"maxItems":    2,
		"items": map[string]any{
			"type":        "string",
			"format":      "email",
			"description": "item\nRules: item rule",
		},
		"propertyNames":        map[string]any{"pattern": "^a$"},
		"additionalProperties": map[string]any{"maxLength": 1},
	}))

	// Without an existing description the rules are the description.
	plain := map[string]any{"type": "string"}
	mergeConstraints(plain, map[string]any{"description": "Rules: only"})
	g.Expect(plain["description"]).To(Equal("Rules: only"))
}

// TestCheckValidateRules_Decodable checks that every rule in the test protos,
// including the predefined ones, can be decoded, and that recursive messages
// end the walk.
func TestCheckValidateRules_Decodable(t *testing.T) {
	g := NewWithT(t)
	for _, message := range append([]proto.Message{
		&testdata.CreateReservationRequest{},
		&testdata.TestValidationRequest{},
		&testdata.TreeNode{},
	}, validateCases...) {
		g.Expect(CheckValidateRules(message.ProtoReflect().Descriptor())).To(Succeed())
	}
}

// TestCheckValidateRules_Undecodable checks that a rule that arrives as
// unknown fields is reported, wherever it sits: that is how a predefined rule
// looks when the package that declares it is not linked in, and a rule newer
// than the protovalidate module in use.
func TestCheckValidateRules_Undecodable(t *testing.T) {
	// Field 9999 is in the extension range of the rule messages, where
	// predefined rules are declared; nothing declares this one.
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 9999, protowire.VarintType), 1)
	undecodable := func() *validate.StringRules {
		rules := &validate.StringRules{}
		rules.ProtoReflect().SetUnknown(unknown)
		return rules
	}
	field := func(rules *validate.FieldRules) *descriptorpb.FieldOptions {
		opts := &descriptorpb.FieldOptions{}
		proto.SetExtension(opts, validate.E_Field, rules)
		return opts
	}
	str, msg, rep := descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
		descriptorpb.FieldDescriptorProto_LABEL_REPEATED

	topLevel := &validate.FieldRules{}
	topLevel.ProtoReflect().SetUnknown(unknown)

	messageRules := &validate.MessageRules{}
	messageRules.ProtoReflect().SetUnknown(unknown)
	messageOpts := &descriptorpb.MessageOptions{}
	proto.SetExtension(messageOpts, validate.E_Message, messageRules)

	oneofRules := &validate.OneofRules{}
	oneofRules.ProtoReflect().SetUnknown(unknown)
	oneofOpts := &descriptorpb.OneofOptions{}
	proto.SetExtension(oneofOpts, validate.E_Oneof, oneofRules)

	for name, tc := range map[string]struct {
		request *descriptorpb.DescriptorProto
		nested  *descriptorpb.DescriptorProto
		want    string
	}{
		"in the rules of a type": {
			request: &descriptorpb.DescriptorProto{Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("name"), Number: proto.Int32(1), Type: &str,
				Options: field(validate.FieldRules_builder{String: undecodable()}.Build()),
			}}},
			want: "undecodable.Request.name: (buf.validate.field) has rules that cannot be decoded (unknown fields in buf.validate.StringRules)",
		},
		"in the field rules": {
			request: &descriptorpb.DescriptorProto{Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("name"), Number: proto.Int32(1), Type: &str,
				Options: field(topLevel),
			}}},
			want: "undecodable.Request.name: (buf.validate.field) has rules that cannot be decoded (unknown fields in buf.validate.FieldRules)",
		},
		"in the rules of list items": {
			request: &descriptorpb.DescriptorProto{Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("names"), Number: proto.Int32(1), Type: &str, Label: &rep,
				Options: field(validate.FieldRules_builder{Repeated: validate.RepeatedRules_builder{
					Items: validate.FieldRules_builder{String: undecodable()}.Build(),
				}.Build()}.Build()),
			}}},
			want: "undecodable.Request.names: (buf.validate.field) has rules that cannot be decoded (unknown fields in buf.validate.StringRules)",
		},
		"in the message rules": {
			request: &descriptorpb.DescriptorProto{Options: messageOpts},
			want:    "undecodable.Request: (buf.validate.message) has rules that cannot be decoded (unknown fields in buf.validate.MessageRules)",
		},
		"in the oneof rules": {
			request: &descriptorpb.DescriptorProto{
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name: proto.String("name"), Number: proto.Int32(1), Type: &str, OneofIndex: proto.Int32(0),
				}},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice"), Options: oneofOpts}},
			},
			want: "undecodable.Request.choice: (buf.validate.oneof) has rules that cannot be decoded (unknown fields in buf.validate.OneofRules)",
		},
		"in a nested message": {
			request: &descriptorpb.DescriptorProto{Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("nested"), Number: proto.Int32(1), Type: &msg, TypeName: proto.String(".undecodable.Nested"),
			}}},
			nested: &descriptorpb.DescriptorProto{Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("name"), Number: proto.Int32(1), Type: &str,
				Options: field(validate.FieldRules_builder{String: undecodable()}.Build()),
			}}},
			want: "undecodable.Nested.name: (buf.validate.field) has rules that cannot be decoded (unknown fields in buf.validate.StringRules)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			tc.request.Name = proto.String("Request")
			nested := tc.nested
			if nested == nil {
				nested = &descriptorpb.DescriptorProto{}
			}
			nested.Name = proto.String("Nested")

			file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
				Name:        proto.String("undecodable.proto"),
				Package:     proto.String("undecodable"),
				Syntax:      proto.String("proto3"),
				Dependency:  []string{"buf/validate/validate.proto"},
				MessageType: []*descriptorpb.DescriptorProto{tc.request, nested},
			}, protoregistry.GlobalFiles)
			g.Expect(err).ToNot(HaveOccurred())

			err = CheckValidateRules(file.Messages().ByName("Request"))
			g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
			g.Expect(err).To(MatchError(ContainSubstring("link the Go package that declares the predefined rule")))
		})
	}
}
