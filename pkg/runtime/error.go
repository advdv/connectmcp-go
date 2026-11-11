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
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/encoding/protojson"
)

// toolError is the JSON shape of a failed RPC as shown to the model.
type toolError struct {
	// Code is the canonical RPC code name, e.g. "INVALID_ARGUMENT".
	Code    string            `json:"code"`
	Message string            `json:"message,omitempty"`
	Details []json.RawMessage `json:"details,omitempty"`
}

// HandleError converts an RPC error into a structured MCP tool error result:
// a JSON object with the error "code", "message" and, for connect errors
// carrying them, "details" (each rendered as protojson with an "@type").
//
// A connect error anywhere in err's chain supplies the code and details;
// any other error is reported with code UNKNOWN.
func HandleError(err error) (*CallToolResult, error) {
	if err == nil {
		return nil, nil
	}

	out := toolError{
		Code:    codeName(connect.CodeUnknown),
		Message: err.Error(),
	}

	if cerr, ok := errors.AsType[*connect.Error](err); ok && cerr != nil {
		out.Code = codeName(cerr.Code())
		// Keep context added by wrapping (fmt.Errorf("...: %w", cerr)); a bare
		// connect error reports just its message, without the code prefix.
		if err.Error() == cerr.Error() {
			out.Message = cerr.Message()
		}
		for _, detail := range cerr.Details() {
			out.Details = append(out.Details, detailJSON(detail))
		}
	}

	b, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		return NewToolResultError("Error: " + err.Error()), nil
	}
	return NewToolResultError(string(b)), nil
}

// codeName renders a connect code in its canonical upper-case form
// ("not_found" -> "NOT_FOUND").
func codeName(code connect.Code) string {
	return strings.ToUpper(code.String())
}

// detailJSON renders an error detail as protojson. Details whose message type
// is not linked into the binary cannot be decoded and are passed through with
// their raw (base64) value instead.
func detailJSON(detail *connect.ErrorDetail) json.RawMessage {
	packed := connectproto.ErrorDetailToAny(detail)
	if b, err := protojson.Marshal(packed); err == nil {
		return b
	}
	b, _ := json.Marshal(struct {
		Type  string `json:"@type"`
		Value []byte `json:"value"`
	}{packed.GetTypeUrl(), packed.GetValue()})
	return b
}
