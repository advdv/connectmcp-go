# Basic Example

Serves the `TestService` from `pkg/testdata` as MCP tools over stdio, using the
generated handlers in `pkg/testdata/gen/go/testdata/testdatamcp` (created by
`just generate`) and the official MCP go-sdk.

```bash
go run .
```

See `../connect` for exposing a connect-go v2 server or client as MCP tools.

## Test / list tools

[f/mcptools](https://github.com/f/mcptools) can be useful.

Run `mcptools tools go run .`
