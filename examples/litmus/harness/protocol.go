package harness

import "boundary-mcp/internal/protocol"

// The behavioral engine consumes the protocol core through aliases so its
// sources keep working after the core moved to internal/protocol.
type (
	Schema    = protocol.Schema
	FrameSpec = protocol.FrameSpec
	Object    = protocol.Object
	Field     = protocol.Field
)

var (
	LoadSchema     = protocol.LoadSchema
	ShortName      = protocol.ShortName
	ValidateBuffer = protocol.ValidateBuffer
)
