package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// handleErrorJSON runs HandleError and parses the tool error it produces.
func handleErrorJSON(t *testing.T, err error) map[string]any {
	t.Helper()
	g := NewWithT(t)

	result, handleErr := HandleError(err)
	g.Expect(handleErr).ToNot(HaveOccurred())
	g.Expect(result).ToNot(BeNil())
	g.Expect(result.IsError).To(BeTrue())

	var parsed map[string]any
	g.Expect(json.Unmarshal([]byte(result.Text), &parsed)).To(Succeed(), result.Text)
	return parsed
}

// withDetails attaches msgs to a connect error as error details.
func withDetails(t *testing.T, cerr *connect.Error, msgs ...proto.Message) *connect.Error {
	t.Helper()
	for _, msg := range msgs {
		detail, err := connectproto.NewErrorDetail(msg)
		if err != nil {
			t.Fatalf("NewErrorDetail: %v", err)
		}
		cerr = cerr.WithDetail(detail)
	}
	return cerr
}

func TestHandleError_NilError(t *testing.T) {
	g := NewWithT(t)

	result, err := HandleError(nil)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(BeNil())
}

// Errors other than a local connect error carry no message, as on connect-go's
// own transports: their text is for logs, not for the model.
func TestHandleError_NonConnectErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"plain":               {fmt.Errorf("query users: %w", errors.New("pq: password authentication failed")), "UNKNOWN"},
		"canceled":            {fmt.Errorf("load: %w", context.Canceled), "CANCELED"},
		"deadline exceeded":   {fmt.Errorf("load: %w", context.DeadlineExceeded), "DEADLINE_EXCEEDED"},
		"os deadline":         {fmt.Errorf("dial: %w", os.ErrDeadlineExceeded), "DEADLINE_EXCEEDED"},
		"remote connect":      {connect.NewError(connect.CodeNotFound, "upstream detail").WithRemote(), "INTERNAL"},
		"wrapped remote":      {fmt.Errorf("call upstream: %w", connect.NewError(connect.CodeNotFound, "upstream detail").WithRemote()), "INTERNAL"},
		"typed nil connect":   {fmt.Errorf("oops: %w", (*connect.Error)(nil)), "UNKNOWN"},
		"remote with details": {withDetails(t, connect.NewError(connect.CodeInvalidArgument, "bad").WithRemote(), wrapperspb.String("x")), "INTERNAL"},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)

			resp := handleErrorJSON(t, tc.err)
			g.Expect(resp).To(Equal(map[string]any{"code": tc.code}))
		})
	}
}

func TestHandleError_ConnectErrorCodes(t *testing.T) {
	for code, expected := range map[connect.Code]string{
		connect.CodeCanceled:           "CANCELED",
		connect.CodeUnknown:            "UNKNOWN",
		connect.CodeInvalidArgument:    "INVALID_ARGUMENT",
		connect.CodeDeadlineExceeded:   "DEADLINE_EXCEEDED",
		connect.CodeNotFound:           "NOT_FOUND",
		connect.CodeAlreadyExists:      "ALREADY_EXISTS",
		connect.CodePermissionDenied:   "PERMISSION_DENIED",
		connect.CodeResourceExhausted:  "RESOURCE_EXHAUSTED",
		connect.CodeFailedPrecondition: "FAILED_PRECONDITION",
		connect.CodeAborted:            "ABORTED",
		connect.CodeOutOfRange:         "OUT_OF_RANGE",
		connect.CodeUnimplemented:      "UNIMPLEMENTED",
		connect.CodeInternal:           "INTERNAL",
		connect.CodeUnavailable:        "UNAVAILABLE",
		connect.CodeDataLoss:           "DATA_LOSS",
		connect.CodeUnauthenticated:    "UNAUTHENTICATED",
	} {
		t.Run(expected, func(t *testing.T) {
			g := NewWithT(t)

			resp := handleErrorJSON(t, connect.NewError(code, "test error for "+expected))
			g.Expect(resp["code"]).To(Equal(expected))
			// The bare message, without connect's "code: " prefix.
			g.Expect(resp["message"]).To(Equal("test error for " + expected))
		})
	}
}

func TestHandleError_Messages(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		g := NewWithT(t)

		resp := handleErrorJSON(t, connect.NewError(connect.CodeInternal, ""))
		g.Expect(resp["code"]).To(Equal("INTERNAL"))
		g.Expect(resp).ToNot(HaveKey("message"))
	})

	t.Run("long", func(t *testing.T) {
		g := NewWithT(t)

		long := strings.Repeat("x", 10000)
		resp := handleErrorJSON(t, connect.NewError(connect.CodeInternal, long))
		g.Expect(resp["message"]).To(Equal(long))
	})

	t.Run("special characters", func(t *testing.T) {
		g := NewWithT(t)

		special := `Error with "quotes" and \backslashes\ and <html> and 日本語`
		resp := handleErrorJSON(t, connect.NewError(connect.CodeInternal, special))
		g.Expect(resp["message"]).To(Equal(special))
	})
}

func TestHandleError_Details(t *testing.T) {
	t.Run("scalar wrapper", func(t *testing.T) {
		g := NewWithT(t)

		resp := handleErrorJSON(t, withDetails(t,
			connect.NewError(connect.CodeInvalidArgument, "invalid request"),
			wrapperspb.String("additional info"),
		))
		g.Expect(resp["code"]).To(Equal("INVALID_ARGUMENT"))
		g.Expect(resp["message"]).To(Equal("invalid request"))
		g.Expect(resp["details"]).To(Equal([]any{map[string]any{
			"@type": "type.googleapis.com/google.protobuf.StringValue",
			"value": "additional info",
		}}))
	})

	t.Run("multiple, in order", func(t *testing.T) {
		g := NewWithT(t)

		resp := handleErrorJSON(t, withDetails(t,
			connect.NewError(connect.CodeInvalidArgument, "validation failed"),
			&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{
				{Field: "email", Description: "email is required"},
				{Field: "password", Description: "password must be at least 8 characters"},
			}},
			&errdetails.ErrorInfo{Reason: "QUOTA", Domain: "example.com", Metadata: map[string]string{"limit": "10"}},
		))
		g.Expect(resp["details"]).To(Equal([]any{
			map[string]any{
				"@type": "type.googleapis.com/google.rpc.BadRequest",
				"fieldViolations": []any{
					map[string]any{"field": "email", "description": "email is required"},
					map[string]any{"field": "password", "description": "password must be at least 8 characters"},
				},
			},
			map[string]any{
				"@type":    "type.googleapis.com/google.rpc.ErrorInfo",
				"reason":   "QUOTA",
				"domain":   "example.com",
				"metadata": map[string]any{"limit": "10"},
			},
		}))
	})

	t.Run("unknown message type is passed through raw", func(t *testing.T) {
		g := NewWithT(t)

		cerr := connect.NewError(connect.CodeInternal, "boom").WithDetail(&connect.ErrorDetail{
			Type:  "acme.v1.NotLinkedIn",
			Value: []byte("raw"),
		})
		resp := handleErrorJSON(t, cerr)
		g.Expect(resp["details"]).To(Equal([]any{map[string]any{
			"@type": "type.googleapis.com/acme.v1.NotLinkedIn",
			"value": "cmF3", // base64("raw")
		}}))
	})
}

// A local connect error found deeper in the chain supplies the code, message
// and details. Neither the text added by wrapping nor the cause is shown: like
// connect-go's transports, only the connect error's own message is.
func TestHandleError_WrappedConnectError(t *testing.T) {
	inner := withDetails(t,
		connect.NewError(connect.CodeNotFound, "entity not found"),
		&errdetails.ResourceInfo{ResourceType: "user", ResourceName: "users/123"},
	)

	t.Run("fmt.Errorf", func(t *testing.T) {
		g := NewWithT(t)

		resp := handleErrorJSON(t, fmt.Errorf("middleware: %w", fmt.Errorf("handler: %w", inner)))
		g.Expect(resp["code"]).To(Equal("NOT_FOUND"))
		g.Expect(resp["message"]).To(Equal("entity not found"))
		g.Expect(resp["details"]).To(HaveLen(1))
	})

	t.Run("errors.Join", func(t *testing.T) {
		g := NewWithT(t)

		resp := handleErrorJSON(t, errors.Join(inner, errors.New("additional context")))
		g.Expect(resp["code"]).To(Equal("NOT_FOUND"))
		g.Expect(resp["message"]).To(Equal("entity not found"))
	})

	t.Run("cause is not leaked", func(t *testing.T) {
		g := NewWithT(t)

		// WithCause is connect v2's way to keep an error server-side only.
		cerr := connect.NewError(connect.CodeInternal, "something went wrong").
			WithCause(errors.New("db password is hunter2"))
		resp := handleErrorJSON(t, cerr)
		g.Expect(resp["message"]).To(Equal("something went wrong"))
	})

	t.Run("local error wrapping a context error keeps its own code", func(t *testing.T) {
		g := NewWithT(t)

		cerr := connect.NewError(connect.CodeUnavailable, "try again").WithCause(context.Canceled)
		resp := handleErrorJSON(t, cerr)
		g.Expect(resp).To(Equal(map[string]any{"code": "UNAVAILABLE", "message": "try again"}))
	})
}
