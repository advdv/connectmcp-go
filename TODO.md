# TODO

## Bugs (all have reproducers in test files)

- [x] **Recursive message stack overflow** -- `MessageSchema` infinite-loops on self-referencing messages. Fixed: recursion guard with visited set in `messageSchema`.
- [x] **Non-string map keys silently dropped** -- `FixOpenAI` dropped int/bool map keys. Fixed: type-aware coercion (float64->int string, bool->string).
- [x] **OpenAI map schema drops key constraints** -- Fixed: use computed `keyConstraints` instead of hardcoded `{"type":"string"}`.
- [x] **WKT wrapper types not unwrapped** -- `FixOpenAI` didn't handle `{"value": X}` form for wrapper types. Fixed: unwrap to plain scalar.
- [x] **Nil NewMessage panics server** -- Fixed: nil check + error return before `protojson.Unmarshal`.
- [x] **Oneof type assertion silent failure** -- Fixed: handle both `string` and `[]string` type values in oneof null-wrapping.
- [x] **Over-broad null on all OpenAI message schemas** -- Fixed: removed blanket `["object","null"]` addition; nullable only for oneof fields.
- [x] **Null map values break protojson** -- Fixed: skip nil values in map KV conversion.
- [x] **Both oneof alternatives in OpenAI mode** -- Fixed: strip null oneof alternatives, keep first non-null.
- [x] **MangleHeadIfTooLong collision** -- Fixed: SHA-256 with 10-char base36 prefix (~51 bits).
- [x] **Extra properties leak into proto unmarshal** -- Fixed: `delete(message, prop.Name)` after extraction.
- [x] **Wrapped connect error loses context** -- Fixed: preserve `err.Error()` when it differs from inner `connectErr.Error()`.

## Migration

- [x] **Migrate from mark3labs/mcp-go to modelcontextprotocol/go-sdk** -- runtime.MCPServer interface with a go-sdk adapter; the mark3labs adapter has been removed.
- [x] **Native connect-go v2** -- connect-only (no gRPC or connect v1 forwarders).
- [x] **Runtime only** -- the protoc plugin and generated `*.pb.mcp.go` are gone; `connectmcp` serves a `*connect.Server` from its descriptors, with tools selected by `(mcp.v1.tool_description)`.

## Bazel

- [x] MODULE.bazel, .bazelrc, .bazelversion
- [x] BUILD.bazel for all Go packages
- [x] Unit tests pass in Bazel (`bazelisk test //...`)
- [x] Conformance + integration tests build in Bazel (tagged `manual`, need API keys via `--test_env`)
- [x] CI integration (GitHub Actions with bazelisk)
