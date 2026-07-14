package tool

import "context"

// ExecutionMeta carries optional coordination metadata for a single tool call.
// Filled by graph/agentspec layers; core does not import graph.
type ExecutionMeta struct {
	ThreadID           string
	RunID              string
	NodeID             string
	ParentCheckpointID string
	LeaseEpoch         int64
	ToolCallID         string
	IdempotencyKey     string
}

// ContextualTool is an optional Tool extension that receives context and
// ExecutionMeta. Tools that ignore it continue to implement only Run(args).
type ContextualTool interface {
	Tool
	RunCtx(ctx context.Context, meta ExecutionMeta, args map[string]any) Result
}
