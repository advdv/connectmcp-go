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

package toolcodec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/connectmcp-go/pkg/mcpserver"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
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
// It reveals exactly what connect-go's own server transports put on the wire
// for a handler error (see connecthttp's scrubHandlerError), so a server
// served as MCP discloses no more than it does over Connect, gRPC or gRPC-Web:
//
//   - A locally authored *connect.Error anywhere in err's chain supplies the
//     code, message and details. Text added by wrapping it
//     (fmt.Errorf("...: %w", cerr)) and its WithCause cause are not shown.
//   - A remote *connect.Error (one decoded from a peer, e.g. returned by a
//     connect client) is reported as INTERNAL without a message: a peer's
//     verdict is not the handler's own.
//   - context.Canceled is CANCELED and context.DeadlineExceeded (or
//     os.ErrDeadlineExceeded) is DEADLINE_EXCEEDED, without a message.
//   - Any other error is UNKNOWN without a message.
//
// Return connect.NewError(code, msg) to tell the model something; keep
// internals in err or in WithCause for logging.
func HandleError(err error) (*mcpserver.CallToolResult, error) {
	if err == nil {
		return nil, nil
	}

	var out toolError
	switch cerr, ok := errors.AsType[*connect.Error](err); {
	case ok && cerr != nil && !cerr.IsRemote():
		out = toolError{Code: codeName(cerr.Code()), Message: cerr.Message()}
		for _, detail := range cerr.Details() {
			out.Details = append(out.Details, detailJSON(detail))
		}
	case ok && cerr != nil:
		out.Code = codeName(connect.CodeInternal)
	case errors.Is(err, context.Canceled):
		out.Code = codeName(connect.CodeCanceled)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		out.Code = codeName(connect.CodeDeadlineExceeded)
	default:
		out.Code = codeName(connect.CodeUnknown)
	}

	b, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		return mcpserver.NewToolResultError(`{"code":"` + out.Code + `"}`), nil
	}
	return mcpserver.NewToolResultError(string(b)), nil
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

// ParseError parses the text of a tool error result written by HandleError
// back into the connect error it describes: it is the inverse of HandleError,
// for clients. The error has exactly the code, message and details in text
// and, being a peer's verdict, is marked remote. Text that is not such a tool
// error (not JSON, no known code, an unreadable detail) is reported with the
// second return value; nothing is assumed in its place.
func ParseError(text string) (*connect.Error, error) {
	var in toolError
	if err := json.Unmarshal([]byte(text), &in); err != nil {
		return nil, fmt.Errorf("tool error is not JSON: %w", err)
	}
	var code connect.Code
	if in.Code == "" || in.Code != strings.ToUpper(in.Code) || code.UnmarshalText([]byte(strings.ToLower(in.Code))) != nil {
		return nil, fmt.Errorf("tool error has no known code: %q", in.Code)
	}
	cerr := connect.NewError(code, in.Message)
	for i, raw := range in.Details {
		detail, err := parseDetail(raw)
		if err != nil {
			return nil, fmt.Errorf("tool error detail %d: %w", i, err)
		}
		cerr = cerr.WithDetail(detail)
	}
	return cerr.WithRemote(), nil
}

// parseDetail is the inverse of detailJSON.
func parseDetail(raw json.RawMessage) (*connect.ErrorDetail, error) {
	packed := &anypb.Any{}
	if err := protojson.Unmarshal(raw, packed); err != nil {
		// A detail whose type is not linked in was written with its raw value.
		var opaque struct {
			Type  string `json:"@type"`
			Value []byte `json:"value"`
		}
		if json.Unmarshal(raw, &opaque) != nil || opaque.Type == "" {
			return nil, err
		}
		packed = &anypb.Any{TypeUrl: opaque.Type, Value: opaque.Value}
	}
	return connectproto.NewErrorDetail(packed)
}
