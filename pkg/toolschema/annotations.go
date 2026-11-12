package toolschema

import (
	"errors"

	"github.com/advdv/connectmcp-go/pkg/connectmcpv1"
	"github.com/advdv/connectmcp-go/pkg/mcpserver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ToolAnnotationsForMethod returns the MCP tool annotations of a method, or
// nil when it has none.
//
// Whether the tool has side effects follows from the standard
// idempotency_level option, the declaration Connect uses for GET requests:
//
//   - NO_SIDE_EFFECTS: readOnlyHint, idempotentHint and destructiveHint false;
//   - IDEMPOTENT: idempotentHint;
//   - unset: no hints.
//
// The (connectmcp.v1.tool_annotations) option adds the title, destructiveHint and
// openWorldHint. A destructive_hint of true on a NO_SIDE_EFFECTS method
// contradicts idempotency_level: the error reports it, and the returned
// annotations then hold the hints derived from idempotency_level alone, so a
// contradictory hint is never published. connectmcp rejects such a method at
// registration.
func ToolAnnotationsForMethod(method protoreflect.MethodDescriptor) (*mcpserver.ToolAnnotations, error) {
	opts, _ := method.Options().(*descriptorpb.MethodOptions)
	derived := mcpserver.ToolAnnotations{}
	switch opts.GetIdempotencyLevel() {
	case descriptorpb.MethodOptions_NO_SIDE_EFFECTS:
		derived.ReadOnlyHint = true
		derived.IdempotentHint = true
		derived.DestructiveHint = proto.Bool(false)
	case descriptorpb.MethodOptions_IDEMPOTENT:
		derived.IdempotentHint = true
	case descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN:
	}

	configured := configuredToolAnnotations(opts)
	if configured == nil {
		return nonZero(derived), nil
	}
	if derived.ReadOnlyHint && configured.GetDestructiveHint() {
		return nonZero(derived), errors.New("destructive_hint is true on a method with idempotency_level NO_SIDE_EFFECTS")
	}

	annotations := derived
	annotations.Title = configured.GetTitle()
	if configured.DestructiveHint != nil {
		annotations.DestructiveHint = proto.Bool(configured.GetDestructiveHint())
	}
	if configured.OpenWorldHint != nil {
		annotations.OpenWorldHint = proto.Bool(configured.GetOpenWorldHint())
	}
	return nonZero(annotations), nil
}

// configuredToolAnnotations returns the (connectmcp.v1.tool_annotations) option, or
// nil when it is not set.
func configuredToolAnnotations(opts *descriptorpb.MethodOptions) *connectmcpv1.ToolAnnotations {
	if opts == nil || !proto.HasExtension(opts, connectmcpv1.E_ToolAnnotations) {
		return nil
	}
	annotations, _ := proto.GetExtension(opts, connectmcpv1.E_ToolAnnotations).(*connectmcpv1.ToolAnnotations)
	return annotations
}

func nonZero(a mcpserver.ToolAnnotations) *mcpserver.ToolAnnotations {
	if a.IsZero() {
		return nil
	}
	return &a
}
