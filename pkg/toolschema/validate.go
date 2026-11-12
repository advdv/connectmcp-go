package toolschema

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This file turns (buf.validate) rules into JSON Schema. The rules are walked
// by reflection, so every rule that is set is found without code for it:
// standard rules, the rules of list items and map keys and values, predefined
// rules (extensions of the rule messages, declared by whoever uses them) and
// CEL rules. Each rule ends up in exactly one place:
//
//   - a JSON Schema keyword, when the rule has an exact equivalent (see
//     ruleTarget.keyword, a table keyed by the name of the rule);
//   - one line in the "description" otherwise, as "<type>.<rule> = <value>"
//     for a standard rule and as its message for a CEL rule.
//
// The keyword table is an optimization: a rule missing from it is described
// instead of dropped. Nothing validates tool arguments against the schema;
// the handler's interceptors (protovalidate) remain what enforces the rules.

// IsFieldRequired reports whether a field must be set: it carries the REQUIRED
// field behavior or (buf.validate.field).required.
func IsFieldRequired(fd protoreflect.FieldDescriptor) bool {
	if proto.HasExtension(fd.Options(), annotations.E_FieldBehavior) {
		behaviors, _ := proto.GetExtension(fd.Options(), annotations.E_FieldBehavior).([]annotations.FieldBehavior)
		for _, behavior := range behaviors {
			if behavior == annotations.FieldBehavior_REQUIRED {
				return true
			}
		}
	}
	rules := fieldRules(fd)
	return rules.GetRequired() && rules.GetIgnore() != validate.Ignore_IGNORE_ALWAYS
}

// ExtractValidateConstraints reads the (buf.validate.field) rules of a field
// and returns the JSON Schema keywords to merge into the schema of that field
// (see the comment at the top of this file).
//
// The keywords describe the field as a whole. For a repeated field they are
// those of the array, with the rules of its items under "items"; for a map
// field the rules of its keys and values are under "propertyNames" and
// "additionalProperties". Rules without a keyword are in "description", and
// example values in "examples". The result is empty, never nil, for a field
// without rules.
func ExtractValidateConstraints(fd protoreflect.FieldDescriptor) map[string]any {
	rules := fieldRules(fd)
	if rules == nil {
		return map[string]any{}
	}
	target := ruleTarget{enum: fd.Enum()}
	if fd.IsMap() {
		target.valueEnum = fd.MapValue().Enum()
	}
	return target.constraints(rules)
}

// ExtractValidateMessageConstraints reads the (buf.validate.message) rules of
// a message and returns the JSON Schema keywords to merge into the schema of
// that message. Rules across fields have no JSON Schema keyword, so they are
// lines in "description". The result is empty, never nil, for a message
// without rules.
func ExtractValidateMessageConstraints(md protoreflect.MessageDescriptor) map[string]any {
	out := map[string]any{}
	rules := messageRules(md)
	if rules == nil {
		return out
	}

	var lines []string
	rangeRules(rules.ProtoReflect(), func(rfd protoreflect.FieldDescriptor, v protoreflect.Value) {
		switch rfd.Name() {
		case "cel":
			for _, rule := range rules.GetCel() {
				lines = append(lines, celLine(rule))
			}
		case "cel_expression":
			lines = append(lines, rules.GetCelExpression()...)
		case "oneof":
			for _, oneof := range rules.GetOneof() {
				quantity := "at most one"
				if oneof.GetRequired() {
					quantity = "exactly one"
				}
				lines = append(lines, fmt.Sprintf("%s of %s must be set", quantity, strings.Join(oneof.GetFields(), ", ")))
			}
		default:
			lines = append(lines, ruleLine("", rfd, v, ruleTarget{}))
		}
	})
	if len(lines) > 0 {
		out["description"] = rulesDescription(lines, "Rules")
	}
	return out
}

// CheckValidateRules reports the first (buf.validate) rule on md, or on a
// message reachable from it, that this binary cannot decode. Such a rule
// arrives as unknown fields: a predefined rule whose Go package is not linked
// into the binary, or a rule newer than the protovalidate Go module in use.
// It cannot be put in the schema, and rather than leaving it out silently
// connectmcp fails registration.
func CheckValidateRules(md protoreflect.MessageDescriptor) error {
	return checkValidateRules(md, map[protoreflect.FullName]bool{})
}

func checkValidateRules(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) error {
	if seen[md.FullName()] {
		return nil
	}
	seen[md.FullName()] = true

	check := func(d protoreflect.Descriptor, option string, rules proto.Message) error {
		if at := undecodable(rules.ProtoReflect()); at != "" {
			return fmt.Errorf("%s: (%s) has rules that cannot be decoded (unknown fields in %s): "+
				"link the Go package that declares the predefined rule, or upgrade the protovalidate Go module",
				d.FullName(), option, at)
		}
		return nil
	}

	if rules := messageRules(md); rules != nil {
		if err := check(md, "buf.validate.message", rules); err != nil {
			return err
		}
	}
	for i := 0; i < md.Oneofs().Len(); i++ {
		oo := md.Oneofs().Get(i)
		if opts := oo.Options(); opts != nil && proto.HasExtension(opts, validate.E_Oneof) {
			if rules, _ := proto.GetExtension(opts, validate.E_Oneof).(*validate.OneofRules); rules != nil {
				if err := check(oo, "buf.validate.oneof", rules); err != nil {
					return err
				}
			}
		}
	}
	for i := 0; i < md.Fields().Len(); i++ {
		fd := md.Fields().Get(i)
		if rules := fieldRules(fd); rules != nil {
			if err := check(fd, "buf.validate.field", rules); err != nil {
				return err
			}
		}
		sub := fd.Message()
		if fd.IsMap() {
			sub = fd.MapValue().Message()
		}
		if sub != nil {
			if err := checkValidateRules(sub, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// undecodable returns the full name of the first message in m, at any depth,
// that has unknown fields, or "" when there is none.
func undecodable(m protoreflect.Message) string {
	if len(m.GetUnknown()) > 0 {
		return string(m.Descriptor().FullName())
	}
	found := ""
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.Message() == nil || fd.IsMap():
		case fd.IsList():
			for i := 0; i < v.List().Len() && found == ""; i++ {
				found = undecodable(v.List().Get(i).Message())
			}
		default:
			found = undecodable(v.Message())
		}
		return found == ""
	})
	return found
}

func fieldRules(fd protoreflect.FieldDescriptor) *validate.FieldRules {
	opts := fd.Options()
	if opts == nil || !proto.HasExtension(opts, validate.E_Field) {
		return nil
	}
	rules, _ := proto.GetExtension(opts, validate.E_Field).(*validate.FieldRules)
	return rules
}

func messageRules(md protoreflect.MessageDescriptor) *validate.MessageRules {
	opts := md.Options()
	if opts == nil || !proto.HasExtension(opts, validate.E_Message) {
		return nil
	}
	rules, _ := proto.GetExtension(opts, validate.E_Message).(*validate.MessageRules)
	return rules
}

// ruleTarget is the value a set of rules applies to: a field, the items of a
// list, or the keys or values of a map.
type ruleTarget struct {
	// enum is the enum of the value, if it is one. Enum rules refer to enum
	// values by number, the schema lists them by name.
	enum protoreflect.EnumDescriptor
	// valueEnum is the enum of the values of a map field.
	valueEnum protoreflect.EnumDescriptor
	// key says the value is a map key. A key is a JSON string whatever its
	// proto type, so numeric rules have no keyword there.
	key bool
}

// constraints returns the JSON Schema keywords for one FieldRules message.
func (t ruleTarget) constraints(rules *validate.FieldRules) map[string]any {
	out := map[string]any{}
	var lines []string

	rangeRules(rules.ProtoReflect(), func(rfd protoreflect.FieldDescriptor, v protoreflect.Value) {
		switch name := rfd.Name(); {
		case name == "cel":
			for _, rule := range rules.GetCel() {
				lines = append(lines, celLine(rule))
			}
		case name == "cel_expression":
			lines = append(lines, rules.GetCelExpression()...)
		case name == "required", name == "ignore":
			// required is in the parent's "required" (IsFieldRequired);
			// ignore is handled below, it qualifies the other rules.
		case rfd.ContainingOneof() != nil && rfd.Message() != nil:
			// The rules for the type of the value: string, int32, repeated...
			lines = append(lines, t.typeConstraints(string(name), v.Message(), out)...)
		default:
			lines = append(lines, ruleLine("", rfd, v, t))
		}
	})

	switch rules.GetIgnore() {
	case validate.Ignore_IGNORE_ALWAYS:
		// No rule is enforced; examples are still worth showing.
		if examples, ok := out["examples"]; ok {
			return map[string]any{"examples": examples}
		}
		return map[string]any{}
	case validate.Ignore_IGNORE_IF_ZERO_VALUE:
		// The keywords still state the rules, though they do not hold for a
		// zero value; say so, if there is any rule to skip.
		keywords := len(out)
		if _, ok := out["examples"]; ok {
			keywords--
		}
		switch {
		case len(lines) > 0:
			out["description"] = rulesDescription(lines, "Rules, skipped when the value is unset or zero")
		case keywords > 0:
			out["description"] = "Rules are skipped when the value is unset or zero."
		}
		return out
	default:
	}

	if len(lines) > 0 {
		out["description"] = rulesDescription(lines, "Rules")
	}
	return out
}

// typeConstraints handles the rules message for one type (StringRules,
// RepeatedRules, ...), named kind after its field in FieldRules ("string",
// "repeated", ...). It sets keywords in out and returns the description
// lines of the rules without a keyword.
func (t ruleTarget) typeConstraints(kind string, rules protoreflect.Message, out map[string]any) []string {
	var lines []string
	nested := func(keyword string, v protoreflect.Value, target ruleTarget) {
		sub, ok := v.Message().Interface().(*validate.FieldRules)
		if !ok {
			return
		}
		if constraints := target.constraints(sub); len(constraints) > 0 {
			out[keyword] = constraints
		}
	}

	rangeRules(rules, func(rfd protoreflect.FieldDescriptor, v protoreflect.Value) {
		switch name := rfd.Name(); {
		case rfd.IsExtension():
			lines = append(lines, predefinedLine(rfd, v, t))
		case name == "example":
			if examples := t.jsonList(kind, rfd, v.List()); len(examples) > 0 {
				out["examples"] = examples
			}
		case kind == "repeated" && name == "items":
			nested("items", v, ruleTarget{enum: t.enum})
		case kind == "map" && name == "keys":
			nested("propertyNames", v, ruleTarget{key: true})
		case kind == "map" && name == "values":
			nested("additionalProperties", v, ruleTarget{enum: t.valueEnum})
		case t.keyword(kind, rules, rfd, v, out):
		default:
			lines = append(lines, ruleLine(kind, rfd, v, t))
		}
	})
	return lines
}

// Rule messages by the JSON type of the values they apply to. 64-bit integers
// are JSON strings (as protojson writes them), so only "const" and "in" have
// a keyword for them.
var (
	number32Kinds = map[string]bool{
		"float": true, "double": true,
		"int32": true, "uint32": true, "sint32": true, "fixed32": true, "sfixed32": true,
	}
	number64Kinds = map[string]bool{
		"int64": true, "uint64": true, "sint64": true, "fixed64": true, "sfixed64": true,
	}
	// stringFormats maps the well-known string rules to a JSON Schema format.
	// Rules without a standard format (ip, tuuid, host_and_port, ...) are
	// described instead.
	stringFormats = map[protoreflect.Name]string{
		"email":    "email",
		"hostname": "hostname",
		"ipv4":     "ipv4",
		"ipv6":     "ipv6",
		"uri":      "uri",
		"uri_ref":  "uri-reference",
		"uuid":     "uuid",
	}
)

// keyword sets the JSON Schema keyword for one rule in out and reports
// whether there is one. It is keyed by the name of the rule, which the rule
// messages share: "gte" is the same rule on int32 and on double.
//
// A keyword is only used when it accepts exactly the values the rule accepts,
// for the JSON form of the value. Everything else is described.
func (t ruleTarget) keyword(
	kind string, rules protoreflect.Message, rfd protoreflect.FieldDescriptor, v protoreflect.Value, out map[string]any,
) bool {
	name := rfd.Name()
	switch {
	case kind == "enum":
		return t.enumKeyword(rules, out)

	case number32Kinds[kind] || number64Kinds[kind] || kind == "bool" || kind == "string" || kind == "bytes":
		switch name {
		case "const":
			if value, ok := t.jsonValue(kind, rfd, v); ok {
				out["const"] = value
				return true
			}
			return false
		case "in":
			if values := t.jsonList(kind, rfd, v.List()); len(values) == v.List().Len() {
				out["enum"] = values
				return true
			}
			return false
		}
	}

	switch {
	case number32Kinds[kind] && !t.key:
		return boundKeyword(rules, rfd, v, out)

	case kind == "string":
		switch name {
		case "len":
			out["minLength"], out["maxLength"] = int(v.Uint()), int(v.Uint())
		case "min_len":
			out["minLength"] = int(v.Uint())
		case "max_len":
			out["maxLength"] = int(v.Uint())
		case "pattern":
			out["pattern"] = v.String()
		default:
			format, ok := stringFormats[name]
			if !ok || !v.Bool() {
				return false
			}
			out["format"] = format
		}
		return true

	case kind == "repeated":
		switch name {
		case "min_items":
			out["minItems"] = int(v.Uint())
		case "max_items":
			out["maxItems"] = int(v.Uint())
		default:
			// "unique" is described: OpenAI rejects a tool schema with
			// uniqueItems (see the conformance tests).
			return false
		}
		return true

	case kind == "map":
		switch name {
		case "min_pairs":
			out["minProperties"] = int(v.Uint())
		case "max_pairs":
			out["maxProperties"] = int(v.Uint())
		default:
			return false
		}
		return true

	case kind == "field_mask" && name == "const":
		// A field mask is one string in JSON, its paths joined in order.
		if value, ok := t.jsonValue(kind, rfd, v); ok {
			out["const"] = value
			return true
		}
	}
	return false
}

// boundKeyword handles gt, gte, lt and lte of a rule message for JSON
// numbers. Integer bounds are made inclusive (gt 0 is minimum 1), floating
// point bounds use exclusiveMinimum and exclusiveMaximum.
func boundKeyword(rules protoreflect.Message, rfd protoreflect.FieldDescriptor, v protoreflect.Value, out map[string]any) bool {
	name := rfd.Name()
	if name != "gt" && name != "gte" && name != "lt" && name != "lte" {
		return false
	}
	number := func(v protoreflect.Value) float64 {
		switch rfd.Kind() {
		case protoreflect.FloatKind, protoreflect.DoubleKind:
			return v.Float()
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
			return float64(v.Uint())
		default:
			return float64(v.Int())
		}
	}
	has := func(n protoreflect.Name) bool {
		fd := rules.Descriptor().Fields().ByName(n)
		return fd != nil && rules.Has(fd)
	}
	bound := func(names ...protoreflect.Name) (float64, bool) {
		for _, n := range names {
			if has(n) {
				return number(rules.Get(rules.Descriptor().Fields().ByName(n))), true
			}
		}
		return 0, false
	}

	// An upper bound below the lower bound means "outside the range" to
	// protovalidate (gt 10 with lt 5: above 10 or below 5), which minimum
	// and maximum would turn into a schema nothing satisfies.
	lower, hasLower := bound("gt", "gte")
	upper, hasUpper := bound("lt", "lte")
	if hasLower && hasUpper && upper <= lower {
		if upper < lower || !has("gte") || !has("lte") {
			return false
		}
	}

	value := number(v)
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	switch rfd.Kind() {
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		keyword := map[protoreflect.Name]string{
			"gt": "exclusiveMinimum", "gte": "minimum", "lt": "exclusiveMaximum", "lte": "maximum",
		}[name]
		out[keyword] = jsonFloat(rfd, v)
	default:
		switch name {
		case "gt":
			out["minimum"] = int(value) + 1
		case "gte":
			out["minimum"] = int(value)
		case "lt":
			out["maximum"] = int(value) - 1
		case "lte":
			out["maximum"] = int(value)
		}
	}
	return true
}

// enumKeyword narrows the enum values listed in the schema to the ones the
// rules allow: const, in and not_in together decide the list. defined_only
// needs nothing, the schema lists defined values only. It reports false,
// leaving the rules to be described, when the value is not an enum or the
// rules allow no value at all.
func (t ruleTarget) enumKeyword(rules protoreflect.Message, out map[string]any) bool {
	enumRules, ok := rules.Interface().(*validate.EnumRules)
	if !ok || t.enum == nil || t.key {
		return false
	}
	contains := func(numbers []int32, n protoreflect.EnumNumber) bool {
		for _, number := range numbers {
			if protoreflect.EnumNumber(number) == n {
				return true
			}
		}
		return false
	}

	names := []string{}
	for i := 0; i < t.enum.Values().Len(); i++ {
		value := t.enum.Values().Get(i)
		switch {
		case enumRules.HasConst() && protoreflect.EnumNumber(enumRules.GetConst()) != value.Number():
		case len(enumRules.GetIn()) > 0 && !contains(enumRules.GetIn(), value.Number()):
		case contains(enumRules.GetNotIn(), value.Number()):
		default:
			names = append(names, string(value.Name()))
		}
	}
	if len(names) == 0 {
		return false
	}
	out["enum"] = names
	return true
}

// jsonList converts the values of a repeated rule ("in", "example") to their
// JSON form, leaving out the ones that have none.
func (t ruleTarget) jsonList(kind string, rfd protoreflect.FieldDescriptor, list protoreflect.List) []any {
	values := make([]any, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		if value, ok := t.jsonValue(kind, rfd, list.Get(i)); ok {
			values = append(values, value)
		}
	}
	return values
}

// jsonValue converts one value of a rule to the JSON form the schema
// describes, which is the form protojson reads and writes: 64-bit integers
// (of a 64-bit field, kind says) and map keys are strings, bytes are base64, enum values are names,
// durations, timestamps and field masks are strings. It reports false for a
// value without such a form, like the number of an undefined enum value.
func (t ruleTarget) jsonValue(kind string, rfd protoreflect.FieldDescriptor, v protoreflect.Value) (any, bool) {
	switch rfd.Kind() {
	case protoreflect.BoolKind:
		if t.key {
			return strconv.FormatBool(v.Bool()), true
		}
		return v.Bool(), true
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if kind == "enum" {
			if t.enum == nil {
				return nil, false
			}
			value := t.enum.Values().ByNumber(protoreflect.EnumNumber(v.Int()))
			if value == nil {
				return nil, false
			}
			return string(value.Name()), true
		}
		if t.key {
			return strconv.FormatInt(v.Int(), 10), true
		}
		return int(v.Int()), true
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if t.key {
			return strconv.FormatUint(v.Uint(), 10), true
		}
		return int(v.Uint()), true
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if number64Kinds[kind] {
			return strconv.FormatInt(v.Int(), 10), true
		}
		return v.Int(), true
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// A 64-bit value of the field is a JSON string; a count, like
		// string.min_bytes, is just a number.
		if number64Kinds[kind] {
			return strconv.FormatUint(v.Uint(), 10), true
		}
		return v.Uint(), true
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return jsonFloat(rfd, v), true
	case protoreflect.StringKind:
		return v.String(), true
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(v.Bytes()), true
	case protoreflect.EnumKind:
		if value := rfd.Enum().Values().ByNumber(v.Enum()); value != nil {
			return string(value.Name()), true
		}
		return int(v.Enum()), true
	case protoreflect.MessageKind, protoreflect.GroupKind:
		raw, err := protojson.Marshal(v.Message().Interface())
		if err != nil {
			return nil, false
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, false
		}
		return value, true
	default:
		return nil, false
	}
}

// jsonFloat is the JSON form of a float or double: a number, or the string
// protojson uses for a value JSON has no number for.
func jsonFloat(rfd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	value := v.Float()
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	case rfd.Kind() == protoreflect.FloatKind:
		// The shortest decimal that is this float32: 0.1, not
		// 0.10000000149011612.
		short, err := strconv.ParseFloat(strconv.FormatFloat(value, 'g', -1, 32), 64)
		if err == nil {
			return short
		}
	}
	return value
}

// ruleLine describes one rule that has no keyword: "string.prefix = "x-"",
// or just "timestamp.gt_now" for a rule that is switched on. kind is empty
// for a rule that is not part of a type's rule message.
func ruleLine(kind string, rfd protoreflect.FieldDescriptor, v protoreflect.Value, t ruleTarget) string {
	name := string(rfd.Name())
	if kind != "" {
		name = kind + "." + name
	}
	if rfd.Kind() == protoreflect.BoolKind && !rfd.IsList() && v.Bool() {
		return name
	}

	text := func(v protoreflect.Value) string {
		value, ok := t.jsonValue(kind, rfd, v)
		if !ok {
			return v.String()
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return v.String()
		}
		return string(raw)
	}
	if !rfd.IsList() {
		return name + " = " + text(v)
	}
	values := make([]string, v.List().Len())
	for i := range values {
		values[i] = text(v.List().Get(i))
	}
	return name + " = [" + strings.Join(values, ", ") + "]"
}

// predefinedLine describes a predefined rule: an extension of a rule message
// whose (buf.validate.predefined) option holds the CEL rules that the value
// of the extension parameterizes. The line is the name and value of the rule
// followed by what its CEL rules say.
func predefinedLine(rfd protoreflect.FieldDescriptor, v protoreflect.Value, t ruleTarget) string {
	line := ruleLine("", rfd, v, t)

	opts := rfd.Options()
	if opts == nil || !proto.HasExtension(opts, validate.E_Predefined) {
		return line
	}
	predefined, _ := proto.GetExtension(opts, validate.E_Predefined).(*validate.PredefinedRules)
	var texts []string
	for _, rule := range predefined.GetCel() {
		if text := celLine(rule); text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return line
	}
	return line + ": " + strings.Join(texts, "; ")
}

// celLine describes a CEL rule by its message. A rule without one computes
// its message in the expression, so the expression is all there is to show.
func celLine(rule *validate.Rule) string {
	for _, text := range []string{rule.GetMessage(), rule.GetExpression(), rule.GetId()} {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}

// rulesDescription formats the description lines of rules under a heading:
// on one line for a single rule, as a list for more.
func rulesDescription(lines []string, heading string) string {
	if len(lines) == 1 {
		return heading + ": " + lines[0]
	}
	return heading + ":\n- " + strings.Join(lines, "\n- ")
}

// rangeRules calls f for every field that is set in a rule message, in a
// stable order: by field number, with extensions (predefined rules) last.
func rangeRules(m protoreflect.Message, f func(protoreflect.FieldDescriptor, protoreflect.Value)) {
	type entry struct {
		fd protoreflect.FieldDescriptor
		v  protoreflect.Value
	}
	var entries []entry
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		entries = append(entries, entry{fd, v})
		return true
	})
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].fd, entries[j].fd
		if a.IsExtension() != b.IsExtension() {
			return b.IsExtension()
		}
		return a.Number() < b.Number()
	})
	for _, e := range entries {
		f(e.fd, e.v)
	}
}

// mergeConstraints merges the keywords from ExtractValidateConstraints into
// a schema. A description is added to the one the schema has, and the
// schemas of items, keys and values are merged rather than replaced.
func mergeConstraints(schema, constraints map[string]any) {
	for key, value := range constraints {
		switch key {
		case "description":
			text, _ := value.(string)
			if existing, _ := schema[key].(string); existing != "" {
				text = existing + "\n" + text
			}
			schema[key] = text
		case "items", "propertyNames", "additionalProperties":
			existing, isSchema := schema[key].(map[string]any)
			nested, ok := value.(map[string]any)
			if isSchema && ok {
				mergeConstraints(existing, nested)
				continue
			}
			schema[key] = value
		default:
			schema[key] = value
		}
	}
}
