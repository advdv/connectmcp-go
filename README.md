# `protoc-gen-go-mcp`

> [!WARNING]
> This repository is a fork of [redpanda-data/protoc-gen-go-mcp](https://github.com/redpanda-data/protoc-gen-go-mcp) meant for internal use. Do not rely on its stability: it may change or break without notice. Use the [upstream repository](https://github.com/redpanda-data/protoc-gen-go-mcp) instead.

> [!WARNING]
> The changes in this fork were made with the help of AI coding tools. The code has been reviewed by a human.

[![Test](https://github.com/advdv/protoc-gen-go-mcp/actions/workflows/test.yml/badge.svg)](https://github.com/advdv/protoc-gen-go-mcp/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/advdv/protoc-gen-go-mcp)](https://goreportcard.com/report/github.com/advdv/protoc-gen-go-mcp)
[![codecov](https://codecov.io/gh/redpanda-data/protoc-gen-go-mcp/branch/main/graph/badge.svg)](https://codecov.io/gh/redpanda-data/protoc-gen-go-mcp)

This module serves [ConnectRPC](https://connectrpc.com) APIs as [Model Context Protocol (MCP)](https://modelcontextprotocol.io) servers, built for [connect-go v2](https://connectrpc.com/docs/go/migrating-to-v2/).

Its `connectmcp` package takes your `*connect.Server` and returns an `http.Handler` that exposes annotated RPCs as MCP tools. It is a peer of `connecthttp`, which serves the same server over Connect and gRPC, and of [vanguard-go](https://github.com/connectrpc/vanguard-go), which serves it over REST. It works at runtime from the protobuf descriptors of the registered services: there is no protoc plugin and no generated MCP code.

> [!NOTE]
> The module is still named `protoc-gen-go-mcp` after the upstream project, which is a protoc plugin. This fork no longer contains one.

## Changes from upstream

Forked from upstream at [`6334690`](https://github.com/redpanda-data/protoc-gen-go-mcp/commit/6334690).

- **No protoc plugin.** Tools are built at runtime from the descriptors of a `*connect.Server` (`connectmcp.NewHandler` / `connectmcp.Register`); no `*.pb.mcp.go` files are generated.
- **Opt-in tools.** Only methods annotated with `(mcp.v1.tool_description)` are exposed; misconfiguration (empty description, streaming method, invalid or duplicate name) is a registration error.
- **connect-go v2 only.** Tool calls go through `connect.Server.Call`, so interceptors run and HTTP headers become request headers. gRPC and the mark3labs/mcp-go adapter are removed; only the official go-sdk is supported.
- **Correctable argument errors.** Undecodable arguments are returned as a tool error naming the offending field and the expected shape, so the model can retry.
- **Removed:** descriptor-only `gen.RegisterService`, extra properties, the tool name prefix and `runtime.Option` (the filter is now `connectmcp.WithToolFilter`).
- **Module and proto.** Go module is `github.com/advdv/protoc-gen-go-mcp`; the annotation proto is published to `buf.build/advdv/protoc-gen-go-mcp` on each release.
- **Tooling.** Tools pinned with mise, reproducible `just generate`, golangci-lint in CI, GitHub-hosted runners.

## Features

- Serves a connect-go v2 server as MCP in one call, through its interceptors
- Tools are selected and described with a proto option
- JSON Schema for tool inputs and outputs derived from the request and response messages
- Tool calls can be forwarded to a remote connect server
- Built on [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) (the official SDK)

## Usage

### Annotate the RPCs to expose

A method becomes a tool when it carries the `(mcp.v1.tool_description)` option. Methods without it are not exposed.

```proto
import "mcp/v1/annotations.proto";

service ItemService {
  rpc GetItem(GetItemRequest) returns (GetItemResponse) {
    option (mcp.v1.tool_description) = "Look up one item by its ID.";
  }
}
```

The description is what the model sees. It is a proto option rather than the RPC's comment because the descriptors compiled into a Go binary carry options but not comments. See [Importing the annotation with Buf](#importing-the-annotation-with-buf) for how to depend on the annotation.

Generate the standard `*.pb.go` and connect-go v2 code as usual; nothing else is generated.

### Serve MCP

```go
import (
    "connectrpc.com/connect/v2"
    "connectrpc.com/connect/v2/connecthttp"
    "github.com/advdv/protoc-gen-go-mcp/pkg/connectmcp"
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
- `NewHandler` returns an error if no method is annotated, a description is empty, an annotated method is streaming, a configured tool name is invalid, or two tools share a name.

Options:

```go
connectmcp.NewHandler(server,
    connectmcp.WithServerInfo("items", "1.0.0"),
    connectmcp.WithStreamableHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}),
)
```

See [`examples/connect`](examples/connect) for a runnable version.

### Other transports, prompts and resources

`NewHandler` is a convenience around `connectmcp.Register`, which adds the tools to an MCP server you own. Use it to serve over stdio or to add prompts and resources. It programs against the one-method `runtime.MCPServer` interface; the `gosdk` package adapts the official go-sdk server to it:

```go
// raw is *mcp.Server for transport, s is runtime.MCPServer for tools.
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

### Errors

An error returned from the RPC is given to the model as a tool error (`isError: true`) containing JSON. For a `*connect.Error` anywhere in the error chain this carries its code, message and details:

```json
{
  "code": "NOT_FOUND",
  "message": "no such item",
  "details": [{"@type": "type.googleapis.com/google.rpc.ResourceInfo", "resourceType": "item", "resourceName": "item-1"}]
}
```

Any other error is reported with code `UNKNOWN` and its `Error()` text, so in a local handler use `connect.NewError(...)` and keep internals in `.WithCause(err)`, which is never shown to the model.

### Using grpc-go

grpc-go is not supported: tools are served from a connect-go v2 `*connect.Server`.

### Tool filtering

`connectmcp.WithToolFilter` registers only the annotated tools for which it returns true, for example to gate writes behind a feature flag.:

```go
connectmcp.NewHandler(server,
    connectmcp.WithToolFilter(func(toolName string) bool { return !writes[toolName] }),
)
```

### Tool naming

By default a tool is named after the method's full proto name with dots replaced by underscores (`redpanda_mcps_sql_v1_SQLService_Query`). Names longer than 64 characters — the strictest provider limit — are truncated: a 10-character hash of the full name plus as much of the tail (the most specific part) as fits. The hash is forced to start with a letter because Gemini rejects function names that start with a digit.

To publish a method under a stable, human-chosen name instead, set the `(mcp.v1.tool_name)` method option:

```proto
import "mcp/v1/annotations.proto";

rpc GetEquityResearchReport(GetEquityResearchReportRequest) returns (GetEquityResearchReportResponse) {
  option (mcp.v1.tool_name) = "get_equity_research_report";
}
```

The value must match `^[a-z_][a-z0-9_-]{0,63}$` — lowercase snake/kebab case within every provider's function-name rules; lowercase guarantees configured names pass untouched through consumers' de-mangling heuristics (they key on the UpperCamel `Service_` segment of derived names). `connectmcp` returns an error for an invalid value and for two tools with the same name.

#### Importing the annotation with Buf

Depend on the annotation through the Buf Schema Registry instead of vendoring it:

```yaml
# buf.yaml
deps:
  - buf.build/advdv/protoc-gen-go-mcp
```

In managed mode, keep its `go_package` so your generated `*.pb.go` code imports the `mcpv1` package from this Go module. Don't generate Go code for it yourself, because a second copy conflicts at registration:

```yaml
# buf.gen.yaml
managed:
  disable:
    - file_option: go_package
      module: buf.build/advdv/protoc-gen-go-mcp
```

To match the module version in `go.mod`, pin the label for the same Git commit: `buf.build/advdv/protoc-gen-go-mcp:<git-sha>`.

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

Nested messages become nested `object` schemas. Maps depend on the mode:

**Standard mode:** `map<K, V>` becomes a JSON object with `propertyNames` constraints (e.g., `pattern` for int keys, `enum` for bool keys).

**OpenAI mode:** `map<K, V>` becomes an array of `{key, value}` objects. Key constraints (patterns, enums) are preserved on the `key` field. At runtime, `FixOpenAI` converts these arrays back to objects before proto unmarshaling.

### Well-known types

| Proto type | Standard schema | OpenAI schema |
|---|---|---|
| `google.protobuf.Timestamp` | `string` with `format: date-time` | Same |
| `google.protobuf.Duration` | `string` with duration pattern | Same |
| `google.protobuf.Struct` | `object` with `additionalProperties: true` | `string` (JSON-encoded) |
| `google.protobuf.Value` | any (no type constraint) | `string` (JSON-encoded) |
| `google.protobuf.ListValue` | `array` | `string` (JSON-encoded) |
| `google.protobuf.FieldMask` | `string` | Same |
| `google.protobuf.Any` | `object` with `@type` + `value` | Same, with `additionalProperties: false` |
| Wrapper types (`StringValue`, etc.) | nullable scalar (e.g., `["string", "null"]`) | Same |

In OpenAI mode, `FixOpenAI` handles the reverse transformation at runtime: JSON-encoded strings are parsed back, wrapper objects `{"value": X}` are unwrapped to `X`, and map arrays are converted to objects.

### Oneof fields

**Standard mode:** Uses JSON Schema `anyOf` with one entry per oneof group. Each entry specifies one allowed alternative.

**OpenAI mode:** Oneof fields are flattened into nullable fields (e.g., `type: ["string", "null"]`) with a description noting the oneof constraint. At runtime, `FixOpenAI` strips null oneof alternatives and keeps only the first non-null field per group.

### Validation constraints

[buf.validate](https://buf.build/bufbuild/protovalidate) annotations are mapped to JSON Schema keywords:

| Validation | JSON Schema |
|---|---|
| `string.uuid` | `format: uuid` |
| `string.email` | `format: email` |
| `string.pattern` | `pattern` |
| `string.min_len/max_len` | `minLength/maxLength` |
| `int32.gte/lte` | `minimum/maximum` |
| `int32.gt/lt` | `minimum+1 / maximum-1` |
| `float.gt/lt` | `exclusiveMinimum/exclusiveMaximum` |
| `double.gte/lte` | `minimum/maximum` |

### Recursive messages

Self-referencing and mutually recursive messages (e.g., tree nodes, linked lists) are supported. The schema expands up to 3 levels deep with full field detail. Beyond that, recursive fields become `{"type": "string", "description": "JSON-encoded TreeNode. Provide a JSON object as a string."}` -- the same pattern used for `Struct`/`Value`/`ListValue` in OpenAI mode.

At runtime, `FixOpenAI` parses these string-encoded fields back into JSON objects before proto unmarshaling. The actual data can nest arbitrarily deep -- the depth limit only applies to the schema the LLM sees, not to what it can send.

### Tool name mangling

If the fully qualified RPC name (dots replaced with underscores) exceeds 64 characters, the name is truncated: the head is replaced with a 10-character SHA-256 hash prefix, preserving the tail (the most specific part, typically `ServiceName_MethodName`). The 64-char limit exists because Claude desktop enforces it.

## Limitations

- Only unary RPCs are exposed as tools.
- Recursive message schemas lose field-level detail beyond 3 levels of nesting (see above). The LLM must encode deeper levels as JSON strings.

## Feedback

We'd love feedback, bug reports, or PRs! Join the discussion and help shape the future of Go and Protobuf MCP tooling.
