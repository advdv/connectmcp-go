# `connectmcp-go`

> [!WARNING]
> This repository started as a fork of [redpanda-data/protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) and is meant for internal use. Do not rely on its stability: it may change or break without notice. Use the [upstream repository](https://github.com/redpanda-data/protoc-gen-go-mcp) instead.

> [!WARNING]
> The changes in this fork were made with the help of AI coding tools. The code has been reviewed by a human.

[![Test](https://github.com/advdv/connectmcp-go/actions/workflows/test.yml/badge.svg)](https://github.com/advdv/connectmcp-go/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/advdv/connectmcp-go)](https://goreportcard.com/report/github.com/advdv/connectmcp-go)

This module serves [ConnectRPC](https://connectrpc.com) APIs as [Model Context Protocol (MCP)](https://modelcontextprotocol.io) servers, built for [connect-go v2](https://connectrpc.com/docs/go/migrating-to-v2/).

Its root package, `connectmcp`, takes your `*connect.Server` and returns an `http.Handler` that exposes annotated RPCs as MCP tools. It is a peer of `connecthttp`, which serves the same server over Connect and gRPC, and of [vanguard-go](https://github.com/connectrpc/vanguard-go), which serves it over REST. It works at runtime from the protobuf descriptors of the registered services: there is no protoc plugin and no generated MCP code.

> [!NOTE]
> This repository was called `protoc-gen-go-mcp` after the upstream project, which is a protoc plugin. It no longer contains one, so it was renamed after its package, following the `<package>-go` naming of connectrpc repositories like `vanguard-go` and `otelconnect-go`.

## Changes from upstream

Forked from upstream at [`6334690`](https://github.com/redpanda-data/protoc-gen-go-mcp/commit/6334690).

- **No protoc plugin.** Tools are built at runtime from the descriptors of a `*connect.Server` (`connectmcp.NewHandler` / `connectmcp.Register`); no `*.pb.mcp.go` files are generated.
- **Opt-in tools.** Only methods annotated with `(connectmcp.v1.tool_description)` are exposed; misconfiguration (empty description, streaming method, invalid or duplicate name) is a registration error.
- **connect-go v2 only.** Tool calls go through `connect.Server.Call`, so interceptors run and HTTP headers become request headers. gRPC and the mark3labs/mcp-go adapter are removed; only the official go-sdk is supported.
- **Correctable argument errors.** Undecodable arguments are returned as an `INVALID_ARGUMENT` tool error, in the same JSON shape as any other failed call, naming the offending field (also as a `google.rpc.BadRequest` detail) and the expected shape, so the model can retry.
- **Tool annotations.** Tools announce MCP's `readOnlyHint` and `idempotentHint` from the standard `idempotency_level` option, and a title, `destructiveHint` and `openWorldHint` from `(connectmcp.v1.tool_annotations)`, so clients can tell a read from a delete.
- **Validation rules in the schema.** Every [protovalidate](https://buf.build/bufbuild/protovalidate) rule reaches the tool's JSON schema, found by reflection instead of a fixed list: standard rules, the rules of list items and map keys and values, predefined (custom) rules, CEL rules and example values. A rule is a JSON Schema keyword where one exists and a line in the property's `description` otherwise (upstream: string length, pattern, uuid/email and some numeric bounds; anything else was dropped silently).
- **Field descriptions.** `(connectmcp.v1.field_description)` describes a field to the model, as the `description` of its property in the tool schemas, so field guidance no longer has to be written into the tool description.
- **Request size limit.** `connectmcp.WithReadMaxBytes` limits the size of a tool call's arguments like `connecthttp.WithReadMaxBytes` limits a request message (default 4 MiB); larger arguments are a `RESOURCE_EXHAUSTED` tool error.
- **Removed:** descriptor-only `gen.RegisterService`, extra properties, the tool name prefix and `runtime.Option` (the filter is now `connectmcp.WithToolFilter`).
- **Module and proto.** The Go module is `github.com/advdv/connectmcp-go`, with the `connectmcp` package at its root. The annotations are `connectmcp/v1/annotations.proto` in proto package `connectmcp.v1` (upstream: `mcp.v1`, same extension numbers), with Go code in `pkg/connectmcpv1`, published to `buf.build/advdv/connectmcp` on each release.
- **Tooling.** Tools pinned with mise, reproducible `just generate`, golangci-lint in CI, GitHub-hosted runners.

## Features

- Serves a connect-go v2 server as MCP in one call, through its interceptors
- Tools are selected and described with a proto option, and so are their fields
- Tool annotations (read-only, idempotent, destructive, title) from `idempotency_level` and a proto option
- JSON Schema for tool inputs and outputs derived from the request and response messages
- [protovalidate](https://buf.build/bufbuild/protovalidate) rules and example values in those schemas, custom and CEL rules included
- Tool calls can be forwarded to a remote connect server
- Built on [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) (the official SDK)

## Usage

### Annotate the RPCs to expose

A method becomes a tool when it carries the `(connectmcp.v1.tool_description)` option. Methods without it are not exposed.

```proto
import "connectmcp/v1/annotations.proto";

service ItemService {
  rpc GetItem(GetItemRequest) returns (GetItemResponse) {
    option (connectmcp.v1.tool_description) = "Look up one item by its ID.";
  }
}
```

The description is what the model sees. It is a proto option rather than the RPC's comment because the descriptors compiled into a Go binary carry options but not comments. See [Importing the annotation with Buf](#importing-the-annotation-with-buf) for how to depend on the annotation.

Generate the standard `*.pb.go` and connect-go v2 code as usual; nothing else is generated.

### Describe the fields

A field is described to the model with the `(connectmcp.v1.field_description)` option, for the same reason: comments do not reach the binary.

```proto
message ListItemsRequest {
  int32 page_size = 1 [
    (connectmcp.v1.field_description) = "How many items to return. The server returns at most 100, and 20 when unset.",
    (buf.validate.field).int32.gte = 0
  ];
}
```

The text is the `description` of the field's property in the input and output schema of every tool that uses the message:

```json
"page_size": {
  "type": "integer",
  "minimum": 0,
  "description": "How many items to return. The server returns at most 100, and 20 when unset."
}
```

Say what the field means and what the server does with it, such as defaults and limits it applies itself. [Validation rules](#validation-rules) and example values are added to the schema from the `buf.validate` options, so they need not be repeated in the text or in the tool description. On a message field the text describes the message as used in that field. Fields without the option have no description.

### Serve MCP

```go
import (
    "connectrpc.com/connect/v2"
    "connectrpc.com/connect/v2/connecthttp"
    "github.com/advdv/connectmcp-go"
)

server := connect.NewServer(validate.NewServerInterceptor())
itemsv1connect.RegisterItemServiceHandler(server, &srv)

mcpHandler, err := connectmcp.NewHandler(server)

mux := http.NewServeMux()
connecthttp.Mount(mux, server)  // the connect API
mux.Handle("/mcp", mcpHandler)  // the annotated RPCs as MCP tools
```

- Every tool call is dispatched with `server.Call`, so the server's interceptors (auth, validation, tracing) run for tool calls exactly as they do for RPCs.
- The headers of the HTTP request carrying a tool call are the RPC's request headers (`CallInfo.RequestHeader()`), and `CallInfo.Protocol` is `"mcp"`.
- `NewHandler` reads the methods registered at the time of the call, so register the services first.
- `NewHandler` returns an error if no method is annotated, a description is empty, an annotated method is streaming, a configured tool name is invalid, a tool annotation contradicts `idempotency_level`, a message has a [validation rule that cannot be decoded](#validation-rules), or two tools share a name.

Options:

```go
connectmcp.NewHandler(server,
    connectmcp.WithServerInfo("items", "1.0.0"),
    connectmcp.WithStreamableHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}),
)
```

See [`examples/connect`](examples/connect) for a runnable version.

### Other transports, prompts and resources

`NewHandler` is a convenience around `connectmcp.Register`, which adds the tools to an MCP server you own. Use it to serve over stdio or to add prompts and resources. It programs against the one-method `mcpserver.MCPServer` interface; the `gosdk` package adapts the official go-sdk server to it:

```go
// raw is *mcp.Server for transport, s is mcpserver.MCPServer for tools.
raw, s := gosdk.NewServer("my-server", "1.0.0")

err := connectmcp.Register(s, server)

raw.Run(ctx, &mcp.StdioTransport{})
```

See [`examples/basic`](examples/basic).

### Forwarding to a remote connect server

In connect-go v2 a generated client has the same method signatures as a service handler, so a client can be registered as the handler. Tool calls are then forwarded:

```go
client := itemsv1connect.NewItemServiceClient(connect.NewClient(
    connecthttp.NewTransport(http.DefaultClient, "https://api.example.com"),
))

server := connect.NewServer()
itemsv1connect.RegisterItemServiceHandler(server, client)
```

The upstream's errors are remote to this server, so, as over connect-go's own transports, the model sees them as a bare `INTERNAL` (see [Errors](#errors)). To pass their code, message and details on, re-author them as the server's own with an interceptor:

```go
func trustUpstreamErrors(next connect.ServerFunc) connect.ServerFunc {
    return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
        err := next(ctx, spec, stream)
        if cerr, ok := errors.AsType[*connect.Error](err); ok && cerr != nil && cerr.IsRemote() {
            local := connect.NewError(cerr.Code(), cerr.Message()).WithCause(err)
            for _, detail := range cerr.Details() {
                local = local.WithDetail(detail)
            }
            return local
        }
        return err
    }
}

server := connect.NewServer(trustUpstreamErrors)
```

### Errors

An error returned from the RPC is given to the model as a tool error (`isError: true`) containing JSON. It reveals exactly what connect-go's own transports put on the wire for that error, so serving a server as MCP discloses nothing it would not disclose over Connect, gRPC or gRPC-Web. A `*connect.Error` created by the handler (anywhere in the error chain) supplies the code, its own message and the details:

```json
{
  "code": "NOT_FOUND",
  "message": "no such item",
  "details": [{"@type": "type.googleapis.com/google.rpc.ResourceInfo", "resourceType": "item", "resourceName": "item-1"}]
}
```

Everything else is reported by code alone, without a message:

- text added by wrapping a connect error (`fmt.Errorf("...: %w", cerr)`) and its `.WithCause(err)` cause are not shown;
- a remote `*connect.Error` (decoded from a peer, e.g. returned by a connect client) is `INTERNAL`;
- `context.Canceled` is `CANCELED`, and `context.DeadlineExceeded` (or `os.ErrDeadlineExceeded`) is `DEADLINE_EXCEEDED`;
- any other error is `UNKNOWN`.

So return `connect.NewError(...)` with a message meant for the model, and keep internals in the wrapped error or in `.WithCause(err)` for logging.

### Calling the tools with a generated client

`connectmcp.NewTransport` is the client-side counterpart of `NewHandler`: a `connect.Transport` that calls each RPC as an MCP tool over streamable HTTP. A generated client built on it reaches the server through MCP, so tests written against the client can run once per way of reaching the server:

```go
for name, transport := range map[string]connect.Transport{
    "connect": connecthttp.NewTransport(srv.Client(), srv.URL+"/rpc"),
    "mcp":     connectmcp.NewTransport(srv.Client(), srv.URL+"/mcp"), // has Close, to end the MCP session
} {
    client := itemsv1connect.NewItemServiceClient(connect.NewClient(transport))
    // the same assertions on responses, *connect.Error codes and details
}
```

The request message is sent as tool arguments in the shape of the tool's input schema, without validating it. A tool error comes back as a remote `*connect.Error` built only from the code, message and details that arrived. Request headers set on a call (`connect.NewClientContext`) are sent with the HTTP request of that call. MCP has no response headers or trailers, and only unary RPCs can be called.

To do the same over an MCP session you own, `toolcodec.EncodeMessage`, `toolcodec.DecodeResult` and `toolcodec.ParseError` are the encodings the transport uses.

### Request size limit

`connectmcp.WithReadMaxBytes(n)` is the counterpart of `connecthttp.WithReadMaxBytes`: it limits the request message, measured as the JSON of the tool call's arguments, to `n` bytes. The default is 4 MiB, as in connecthttp, and zero allows any size. The limit is checked before the arguments are decoded. Larger arguments fail the call, while the handler receives its request message, with connecthttp's own error, and interceptors see it:

```json
{"code": "RESOURCE_EXHAUSTED", "message": "message is larger than configured max 1048576"}
```

The limit does not cover the JSON-RPC envelope or the HTTP request body, which go-sdk reads in full before the tool is called. To bound those too, wrap the handler with `http.MaxBytesHandler` at a limit with headroom above `n`. go-sdk answers a body over that limit with a plain-text HTTP 400.

### Using grpc-go

grpc-go is not supported: tools are served from a connect-go v2 `*connect.Server`.

### Tool filtering

`connectmcp.WithToolFilter` registers only the annotated tools for which it returns true, for example to gate writes behind a feature flag.:

```go
connectmcp.NewHandler(server,
    connectmcp.WithToolFilter(func(toolName string) bool { return !writes[toolName] }),
)
```

### Tool annotations

MCP tools carry [annotations](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-annotations): a display title and hints that clients use to decide, for example, which calls to run without asking the user. A tool without hints is assumed to be possibly destructive and to reach into an open world, so annotate every tool.

Whether a method has side effects is declared with the standard `idempotency_level` option, the same declaration Connect uses for GET requests (and [AIP](https://google.aip.dev/) for REST). `connectmcp` derives the hints from it:

| `idempotency_level` | `readOnlyHint` | `idempotentHint` | `destructiveHint` |
|---|---|---|---|
| `NO_SIDE_EFFECTS` | `true` | `true` | `false` |
| `IDEMPOTENT` | | `true` | |
| unset | | | |

The `(connectmcp.v1.tool_annotations)` option adds what `idempotency_level` cannot express: the `title` clients display instead of the tool name, whether a method with side effects is destructive (`destructive_hint`, `false` for one that only adds data), and whether it reaches outside the service's own domain (`open_world_hint`). A service that manages workspaces:

```proto
service WorkspaceService {
  rpc GetWorkspace(GetWorkspaceRequest) returns (Workspace) {
    option idempotency_level = NO_SIDE_EFFECTS;
    option (connectmcp.v1.tool_description) = "Look up one workspace by its ID.";
    option (connectmcp.v1.tool_annotations) = {title: "Get workspace", open_world_hint: false};
  }
  rpc CreateWorkspace(CreateWorkspaceRequest) returns (Workspace) {
    option (connectmcp.v1.tool_description) = "Create a new workspace.";
    option (connectmcp.v1.tool_annotations) = {title: "Create workspace", destructive_hint: false, open_world_hint: false};
  }
  rpc UpdateWorkspace(UpdateWorkspaceRequest) returns (Workspace) {
    option idempotency_level = IDEMPOTENT;
    option (connectmcp.v1.tool_description) = "Replace the fields of a workspace.";
    option (connectmcp.v1.tool_annotations) = {title: "Update workspace", open_world_hint: false};
  }
  rpc DeleteWorkspace(DeleteWorkspaceRequest) returns (DeleteWorkspaceResponse) {
    option idempotency_level = IDEMPOTENT;
    option (connectmcp.v1.tool_description) = "Delete a workspace and all of its data.";
    option (connectmcp.v1.tool_annotations) = {title: "Delete workspace", destructive_hint: true, open_world_hint: false};
  }
}
```

is listed as:

| Tool | `title` | `readOnlyHint` | `idempotentHint` | `destructiveHint` | `openWorldHint` |
|---|---|---|---|---|---|
| `GetWorkspace` | Get workspace | `true` | `true` | `false` | `false` |
| `CreateWorkspace` | Create workspace | | | `false` | `false` |
| `UpdateWorkspace` | Update workspace | | `true` | (default `true`) | `false` |
| `DeleteWorkspace` | Delete workspace | | `true` | `true` | `false` |

The title is sent both as the tool's `title` and as `annotations.title`, for clients of older MCP versions. `destructive_hint: true` on a `NO_SIDE_EFFECTS` method is a contradiction, and `NewHandler` returns an error for it. The full example is [`tool_annotations.proto`](pkg/testdata/proto/testdata/tool_annotations.proto).

To set annotations in code, for example a default for every tool or for a forwarded service whose protos you do not own, use `connectmcp.WithToolAnnotations`. It is called for every registered tool with the annotations derived from the protos, which it may change. These changes are not checked against `idempotency_level`:

```go
connectmcp.NewHandler(server,
    // Every tool works on this service's data only, unless its proto says otherwise.
    connectmcp.WithToolAnnotations(func(_ string, _ protoreflect.MethodDescriptor, a *mcpserver.ToolAnnotations) {
        if a.OpenWorldHint == nil {
            a.OpenWorldHint = proto.Bool(false)
        }
    }),
)
```

Annotations are hints for clients, not a security boundary: authorize tool calls in an interceptor.

### Tool naming

By default a tool is named after the method's full proto name with dots replaced by underscores (`redpanda_mcps_sql_v1_SQLService_Query`). Names longer than 64 characters — the strictest provider limit — are truncated: a 10-character hash of the full name plus as much of the tail (the most specific part) as fits. The hash is forced to start with a letter because Gemini rejects function names that start with a digit.

To publish a method under a stable, human-chosen name instead, set the `(connectmcp.v1.tool_name)` method option:

```proto
import "connectmcp/v1/annotations.proto";

rpc GetEquityResearchReport(GetEquityResearchReportRequest) returns (GetEquityResearchReportResponse) {
  option (connectmcp.v1.tool_name) = "get_equity_research_report";
}
```

The value must match `^[a-z_][a-z0-9_-]{0,63}$` — lowercase snake/kebab case within every provider's function-name rules; lowercase guarantees configured names pass untouched through consumers' de-mangling heuristics (they key on the UpperCamel `Service_` segment of derived names). `connectmcp` returns an error for an invalid value and for two tools with the same name.

#### Importing the annotation with Buf

Depend on the annotation through the Buf Schema Registry instead of vendoring it:

```yaml
# buf.yaml
deps:
  - buf.build/advdv/connectmcp
```

In managed mode, keep its `go_package` so your generated `*.pb.go` code imports the `connectmcpv1` package from this Go module. Don't generate Go code for it yourself, because a second copy conflicts at registration:

```yaml
# buf.gen.yaml
managed:
  disable:
    - file_option: go_package
      module: buf.build/advdv/connectmcp
```

To match the module version in `go.mod`, pin the label for the same Git commit: `buf.build/advdv/connectmcp:<git-sha>`.

## Development & Testing

### Tooling

Developer tools (Go, just, bazelisk, buf, golangci-lint, gofumpt, gh) are pinned in `mise.toml`. Install them with [mise](https://mise.jdx.dev):

```bash
mise install
```

### Commands

All commands use [just](https://github.com/casey/just):

```bash
just build                # Build everything (Bazel)
just test-unit            # Unit tests (no API keys needed)
just test                 # All tests including conformance/integration (needs API keys)
just test-cover           # Tests with coverage report
just generate             # Regenerate the annotation and test proto code
just lint                 # Run golangci-lint
just fmt                  # Format code
just gazelle              # Sync BUILD files from go.mod

# View all available commands
just --list
```

### Development Workflow

```bash
# 1. Edit protos or code
# 2. Regenerate
just generate
# 3. Run tests
just test-unit
```

## Proto-to-JSON Schema mapping

Protobuf types are mapped to JSON Schema. Understanding these mappings matters because LLMs see the schema, not your proto definitions.

### Scalar types

| Proto type | JSON Schema type | Notes |
|---|---|---|
| `double`, `float` | `number` | |
| `int32`, `uint32`, `sint32`, `fixed32`, `sfixed32` | `integer` | |
| `int64`, `uint64`, `sint64`, `fixed64`, `sfixed64` | `string` | JSON cannot represent 64-bit integers precisely |
| `bool` | `boolean` | |
| `string` | `string` | |
| `bytes` | `string` | `contentEncoding: base64` |
| `enum` | `string` | `enum` array with all value names |

### Messages and maps

Nested messages become nested `object` schemas. `map<K, V>` becomes a JSON object whose values have the schema of `V`, with `propertyNames` constraining the keys (a `pattern` for integer keys, an `enum` for bool keys).

### Well-known types

| Proto type | JSON Schema |
|---|---|
| `google.protobuf.Timestamp` | `string` with `format: date-time` |
| `google.protobuf.Duration` | `string` with a duration pattern |
| `google.protobuf.Struct` | `object` with `additionalProperties: true` |
| `google.protobuf.Value` | any (no type constraint) |
| `google.protobuf.ListValue` | `array` |
| `google.protobuf.FieldMask` | `string` |
| `google.protobuf.Any` | `object` with `@type` + `value` |
| Wrapper types (`StringValue`, etc.) | nullable scalar (e.g., `["string", "null"]`) |

### Oneof fields

A oneof is one property, named after the oneof: an object with a `which` discriminator that names the member that is set, and one property per member. Providers reject a union (`anyOf`/`oneOf`) at the top of a tool's input schema, which is why it is an object.

```json
"item_type": {
  "type": "object",
  "description": "Exactly one of the \"item_type\" group. Set \"which\" to the chosen field name, then set only that field.",
  "required": ["which"],
  "properties": {
    "which": {"type": "string", "enum": ["product", "service"], "description": "Which field of the \"item_type\" oneof is set."},
    "product": {"type": "object", "description": "The value when which=\"product\".", "...": "..."},
    "service": {"type": "object", "description": "The value when which=\"service\".", "...": "..."}
  }
}
```

The arguments of a call are turned back into the oneof before they are decoded, and results are written in the same shape. A oneof with `(buf.validate.oneof).required` is in the parent's `required`.

### Validation rules

[protovalidate](https://buf.build/bufbuild/protovalidate) rules (`buf.validate`) and example values are part of the schema, so the model knows what a valid value looks like before it calls the tool. See [`examples/basic`](examples/basic/README.md) for a request with most kinds of rules and its schema.

The rules are found by reflection over the rule messages, not from a list of supported rules, so every rule that is set is in the schema: standard rules, the rules of list items and of map keys and values, predefined rules, and CEL rules. Each rule is in one place.

**A JSON Schema keyword**, when one accepts exactly what the rule accepts:

| Rule | JSON Schema |
|---|---|
| `const` | `const` (64-bit integers as a string, `bytes` as base64, a `field_mask` as its string) |
| `in` | `enum` |
| `gte` / `lte` | `minimum` / `maximum` |
| `gt` / `lt` on an integer | `minimum` + 1 / `maximum` - 1 |
| `gt` / `lt` on a `float` or `double` | `exclusiveMinimum` / `exclusiveMaximum` |
| `string.len`, `min_len`, `max_len` | `minLength` / `maxLength` |
| `string.pattern` | `pattern` |
| `string.email`, `hostname`, `ipv4`, `ipv6`, `uri`, `uri_ref`, `uuid` | `format` |
| `enum.const`, `in`, `not_in` | the listed `enum` names are narrowed to the allowed ones |
| `repeated.min_items`, `max_items` | `minItems` / `maxItems` |
| `repeated.items` | the same mapping, in `items` |
| `map.min_pairs`, `max_pairs` | `minProperties` / `maxProperties` |
| `map.keys`, `map.values` | the same mapping, in `propertyNames` / `additionalProperties` |
| `example` | `examples` |
| `(buf.validate.field).required` | the field is in the parent's `required`, like `(google.api.field_behavior) = REQUIRED` |

**A line in the `description`** for every other rule, after `Rules:`. A standard rule is written as its name and value, with the value in its JSON form:

```json
{"type": "string", "description": "Rules:\n- string.prefix = \"x-\"\n- string.not_in = [\"x-admin\"]"}
{"type": ["string", "null"], "format": "date-time", "description": "Rules: timestamp.gt_now"}
```

This is also where a rule goes when a keyword would be wrong: the bounds of a 64-bit integer or a numeric map key (JSON strings, which `minimum` does not apply to), and bounds that protovalidate reads as "outside the range" (`gt: 10, lt: 5`). `repeated.unique` is described too, because OpenAI rejects a tool schema with `uniqueItems`.

**CEL rules** are described by their `message`. A rule without one computes its message in the expression, so the expression is shown instead:

- `(buf.validate.field).cel` and `cel_expression` are lines in the description of the field;
- `(buf.validate.message).cel`, `cel_expression` and `oneof` are lines in the description of the message, which for the request itself is the description of the input schema;
- a predefined rule, an extension of a rule message that you declare with `(buf.validate.predefined)`, is its name and value followed by the messages of its CEL rules: `uuid_name = "workspaces": value must be a resource name with a UUID in lower case as its ID`.

**Modifiers.** With `ignore = IGNORE_IF_ZERO_VALUE` the keywords are kept and the description says that the rules are skipped when the value is unset or zero. With `IGNORE_ALWAYS` only the examples are kept.

Three things to know:

- The schema informs, it does not enforce: tool arguments are not validated against it. Add a protovalidate interceptor to the connect server; its violations reach the model as an `INVALID_ARGUMENT` tool error.
- Descriptions cost tokens in every `tools/list`. Rules with a keyword add no description, and a field without rules adds nothing.
- A rule that the binary cannot decode is a registration error instead of a gap in the schema. That is a predefined rule whose Go package is not linked into the binary, or a rule newer than the protovalidate Go module (`buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go`) in your `go.mod`.

What a field means is not a rule: describe it with [`(connectmcp.v1.field_description)`](#describe-the-fields), whose text comes before the rules in the description. Field comments are not in the schema, because the descriptors compiled into a Go binary do not carry them.

### Recursive messages

Self-referencing and mutually recursive messages (e.g., tree nodes, linked lists) are supported. The schema expands up to 3 levels deep with full field detail. Beyond that, recursive fields become `{"type": "string", "description": "JSON-encoded TreeNode. Provide a JSON object as a string."}`.

Such a string is parsed back into an object before the arguments are decoded. The actual data can nest arbitrarily deep -- the depth limit only applies to the schema the LLM sees, not to what it can send.

### Tool name mangling

If the fully qualified RPC name (dots replaced with underscores) exceeds 64 characters, the name is truncated: the head is replaced with a 10-character SHA-256 hash prefix, preserving the tail (the most specific part, typically `ServiceName_MethodName`). The 64-char limit exists because Claude desktop enforces it.

## Limitations

- Only unary RPCs are exposed as tools.
- Recursive message schemas lose field-level detail beyond 3 levels of nesting (see above). The LLM must encode deeper levels as JSON strings.

## Feedback

We'd love feedback, bug reports, or PRs! Join the discussion and help shape the future of Go and Protobuf MCP tooling.
