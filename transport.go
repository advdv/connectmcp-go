package connectmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/advdv/connectmcp-go/pkg/gen"
	"github.com/advdv/connectmcp-go/pkg/runtime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Transport is a connect.Transport that calls RPCs as MCP tools on a server
// served with NewHandler (or Register). It is the client-side counterpart of
// NewHandler, as connecthttp.NewTransport is of connecthttp.Mount: a generated
// client built on it reaches the server through MCP, so tests written against
// the client can cover the MCP surface too.
//
//	transport := connectmcp.NewTransport(http.DefaultClient, "https://api.example.com/mcp")
//	defer transport.Close()
//	client := itemsv1connect.NewItemServiceClient(connect.NewClient(transport))
//
// Create it with NewTransport. It is safe for concurrent use.
type Transport struct {
	transport *mcp.StreamableClientTransport

	mu      sync.Mutex
	session *mcp.ClientSession
	closed  bool
}

// NewTransport returns a Transport that calls the tools of the MCP server at
// url over streamable HTTP, using httpClient (http.DefaultClient when nil).
//
// The MCP session is opened by the first RPC and ended by Close. Each RPC is
// one tools/call request:
//
//   - The tool is the one NewHandler names for the method (gen.ToolNameForMethod).
//   - The request message is sent as the tool arguments in the shape of the
//     tool's input schema (runtime.EncodeMessage), without validating it.
//   - The request headers set on the call (connect.NewClientContext) are added
//     to the HTTP request carrying it, so they are the request headers the
//     server's interceptors see. Headers the MCP client sets itself, such as
//     Content-Type, are kept.
//   - A tool error is returned as the *connect.Error it describes, built only
//     from the code, message and details that arrived (runtime.ParseError).
//
// MCP carries no response headers or trailers, so those of the call stay
// empty. Only unary RPCs can be called; others fail with CodeUnimplemented.
func NewTransport(httpClient *http.Client, url string) *Transport {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client := *httpClient
	client.Transport = callHeaders{base: httpClient.Transport}
	return &Transport{transport: &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &client,
		// Only responses to tool calls are needed, not server-initiated messages.
		DisableStandaloneSSE: true,
	}}
}

// Close ends the MCP session. RPCs fail after Close.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.session == nil {
		return nil
	}
	session := t.session
	t.session = nil
	return session.Close()
}

// NewClientStream implements connect.Transport.
func (t *Transport) NewClientStream(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
	method, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok {
		return nil, connect.Errorf(connect.CodeInternal, "connectmcp: %s has no protobuf method descriptor", spec.Procedure)
	}
	if spec.StreamType != connect.StreamTypeUnary {
		return nil, connect.Errorf(connect.CodeUnimplemented, "connectmcp: %s is a %s method, only unary methods can be tools",
			spec.Procedure, spec.StreamType)
	}
	return &clientStream{ctx: ctx, transport: t, tool: gen.ToolNameForMethod(method)}, nil
}

// connect returns the session, opening it on first use.
func (t *Transport) connect(ctx context.Context) (*mcp.ClientSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("transport is closed")
	}
	if t.session == nil {
		// The session outlives this call: it must not carry the call's values,
		// or the call's headers would be sent with the session's own requests.
		client := mcp.NewClient(&mcp.Implementation{Name: "connectmcp", Version: "0.0.0"}, nil)
		session, err := client.Connect(valueless{ctx}, t.transport, nil)
		if err != nil {
			return nil, err
		}
		t.session = session
	}
	return t.session, nil
}

// valueless is a context without the values of the one it wraps.
type valueless struct{ context.Context }

func (valueless) Value(any) any { return nil }

// callHeaders adds the request headers of the connect call a request is made
// for, if any, to that request.
type callHeaders struct{ base http.RoundTripper }

func (rt callHeaders) RoundTrip(req *http.Request) (*http.Response, error) {
	if info, ok := connect.CallInfoForClientContext(req.Context()); ok && info.RequestHeader().Len() > 0 {
		req = req.Clone(req.Context())
		for key, values := range info.RequestHeader().All() {
			if _, set := req.Header[key]; !set {
				req.Header[key] = values
			}
		}
	}
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// clientStream is the connect.ClientStream of one tool call.
type clientStream struct {
	ctx       context.Context
	transport *Transport
	tool      string

	result   *mcp.CallToolResult
	received bool
}

func (*clientStream) SendHeaders() error { return nil }
func (*clientStream) CloseSend() error   { return nil }
func (*clientStream) Close() error       { return nil }

func (s *clientStream) Send(msg any) error {
	req, ok := msg.(proto.Message)
	if !ok {
		return connect.Errorf(connect.CodeInternal, "connectmcp: request message %T is not a proto.Message", msg)
	}
	if s.result != nil {
		return connect.NewError(connect.CodeInternal, "connectmcp: unary RPC sent more than one request message")
	}
	arguments, err := runtime.EncodeMessage(req)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "connectmcp: encode request: %v", err)
	}
	session, err := s.transport.connect(s.ctx)
	if err != nil {
		return s.callError(err)
	}
	s.result, err = session.CallTool(s.ctx, &mcp.CallToolParams{Name: s.tool, Arguments: arguments})
	if err != nil {
		return s.callError(err)
	}
	return nil
}

// callError is the error of a call that got no tool result.
func (s *clientStream) callError(err error) error {
	code := connect.CodeUnavailable
	switch ctxErr := s.ctx.Err(); {
	case errors.Is(ctxErr, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(ctxErr, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return connect.NewError(code, fmt.Sprintf("connectmcp: call tool %s: %v", s.tool, err)).WithCause(err)
}

func (s *clientStream) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	s.received = true

	res, ok := msg.(proto.Message)
	if !ok {
		return connect.Errorf(connect.CodeInternal, "connectmcp: response message %T is not a proto.Message", msg)
	}
	if s.result == nil {
		return connect.NewError(connect.CodeInternal, "connectmcp: no request message was sent")
	}
	if s.result.IsError {
		text, ok := singleText(s.result)
		if !ok {
			return connect.Errorf(connect.CodeInternal, "connectmcp: tool %s: error result is not one text content block", s.tool)
		}
		cerr, err := runtime.ParseError(text)
		if err != nil {
			return connect.Errorf(connect.CodeInternal, "connectmcp: tool %s: %v", s.tool, err)
		}
		return cerr
	}
	structured, err := json.Marshal(s.result.StructuredContent)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "connectmcp: tool %s: %v", s.tool, err)
	}
	if err := runtime.DecodeResult(structured, res); err != nil {
		return connect.Errorf(connect.CodeInternal, "connectmcp: tool %s: decode result: %v", s.tool, err)
	}
	return nil
}

func singleText(result *mcp.CallToolResult) (string, bool) {
	if len(result.Content) != 1 {
		return "", false
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return "", false
	}
	return text.Text, true
}
