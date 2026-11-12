# connectmcp-go

Runtime library that serves a connect-go v2 (`connectrpc.com/connect/v2`) `*connect.Server` as an MCP
(Model Context Protocol) server. Module `github.com/advdv/connectmcp-go`, package `connectmcp` at the
root (like `connectrpc.com/vanguard`). Started as a fork of redpanda-data/protoc-gen-go-mcp; there is no
protoc plugin and no generated MCP code any more.

## Build & Test

Tool versions (go, just, bazelisk, buf, golangci-lint, gofumpt, gh) are pinned in `mise.toml`; run `mise install`. Keep `go` in sync with `go.mod`.

```bash
just build                # bazelisk build //...
just test-unit            # bazelisk test //... (no API keys needed)
just test                 # All tests including conformance/integration (needs API keys)
just test-cover           # Tests with coverage report
just generate             # Regenerate annotation + test proto code
just lint                 # Run golangci-lint
just conformancetest      # Run conformance tests against LLM providers (needs API keys)
just gazelle              # Sync BUILD files from go.mod
```

## Architecture

```
connectmcp.go               Package connectmcp (//:connectmcp-go), THE entry point: NewHandler (http.Handler) and Register, on a *connect.Server
pkg/gen/                    Core library:
  schema.go                 JSON schema generation from protoreflect descriptors
  toolname.go               Tool names and the (connectmcp.v1.*) method options
pkg/runtime/                MCPServer interface, argument decoding, error handling (connect v2 errors)
  server.go                 MCPServer interface + Tool/CallToolRequest/CallToolResult types
  gosdk/                    Adapter: modelcontextprotocol/go-sdk -> runtime.MCPServer
pkg/connectmcpv1/           Generated Go for proto/connectmcp/v1/annotations.proto
proto/                      The annotations proto, published to the BSR
pkg/testdata/               Test protos + generated pb/connect code
internal/mcptest/           In-memory go-sdk MCP server for tests, driven by raw JSON-RPC messages
internal/tools/             Blank imports pinning the nogo analyzers in go.mod
conformancetest/            E2E tests against real LLM providers (Gemini, OpenAI, Anthropic)
```

### How it works

`connectmcp.NewHandler(server)` exposes every method registered on a `*connect.Server` that carries
`(connectmcp.v1.tool_description)` as a tool, dispatching through `connect.Server.Call`. Modeled on
vanguard-go's `Mount`. This is the only way to create tools.

## Key APIs

### Serving a connect server as MCP
```go
server := connect.NewServer(interceptors...)
testdataconnect.RegisterTestServiceHandler(server, srv) // srv may be a generated client: forwarding

h, err := connectmcp.NewHandler(server,             // http.Handler, streamable HTTP
    connectmcp.WithServerInfo("name", "1.0"),
    connectmcp.WithStreamableHTTPOptions(&mcp.StreamableHTTPOptions{Stateless: true}),
    connectmcp.WithToolFilter(keep),
    connectmcp.WithReadMaxBytes(1<<20),                 // tool arguments; default 4 MiB like connecthttp
)
```

### Registering on an MCP server you own (stdio, prompts, resources)
```go
raw, s := gosdk.NewServer("name", "1.0")    // s is runtime.MCPServer
err := connectmcp.Register(s, server)
raw.Run(ctx, &mcp.StdioTransport{})
```

### Schema generation (library use)
```go
schema := gen.MessageSchema(msgDescriptor, gen.SchemaOptions{})
tool := gen.ToolForMethod(methodDescriptor, "description")
```

## Key Design Decisions

- Runtime only: everything comes from the descriptors in `connect.Spec.Schema`. Tool selection and
  description are a proto option because Go descriptors carry options but not comments.
- `connectmcp` selects tools by annotation, like vanguard selects REST routes by `google.api.http`.
  Methods without `(connectmcp.v1.tool_description)` are skipped; an empty description, a streaming method,
  an invalid `(connectmcp.v1.tool_name)`, a duplicate tool name, or no annotated method at all is a
  `Register` error. Register reads `server.Specs()` at call time: services must be registered first.
- Tool calls run through `connect.Server.Call` on a ServerStream that decodes the tool arguments into
  the handler's request message, so interceptors always run. HTTP request headers become the RPC's
  request headers; `CallInfo.Protocol` is "mcp".
- Undecodable arguments are an `INVALID_ARGUMENT` tool error in the same JSON shape as every other
  failed call (`runtime.HandleError`), with a `google.rpc.BadRequest` detail when one field is to blame
  (named as in the input schema). Interceptors see that same error; the client gets it regardless of
  how interceptors rewrite it, so the model always learns what to fix.
- `WithReadMaxBytes` (default 4 MiB, 0 = unlimited) limits the raw JSON of the tool arguments, checked
  in `Receive` before decoding, with connecthttp's `RESOURCE_EXHAUSTED` "message is larger than
  configured max N" error, which, like an argument error, the client gets whatever interceptors do.
  `runtime.CallToolRequest.Arguments` is therefore the undecoded `json.RawMessage`. It limits the message,
  not the HTTP body that go-sdk reads in full.
- Forwarding to a remote: register a generated connect client as the handler on a connect server.
- Tool annotations: `readOnlyHint`/`idempotentHint` are derived from the standard `idempotency_level`
  (`NO_SIDE_EFFECTS` -> read-only, idempotent, not destructive; `IDEMPOTENT` -> idempotent), never set
  in the mcp option, so side effects have one source of truth. `(connectmcp.v1.tool_annotations)` adds
  `title`, `destructive_hint`, `open_world_hint`; `destructive_hint: true` on `NO_SIDE_EFFECTS` is a
  `Register` error (`gen.ToolAnnotationsForMethod`). `connectmcp.WithToolAnnotations` adjusts them in
  code, unchecked. The gosdk adapter sends the title as `Tool.title` and `annotations.title`.
- Tool names > 64 chars get hash-mangled (Claude desktop limit)
- `pkg/gen` works with any protoreflect descriptor
- Tool registration is MCP-library-agnostic via the runtime.MCPServer interface; runtime/gosdk
  bridges to the official MCP go-sdk
- Connect-only, no gRPC
- `runtime.HandleError` maps an error to a JSON tool error revealing exactly what connect-go's transports
  put on the wire (`scrubHandlerError` in connecthttp): a local `*connect.Error` keeps its code,
  `Message()` and details (wrapping text and causes are dropped); a remote one is `INTERNAL`, context
  errors are `CANCELED`/`DEADLINE_EXCEEDED`, anything else `UNKNOWN`, all without a message. Forwarding
  servers re-author remote errors in an interceptor to pass them on (README, `connect_e2e_test.go`)

## Testing

- Unit tests: `go test -race ./...` (with -race, always)
- Conformance tests: `go test ./conformancetest/` (needs API keys, tests skip if missing)
- connectmcp API (HTTP handler incl. stateless JSON, header forwarding, registration errors):
  `connectmcp_test.go` (repo root)
- Behavioral tests inherited from the codegen era (schemas, round trips, tool filter,
  connect E2E) live in the root `*_test.go` files; `testsupport_test.go` holds their wiring
- Edge case protos: `pkg/testdata/proto/testdata/edge_cases.proto`
- Tool annotations: `annotations_test.go` on `tool_annotations.proto` (WorkspaceService)
- Fuzz tests: `pkg/runtime/decode_fuzz_test.go`, `pkg/gen/schema_fuzz_test.go`

## Proto Generation

Uses `buf`. Test protos in `pkg/testdata/proto/`; methods must carry `(connectmcp.v1.tool_description)` to
be served by connectmcp. `pkg/testdata/proto/connectmcp/v1/annotations.proto` is a vendored copy of
`proto/connectmcp/v1/annotations.proto`; keep them in sync.
After changing protos: `just generate`.

## Development workflow

1. Edit protos or code
2. `just generate` (only after proto changes)
3. `just test-unit`

## Releasing

Releases are cut by pushing a `vX.Y.Z` tag on main; `.github/workflows/release.yml` then:

- creates the GitHub release;
- pushes `proto/` to `buf.build/advdv/connectmcp` labeled with the tag, the commit SHA, and `main`.

The BSR `main` label is the module's default label and is moved **only** by releases
(`test.yml` pushes main-branch commits under their SHA label only). An unqualified
`buf.build/advdv/connectmcp` dependency therefore resolves to the latest release.

Unless asked otherwise, a release bumps the **patch** component of the latest tag
(e.g. `v0.1.3` -> `v0.1.4`). To release:

1. Ensure main is pushed and its `Test` workflow is green.
2. `git tag vX.Y.Z && git push origin vX.Y.Z`
3. Monitor the `Release` workflow until it succeeds (`gh run watch`).
4. Verify the BSR `main` label moved to the release commit, e.g.
   `buf registry module label info buf.build/advdv/connectmcp:main` matches the commit of
   `:vX.Y.Z`, and that a scratch module depending on unqualified
   `buf.build/advdv/connectmcp` resolves (`buf dep update`) to that same commit in `buf.lock`.
