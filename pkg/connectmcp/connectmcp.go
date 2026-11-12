// Package connectmcp serves the RPCs of a connect-go v2 server as MCP tools.
//
// It is a peer of connecthttp: where connecthttp serves a *connect.Server over
// the Connect, gRPC and gRPC-Web protocols, connectmcp serves it over the
// Model Context Protocol. It works at runtime from the method descriptors of
// the registered services and needs no generated code of its own.
//
// A method becomes a tool when it carries the (mcp.v1.tool_description)
// option; methods without it are not exposed:
//
//	rpc GetItem(GetItemRequest) returns (GetItemResponse) {
//	  option (mcp.v1.tool_description) = "Look up one item by its ID.";
//	}
//
// The tool's JSON schemas are derived from the request and response messages,
// and its name from the method's full name or the (mcp.v1.tool_name) option.
//
//	server := connect.NewServer(interceptors...)
//	itemsv1connect.RegisterItemServiceHandler(server, &itemService{})
//
//	mux := http.NewServeMux()
//	connecthttp.Mount(mux, server)                 // Connect, gRPC, gRPC-Web
//	handler, err := connectmcp.NewHandler(server)  // MCP
//	mux.Handle("/mcp", handler)
//
// Every tool call is dispatched with connect.Server.Call, so the server's
// interceptors run for tool calls exactly as they do for RPCs.
package connectmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect/v2"
	"github.com/advdv/protoc-gen-go-mcp/pkg/gen"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime"
	"github.com/advdv/protoc-gen-go-mcp/pkg/runtime/gosdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Protocol is the CallInfo.Protocol value of RPCs that arrive as MCP tool calls.
const Protocol = "mcp"

// DefaultReadMaxBytes is the default limit on the size of a tool call's
// arguments, matching connecthttp's default limit on a request message.
const DefaultReadMaxBytes = 4 * 1024 * 1024 // 4 MiB

// Option configures NewHandler and Register.
type Option func(*config)

type config struct {
	implementation mcp.Implementation
	httpOptions    *mcp.StreamableHTTPOptions
	toolFilter     func(toolName string) bool
	readMaxBytes   int
}

// WithServerInfo sets the name and version the MCP server created by
// NewHandler reports to clients. The default is "connectmcp" and "0.0.0".
// It has no effect on Register, where the caller owns the MCP server.
func WithServerInfo(name, version string) Option {
	return func(c *config) {
		c.implementation = mcp.Implementation{Name: name, Version: version}
	}
}

// WithStreamableHTTPOptions sets the options of the streamable HTTP transport
// created by NewHandler, for example to serve statelessly or with plain JSON
// responses. It has no effect on Register.
func WithStreamableHTTPOptions(options *mcp.StreamableHTTPOptions) Option {
	return func(c *config) {
		c.httpOptions = options
	}
}

// WithToolFilter registers only the annotated tools for which keep returns
// true, given the tool name. Useful to expose a conditional subset of the
// tools (e.g. feature-gated writes).
func WithToolFilter(keep func(toolName string) bool) Option {
	return func(c *config) {
		c.toolFilter = keep
	}
}

// WithReadMaxBytes limits the size of the request message a tool call can
// carry: the JSON of its arguments, in bytes. It is the counterpart of
// connecthttp.WithReadMaxBytes and defaults to DefaultReadMaxBytes; zero
// allows any size.
//
// The arguments are checked before they are decoded. Larger arguments fail
// the call when the handler receives its request message, with the same
// RESOURCE_EXHAUSTED error connecthttp returns ("message is larger than
// configured max N"), so interceptors see it. The client gets that error as a
// tool error even when an interceptor rewrites it.
//
// The limit does not cover the JSON-RPC envelope or the HTTP request body,
// which the MCP transport reads in full before the tool is called. To bound
// those too, wrap the handler with net/http.MaxBytesHandler at a limit with
// headroom above n.
func WithReadMaxBytes(n int) Option {
	return func(c *config) {
		c.readMaxBytes = n
	}
}

func newConfig(opts []Option) config {
	config := config{
		implementation: mcp.Implementation{Name: "connectmcp", Version: "0.0.0"},
		readMaxBytes:   DefaultReadMaxBytes,
	}
	for _, opt := range opts {
		opt(&config)
	}
	return config
}

// NewHandler returns an http.Handler that serves the tools of server over
// MCP's streamable HTTP transport.
//
// The headers of the HTTP request carrying a tool call are the request headers
// of the RPC, so header-based authentication written for connect keeps
// working.
//
// To serve MCP over another transport (such as stdio), or to add prompts and
// resources, create the MCP server yourself and use Register.
//
// See Register for the conditions under which an error is returned.
func NewHandler(server *connect.Server, opts ...Option) (http.Handler, error) {
	config := newConfig(opts)
	mcpServer := mcp.NewServer(&config.implementation, nil)
	if err := Register(gosdk.Wrap(mcpServer), server, opts...); err != nil {
		return nil, err
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, config.httpOptions), nil
}

// Register adds a tool to s for every method registered on server that carries
// the (mcp.v1.tool_description) option. Methods without the option, and
// methods whose schema is not a protobuf method descriptor, are skipped. For
// the official MCP go-sdk, wrap the *mcp.Server with gosdk.Wrap.
//
// Register reads the methods registered on server at the time of the call, so
// register the services first. It returns an error, without adding any tool,
// if:
//   - no registered method carries the option;
//   - the option is set to an empty description;
//   - the option is set on a streaming method;
//   - an (mcp.v1.tool_name) option is set to an invalid name;
//   - two tools end up with the same name.
func Register(s runtime.MCPServer, server *connect.Server, opts ...Option) error {
	config := newConfig(opts)

	type tool struct {
		spec connect.Spec
		def  runtime.Tool
	}
	var (
		tools     []tool
		annotated bool
		names     = map[string]string{} // tool name -> procedure
	)
	for spec := range server.Specs() {
		method, ok := spec.Schema.(protoreflect.MethodDescriptor)
		if !ok {
			continue
		}
		description, ok := gen.ConfiguredToolDescription(method)
		if !ok {
			continue
		}
		annotated = true

		if description == "" {
			return fmt.Errorf("connectmcp: %s: (mcp.v1.tool_description) must not be empty", spec.Procedure)
		}
		if spec.StreamType != connect.StreamTypeUnary {
			return fmt.Errorf("connectmcp: %s: (mcp.v1.tool_description) is set on a %s method, only unary methods can be tools",
				spec.Procedure, spec.StreamType)
		}
		if name, ok := gen.ConfiguredToolName(method); ok {
			if err := gen.ValidateToolName(name); err != nil {
				return fmt.Errorf("connectmcp: %s: (mcp.v1.tool_name): %w", spec.Procedure, err)
			}
		}

		def := gen.ToolForMethod(method, description)
		if config.toolFilter != nil && !config.toolFilter(def.Name) {
			continue
		}
		if prev, dup := names[def.Name]; dup {
			return fmt.Errorf("connectmcp: duplicate tool name %q: claimed by both %s and %s", def.Name, prev, spec.Procedure)
		}
		names[def.Name] = spec.Procedure
		tools = append(tools, tool{spec: spec, def: def})
	}
	if !annotated {
		return fmt.Errorf("connectmcp: no method registered on the connect server carries the (mcp.v1.tool_description) option")
	}

	for _, tool := range tools {
		s.AddTool(tool.def, toolHandler(server, tool.spec, config.readMaxBytes))
	}
	return nil
}

// toolHandler returns the MCP tool handler that calls the procedure of spec on
// server, accepting arguments of at most readMaxBytes (zero: any size).
func toolHandler(server *connect.Server, spec connect.Spec, readMaxBytes int) runtime.ToolHandler {
	return func(ctx context.Context, request *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
		info := &connect.CallInfo{Spec: spec, Protocol: Protocol, Codec: "json"}
		for key, values := range request.Header {
			info.RequestHeader().SetValues(key, values)
		}

		stream := &toolStream{args: request.Arguments, readMaxBytes: readMaxBytes}
		err := server.Call(ctx, spec.Procedure, info, stream)
		switch {
		case stream.argsErr != nil:
			// Arguments that are too large or undecodable are reported as the
			// error the interceptors saw, in the shape of every other tool
			// error, whatever the interceptors returned in its place: a
			// rewritten error would hide from the model what to fix.
			return runtime.HandleError(stream.argsErr)
		case err != nil:
			return runtime.HandleError(err)
		case stream.response == nil:
			return nil, fmt.Errorf("connectmcp: %s returned no response message", spec.Procedure)
		}
		return runtime.NewToolResultJSON(stream.response), nil
	}
}

// toolStream is the connect.ServerStream of one tool call: the single request
// message is decoded from the tool arguments, and the single response message
// is encoded as the tool result.
type toolStream struct {
	args         json.RawMessage
	readMaxBytes int
	received     bool

	// argsErr is the error Receive returned because the arguments were too
	// large or could not be decoded.
	argsErr  *connect.Error
	response []byte
}

func (s *toolStream) Receive(msg any) error {
	if s.received {
		return io.EOF
	}
	s.received = true

	req, ok := msg.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Sprintf("connectmcp: request message %T is not a proto.Message", msg))
	}
	if s.readMaxBytes > 0 && len(s.args) > s.readMaxBytes {
		s.argsErr = connect.Errorf(connect.CodeResourceExhausted, "message is larger than configured max %d", s.readMaxBytes)
		return s.argsErr
	}

	// Absent arguments are an empty object. Arguments that are not a JSON
	// object decode to nil, which UnmarshalArguments reports to the model.
	args := map[string]any{}
	if len(s.args) > 0 {
		if err := json.Unmarshal(s.args, &args); err != nil {
			args = nil
		}
	}

	// Rewrite oneof discriminated wrappers and recursion placeholders into the
	// protojson-native shape, then unmarshal.
	if args != nil {
		if err := runtime.DecodeArguments(req.ProtoReflect().Descriptor(), args); err != nil {
			return s.badArguments(&runtime.ArgumentsError{Description: err.Error()})
		}
	}
	if err := runtime.UnmarshalArguments(args, req); err != nil {
		argsErr, ok := errors.AsType[*runtime.ArgumentsError](err)
		if !ok {
			argsErr = &runtime.ArgumentsError{Description: err.Error()}
		}
		return s.badArguments(argsErr)
	}
	return nil
}

func (s *toolStream) badArguments(err *runtime.ArgumentsError) error {
	s.argsErr = err.ConnectError()
	return s.argsErr
}

func (*toolStream) SendHeaders() error { return nil }

func (s *toolStream) Send(msg any) error {
	if s.response != nil {
		return connect.NewError(connect.CodeInternal, "connectmcp: unary RPC sent more than one response message")
	}
	resp, ok := msg.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Sprintf("connectmcp: response message %T is not a proto.Message", msg))
	}
	encoded, err := runtime.EncodeMessage(resp)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Sprintf("connectmcp: encode response: %v", err))
	}
	s.response = encoded
	return nil
}
