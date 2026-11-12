// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var argumentsUnmarshalOptions = protojson.UnmarshalOptions{DiscardUnknown: true}

// ArgumentsError is the reason tool-call arguments could not be decoded into
// the request message. Its Error text is phrased for the model, so it can see
// what was wrong and call the tool again with corrected arguments.
//
// Use ConnectError to report it like any other failed call: as an
// INVALID_ARGUMENT connect error rendered by HandleError.
type ArgumentsError struct {
	// Field is the path of the offending field as named in the tool's input
	// schema, e.g. "page_size" or "items[2].name". It is empty when no single
	// field is to blame.
	Field string
	// Description says what is wrong: with Field when it is set (e.g. `must
	// be an integer, but got the string "ten".`), otherwise with the
	// arguments as a whole.
	Description string
}

func (e *ArgumentsError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("invalid tool arguments: field %q %s Fix this field and call the tool again.",
			e.Field, e.Description)
	}
	return fmt.Sprintf("invalid tool arguments: %s. "+
		"Check the arguments against the tool's input schema and call the tool again.", e.Description)
}

// ConnectError returns e as an INVALID_ARGUMENT connect error with e's text as
// its message. When a single field is to blame, the error carries a
// google.rpc.BadRequest detail with one field violation for it.
func (e *ArgumentsError) ConnectError() *connect.Error {
	cerr := connect.NewError(connect.CodeInvalidArgument, e.Error())
	if e.Field == "" {
		return cerr
	}
	detail, err := connectproto.NewErrorDetail(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{
			Field:       e.Field,
			Description: e.Description,
		}},
	})
	if err != nil {
		return cerr
	}
	return cerr.WithDetail(detail)
}

// UnmarshalArguments unmarshals tool-call arguments, already rewritten by
// DecodeArguments, into msg. A failure is returned as an *ArgumentsError.
//
// When a single field is to blame, the error names that field, the JSON shape
// it expects, and the shape that was sent.
func UnmarshalArguments(args map[string]any, msg proto.Message) error {
	if args == nil {
		return &ArgumentsError{Description: "the arguments must be a JSON object matching the tool's input schema"}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return &ArgumentsError{Description: err.Error()}
	}
	if err := argumentsUnmarshalOptions.Unmarshal(b, msg); err != nil {
		return argumentsError(msg.ProtoReflect().Descriptor(), args, err)
	}
	return nil
}

// argumentsError explains a protojson failure. When one field is to blame the
// parser's own text is left out: it names fields by their JSON name
// ("pageSize"), not the name the input schema showed the model ("page_size").
func argumentsError(md protoreflect.MessageDescriptor, args map[string]any, cause error) *ArgumentsError {
	if bad, ok := findInvalidField(md, args, ""); ok {
		return &ArgumentsError{
			Field: bad.path,
			Description: fmt.Sprintf("must be %s, but got %s.%s",
				bad.expected, describeJSON(bad.value), stringHint(bad.value, bad.wantsStructured)),
		}
	}
	return &ArgumentsError{Description: cause.Error()}
}

type invalidField struct {
	path            string
	expected        string
	value           any
	wantsStructured bool // the field expects a JSON object or array
}

// findInvalidField narrows a protojson failure down to the first field (in key
// order) that fails to unmarshal on its own, descending into nested messages and
// list elements so the reported path is as specific as possible. It reports
// false when no single field fails in isolation (for example, two members of
// the same oneof set at once).
func findInvalidField(md protoreflect.MessageDescriptor, obj map[string]any, prefix string) (invalidField, bool) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		fd := md.Fields().ByName(protoreflect.Name(key))
		if fd == nil {
			fd = md.Fields().ByJSONName(key)
		}
		if fd == nil {
			continue // unknown fields are discarded, never an error
		}
		val := obj[key]
		if fieldUnmarshals(md, key, val) {
			continue
		}
		// The input schema names fields by their proto name, whichever of
		// the two names the model sent.
		path := prefix + string(fd.Name())

		switch {
		case fd.IsList():
			if arr, ok := val.([]any); ok {
				for i, elem := range arr {
					if fieldUnmarshals(md, key, []any{elem}) {
						continue
					}
					elemPath := fmt.Sprintf("%s[%d]", path, i)
					if nested, ok := descend(fd, elem, elemPath); ok {
						return nested, true
					}
					return invalidField{elemPath, expectedJSON(fd), elem, wantsStructured(fd)}, true
				}
			}
			return invalidField{path, "a JSON array whose elements are each " + expectedJSON(fd), val, true}, true
		case fd.IsMap():
			return invalidField{path, "a JSON object whose values are each " + expectedJSON(fd.MapValue()), val, true}, true
		default:
			if nested, ok := descend(fd, val, path); ok {
				return nested, true
			}
			return invalidField{path, expectedJSON(fd), val, wantsStructured(fd)}, true
		}
	}
	return invalidField{}, false
}

// descend continues the search inside a message-typed value that is itself an
// object, so the error names the innermost offending field.
func descend(fd protoreflect.FieldDescriptor, val any, path string) (invalidField, bool) {
	if fd.Kind() != protoreflect.MessageKind || isWellKnown(fd.Message()) {
		return invalidField{}, false
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return invalidField{}, false
	}
	return findInvalidField(fd.Message(), obj, path+".")
}

// fieldUnmarshals reports whether {key: val} alone unmarshals into md.
func fieldUnmarshals(md protoreflect.MessageDescriptor, key string, val any) bool {
	b, err := json.Marshal(map[string]any{key: val})
	if err != nil {
		return false
	}
	return argumentsUnmarshalOptions.Unmarshal(b, dynamicpb.NewMessage(md)) == nil
}

// expectedJSON describes, for the model, the JSON a single value of fd must be.
// For a repeated or map field it describes one element.
func expectedJSON(fd protoreflect.FieldDescriptor) string {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return "true or false"
	case protoreflect.StringKind:
		return "a JSON string"
	case protoreflect.BytesKind:
		return "a base64-encoded string"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "an integer"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "an integer (a number or a numeric string)"
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return "a number"
	case protoreflect.EnumKind:
		return "one of " + enumNames(fd.Enum())
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return expectedMessageJSON(fd.Message())
	default:
		return "a valid JSON value"
	}
}

func expectedMessageJSON(md protoreflect.MessageDescriptor) string {
	switch name := string(md.FullName()); name {
	case "google.protobuf.Struct":
		return "a JSON object (google.protobuf.Struct)"
	case "google.protobuf.ListValue":
		return "a JSON array (google.protobuf.ListValue)"
	case "google.protobuf.Value":
		return "a JSON value (google.protobuf.Value)"
	case "google.protobuf.Timestamp":
		return `an RFC 3339 timestamp string such as "2026-01-02T03:04:05Z"`
	case "google.protobuf.Duration":
		return `a duration string in seconds such as "1.5s"`
	case "google.protobuf.FieldMask":
		return `a string of comma-separated field paths such as "name,description"`
	case "google.protobuf.Any":
		return `a JSON object with an "@type" field`
	case "google.protobuf.Empty":
		return "an empty JSON object"
	default:
		if isWrapperType(name) {
			if v := md.Fields().ByName("value"); v != nil {
				return expectedJSON(v) // wrapper types: the bare wrapped value
			}
		}
		return "a JSON object (" + name + ")"
	}
}

// wantsStructured reports whether a single value of fd is a JSON object or
// array, so a string sent in its place is never valid.
func wantsStructured(fd protoreflect.FieldDescriptor) bool {
	if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
		return false
	}
	switch name := string(fd.Message().FullName()); name {
	case "google.protobuf.Struct", "google.protobuf.ListValue":
		return true
	case "google.protobuf.Value", "google.protobuf.Timestamp",
		"google.protobuf.Duration", "google.protobuf.FieldMask":
		return false
	default:
		return !isWrapperType(name)
	}
}

// isWrapperType reports whether name is one of the google.protobuf scalar
// wrappers (Int64Value, StringValue, ...), which render as the bare value.
func isWrapperType(name string) bool {
	return strings.HasPrefix(name, "google.protobuf.") && strings.HasSuffix(name, "Value") &&
		name != "google.protobuf.Value" && name != "google.protobuf.ListValue"
}

func enumNames(ed protoreflect.EnumDescriptor) string {
	const limit = 12
	vals := ed.Values()
	names := make([]string, 0, min(vals.Len(), limit))
	for i := 0; i < vals.Len() && i < limit; i++ {
		names = append(names, fmt.Sprintf("%q", vals.Get(i).Name()))
	}
	s := strings.Join(names, ", ")
	if vals.Len() > limit {
		s += ", ..."
	}
	return s
}

// describeJSON describes, for the model, the JSON value it actually sent.
func describeJSON(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("the boolean %v", t)
	case float64:
		return fmt.Sprintf("the number %v", t)
	case string:
		const limit = 80
		if len(t) > limit {
			t = t[:limit] + "..."
		}
		return fmt.Sprintf("the string %q", t)
	case map[string]any:
		return "a JSON object"
	case []any:
		return "a JSON array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// stringHint explains the most common model mistake on structured fields:
// sending JSON text inside a string, often encoded more than once.
func stringHint(v any, structured bool) string {
	s, ok := v.(string)
	if !ok || !structured {
		return ""
	}
	var inner any
	if json.Unmarshal([]byte(s), &inner) != nil {
		return ""
	}
	for i := 0; i < 3; i++ {
		str, ok := inner.(string)
		if !ok {
			break
		}
		if json.Unmarshal([]byte(str), &inner) != nil {
			return ""
		}
	}
	switch inner.(type) {
	case map[string]any, []any:
		return " The string contains JSON text. Send the JSON value itself; " +
			"if the tool's schema types this field as a string, encode the value as JSON exactly once, not twice."
	}
	return ""
}
