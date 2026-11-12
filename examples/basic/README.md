# Basic Example

Serves the `TestService` from `pkg/testdata` as MCP tools over stdio, using
`connectmcp.Register` and the official MCP go-sdk. The methods are tools because
they carry the `(connectmcp.v1.tool_description)` option in `test_service.proto`.

```bash
go run .
```

The tools are annotated from the protos, so clients can tell a read from a
write. `GetItem` declares `option idempotency_level = NO_SIDE_EFFECTS;` and a
title, and `CreateItem` says with `(connectmcp.v1.tool_annotations)` that it adds
data without destroying any. `tools/list` returns:

```json
{"name": "testdata_TestService_GetItem", "title": "Get item",
 "annotations": {"title": "Get item", "readOnlyHint": true, "idempotentHint": true, "destructiveHint": false}, ...}
{"name": "testdata_TestService_CreateItem", "title": "Create item",
 "annotations": {"title": "Create item", "destructiveHint": false, "openWorldHint": false}, ...}
```

The other two tools carry no annotations, so clients assume MCP's defaults
(possibly destructive, open world).

See `../connect` for serving MCP over HTTP next to the connect API itself.

## Test / list tools

[f/mcptools](https://github.com/f/mcptools) can be useful.

Run `mcptools tools go run .`
