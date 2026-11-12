# Basic Example

Serves the `TestService` from `pkg/testdata` as MCP tools over stdio, using
`connectmcp.Register` and the official MCP go-sdk. The methods are tools because
they carry the `(mcp.v1.tool_description)` option in `test_service.proto`.

```bash
go run .
```

See `../connect` for serving MCP over HTTP next to the connect API itself.

## Test / list tools

[f/mcptools](https://github.com/f/mcptools) can be useful.

Run `mcptools tools go run .`
