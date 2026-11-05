package arena

import (
	"context"
	"fmt"
)

// ModelRequest is one stateless decision request.
type ModelRequest struct {
	Model     string
	Effort    string // "" = model default
	Thinking  string // "" = omitted, or "adaptive"; never "disabled"
	MaxTokens int64
	Tool      ToolDef
	System    string // cached prefix (after the tool)
	User      string // the fresh view
}

// ActInput is the decoded input of the `act` tool call.
type ActInput struct {
	Action string `json:"action"`
	Talk   string `json:"talk"`
	Memo   string `json:"memo"`
}

// ModelReply is a successful API response. Act is nil when the reply had no
// `act` call.
type ModelReply struct {
	Act        *ActInput
	StopReason string
	Model      string // the model the API says answered
	Usage      Usage
}

// ModelErrorKind classifies a failed request for the strike machine.
type ModelErrorKind uint8

const (
	ModelTimeout  ModelErrorKind = iota + 1 // decision timeout
	ModelProvider                           // 429, 5xx, overloaded, network: retry with backoff
	ModelFatal                              // 400, 401, 403, 404: configuration error, stop the run
)

// ModelError is returned by ModelClient implementations.
type ModelError struct {
	Kind   ModelErrorKind
	Status int
	Code   string
	Err    error
}

func (e *ModelError) Error() string {
	return fmt.Sprintf("model error (%s, status %d): %v", e.Code, e.Status, e.Err)
}

func (e *ModelError) Unwrap() error { return e.Err }

// ModelClient makes one decision request.
type ModelClient interface {
	Decide(ctx context.Context, req ModelRequest) (ModelReply, error)
}
