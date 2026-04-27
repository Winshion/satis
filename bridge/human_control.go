package bridge

import (
	"context"

	"satis/satis"
)

type HumanControlRequest struct {
	RunID           string
	ChunkID         string
	ControlKind     string
	Title           string
	Description     string
	AllowedBranches []string
	DefaultBranch   string
	InputBindings   map[string]satis.RuntimeBinding
}

type HumanControlChoice struct {
	Branch  string
	Payload map[string]any
}

type HumanControlChooser func(ctx context.Context, req HumanControlRequest) (HumanControlChoice, error)

type pendingHumanControl struct {
	ctx            context.Context
	request        HumanControlRequest
	responseCh     chan humanControlResponse
	dispatched     bool
	dispatchSeq    uint64
	dispatchCancel context.CancelFunc
}

type humanControlResponse struct {
	choice HumanControlChoice
	err    error
}
