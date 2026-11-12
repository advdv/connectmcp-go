# Basic Example

Serves the `TestService` and the `ReservationService` from `pkg/testdata` as MCP
tools over stdio, using `connectmcp.Register` and the official MCP go-sdk. The
methods are tools because they carry the `(connectmcp.v1.tool_description)`
option in `test_service.proto` and `validation.proto`.

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

## Validation rules in the schema

The request of `CreateReservation` (`validation.proto`) carries
[protovalidate](https://buf.build/bufbuild/protovalidate) rules and example
values:

```proto
message CreateReservationRequest {
  option (buf.validate.message).cel = {
    id: "reservation.end_after_start"
    message: "end_time must be after start_time"
    expression: "this.end_time > this.start_time"
  };

  string parent = 1 [
    (google.api.field_behavior) = REQUIRED,
    (buf.validate.field).string.(testdata.uuid_name) = "workspaces",
    (buf.validate.field).string.example = "workspaces/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d"
  ];
  string title = 2 [
    (google.api.field_behavior) = REQUIRED,
    (buf.validate.field).string = {min_len: 1, max_len: 120, example: "Quarterly planning"}
  ];
  optional int32 seats = 3 [(buf.validate.field).int32 = {gte: 1, lte: 50, example: 8}];
  google.protobuf.Timestamp start_time = 4 [
    (google.api.field_behavior) = REQUIRED,
    (buf.validate.field).timestamp.gt_now = true
  ];
  google.protobuf.Timestamp end_time = 5 [(google.api.field_behavior) = REQUIRED];
  repeated string attendees = 6 [(buf.validate.field).repeated = {
    max_items: 20, unique: true, items: {string: {email: true}}
  }];
  map<string, string> labels = 7 [(buf.validate.field).map = {
    max_pairs: 8
    keys: {string: {pattern: "^[a-z][a-z0-9_]{0,30}$"}}
    values: {string: {max_len: 64}}
  }];
  Urgency priority = 8 [(buf.validate.field).enum = {defined_only: true, not_in: [0]}];
}
```

`uuid_name` is a predefined rule that `predefined_rules.proto` declares itself,
with the message "value must be a resource name with a UUID in lower case as
its ID". The input schema of the `testdata_ReservationService_CreateReservation`
tool has all of it. A rule with a JSON Schema equivalent is a keyword, any other
rule is a line in a `description`:

```json
{
  "type": "object",
  "description": "Rules: end_time must be after start_time",
  "required": ["parent", "title", "start_time", "end_time"],
  "properties": {
    "parent": {
      "type": "string",
      "description": "Rules: uuid_name = \"workspaces\": value must be a resource name with a UUID in lower case as its ID",
      "examples": ["workspaces/01936d3c-8f4a-7b2e-9c1d-5e6f7a8b9c0d"]
    },
    "title": {"type": "string", "minLength": 1, "maxLength": 120, "examples": ["Quarterly planning"]},
    "seats": {"type": "integer", "minimum": 1, "maximum": 50, "examples": [8]},
    "start_time": {"type": ["string", "null"], "format": "date-time", "description": "Rules: timestamp.gt_now"},
    "end_time": {"type": ["string", "null"], "format": "date-time"},
    "attendees": {
      "type": "array", "maxItems": 20, "description": "Rules: repeated.unique",
      "items": {"type": "string", "format": "email"}
    },
    "labels": {
      "type": "object", "maxProperties": 8,
      "propertyNames": {"type": "string", "pattern": "^[a-z][a-z0-9_]{0,30}$"},
      "additionalProperties": {"type": "string", "maxLength": 64}
    },
    "priority": {"type": "string", "enum": ["URGENCY_LOW", "URGENCY_NORMAL", "URGENCY_HIGH"]}
  }
}
```

The schema tells the model what to send; it enforces nothing. This example has
no protovalidate interceptor, so the rules are not checked here.

See `../connect` for serving MCP over HTTP next to the connect API itself.

## Test / list tools

[f/mcptools](https://github.com/f/mcptools) can be useful.

Run `mcptools tools go run .`
