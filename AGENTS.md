# protoc-gen-go-mcp

Protoc plugin + runtime library for generating MCP (Model Context Protocol) server handlers from gRPC service definitions.

## Build & Test

Tool versions (go, just, bazelisk, buf, golangci-lint, gofumpt) are pinned in `mise.toml`; run `mise install`. Keep `go` in sync with `go.mod`.

```bash
just build                # bazelisk build //...
just test-unit            # bazelisk test //... (no API keys needed)
just test                 # All tests including conformance/integration (needs API keys)
just test-cover           # Tests with coverage report
just generate             # Regenerate proto code + descriptor set
just lint                 # Run golangci-lint
just conformancetest      # Run conformance tests against LLM providers (needs API keys)
just gazelle              # Sync BUILD files from go.mod
```

## Architecture

```
cmd/protoc-gen-go-mcp/     Entry point for protoc plugin
pkg/gen/                    Core library (THE important package):
  schema.go                 JSON schema generation from protoreflect descriptors
  register.go               Dynamic MCP tool registration at runtime
pkg/generator/              Protoc plugin: Go template output, delegates to pkg/gen
pkg/runtime/                MCPServer interface, FixOpenAI, error handling, extra properties
  server.go                 MCPServer interface + Tool/CallToolRequest/CallToolResult types
  mark3labs/                Adapter: mark3labs/mcp-go -> runtime.MCPServer
  gosdk/                    Adapter: modelcontextprotocol/go-sdk -> runtime.MCPServer
pkg/testdata/               Proto files + generated code for testing
conformancetest/            E2E tests against real LLM providers (Gemini, OpenAI, Anthropic)
```

### Two modes of operation

1. **Static (codegen)**: `protoc-gen-go-mcp` generates `*.pb.mcp.go` with pre-computed schemas
2. **Dynamic (runtime)**: `gen.RegisterService()` creates MCP tools from any `protoreflect.ServiceDescriptor` at runtime - no codegen needed. This is the proxy/gateway mode.

## Key APIs

### Creating an MCP server (pick your library)
```go
// Official go-sdk
raw, s := gosdk.NewServer("name", "1.0")    // s is runtime.MCPServer
raw.Run(ctx, &mcp.StdioTransport{})

// mark3labs/mcp-go
raw, s := mark3labs.NewServer("name", "1.0") // s is runtime.MCPServer
server.ServeStdio(raw)
```

### Static registration (from generated code)
```go
testdatamcp.RegisterTestServiceHandler(s, myServiceImpl)
testdatamcp.RegisterTestServiceHandlerOpenAI(s, myServiceImpl)
```

### Dynamic registration (runtime, no codegen)
```go
gen.RegisterService(s, serviceDescriptor, handler, gen.RegisterServiceOptions{
    Provider:   runtime.LLMProviderOpenAI,
    NewMessage: func(md protoreflect.MessageDescriptor) proto.Message { ... },
})
```

### Schema generation (library use)
```go
schema := gen.MessageSchema(msgDescriptor, gen.SchemaOptions{OpenAICompat: true})
standard, openAI := gen.ToolForMethod(methodDescriptor, "description")
```

## Key Design Decisions

- Two schema modes: standard MCP and OpenAI-compatible (`gen.SchemaOptions`)
- OpenAI mode: maps -> arrays of KV pairs, all fields required, additionalProperties: false
- Well-known types (Struct, Value, ListValue) become JSON strings in OpenAI mode
- Tool names > 64 chars get hash-mangled (Claude desktop limit)
- `pkg/gen` is fully independent of protoc - works with any protoreflect descriptor
- Generated code and runtime are MCP-library-agnostic via runtime.MCPServer interface
- Adapter packages (runtime/mark3labs, runtime/gosdk) bridge to concrete MCP libraries
- Golden test re-runs generator in-process from compiled descriptors, no shell/buf at test time

## Testing

- Unit tests: `go test ./pkg/...` (with -race, always)
- Conformance tests: `go test ./conformancetest/` (needs API keys, tests skip if missing)
- Golden test: in-process generator re-run vs checked-in `gen/go/*.pb.mcp.go`
- Edge case protos: `pkg/testdata/proto/testdata/edge_cases.proto`
- Fuzz tests: `pkg/runtime/fix_fuzz_test.go`, `pkg/gen/schema_fuzz_test.go`

## Proto Generation

Uses `buf`. Test protos in `pkg/testdata/proto/`.
After changing protos: `just generate`.

## Development workflow

1. Edit proto or generator code
2. `just generate` (regenerates test proto Go code + descriptor set)
3. `just test-unit` (runs unit tests, including golden comparison)

## Releasing

Releases are cut by pushing a `vX.Y.Z` tag on main; `.github/workflows/release.yml` then:

- builds the plugin binaries and creates the GitHub release;
- pushes `proto/` to `buf.build/advdv/protoc-gen-go-mcp` labeled with the tag, the commit SHA, and `main`.

The BSR `main` label is the module's default label and is moved **only** by releases
(`test.yml` pushes main-branch commits under their SHA label only). An unqualified
`buf.build/advdv/protoc-gen-go-mcp` dependency therefore resolves to the latest release.

Unless asked otherwise, a release bumps the **patch** component of the latest tag
(e.g. `v0.1.3` -> `v0.1.4`). To release:

1. Ensure main is pushed and its `Test` workflow is green.
2. `git tag vX.Y.Z && git push origin vX.Y.Z`
3. Monitor the `Release` workflow until it succeeds (`gh run watch`).
4. Verify the BSR `main` label moved to the release commit, e.g.
   `buf registry module label info buf.build/advdv/protoc-gen-go-mcp:main` matches the commit of
   `:vX.Y.Z`, and that a scratch module depending on unqualified
   `buf.build/advdv/protoc-gen-go-mcp` resolves (`buf dep update`) to that same commit in `buf.lock`.
