package arena

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicClient calls the Messages API with one `act` tool.
type AnthropicClient struct {
	c anthropic.Client
}

// NewAnthropicClient builds a client from an explicit key. It ignores every
// environment default of the SDK (key variables, base URL, custom headers,
// profiles) and disables the SDK's own retries: the strike machine owns the
// retry policy. baseURL "" means the production API.
func NewAnthropicClient(apiKey, baseURL string) *AnthropicClient {
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithHTTPClient(&http.Client{}),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &AnthropicClient{c: anthropic.NewClient(opts...)}
}

func buildParams(req ModelRequest) anthropic.MessageNewParams {
	schema := req.Tool.InputSchema
	required, _ := schema["required"].([]any)
	var req2 []string
	for _, r := range required {
		if s, ok := r.(string); ok {
			req2 = append(req2, s)
		}
	}
	tool := anthropic.ToolParam{
		Name:        req.Tool.Name,
		Description: anthropic.String(req.Tool.Description),
		Strict:      anthropic.Bool(true),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties:  schema["properties"],
			Required:    req2,
			ExtraFields: map[string]any{"additionalProperties": false},
		},
	}
	p := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		System:    []anthropic.TextBlockParam{{Text: req.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.User))},
		Tools:     []anthropic.ToolUnionParam{{OfTool: &tool}},
		ToolChoice: anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{
			DisableParallelToolUse: anthropic.Bool(true),
		}},
	}
	if req.Effort != "" {
		p.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	}
	if req.Thinking == "adaptive" {
		p.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}}
	}
	return p
}

// Decide sends one request. Errors are *ModelError, or the context's error
// when ctx was cancelled (not a timeout).
func (a *AnthropicClient) Decide(ctx context.Context, req ModelRequest) (ModelReply, error) {
	resp, err := a.c.Messages.New(ctx, buildParams(req))
	if err != nil {
		return ModelReply{}, classify(ctx, err)
	}
	rep := ModelReply{
		StopReason: string(resp.StopReason),
		Model:      string(resp.Model),
		Usage: Usage{In: resp.Usage.InputTokens, Out: resp.Usage.OutputTokens,
			CacheRead: resp.Usage.CacheReadInputTokens, CacheWrite: resp.Usage.CacheCreationInputTokens},
	}
	for _, block := range resp.Content {
		tu, ok := block.AsAny().(anthropic.ToolUseBlock)
		if !ok || tu.Name != req.Tool.Name {
			continue
		}
		var in ActInput
		if json.Unmarshal(tu.Input, &in) == nil {
			rep.Act = &in
		}
		break
	}
	return rep, nil
}

func classify(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &ModelError{Kind: ModelTimeout, Code: "timeout", Err: context.DeadlineExceeded}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		me := &ModelError{Status: apierr.StatusCode, Code: http.StatusText(apierr.StatusCode), Err: errors.New("api status")}
		switch {
		case apierr.StatusCode == 408 || apierr.StatusCode == 409 || apierr.StatusCode == 429 || apierr.StatusCode >= 500:
			me.Kind = ModelProvider
		default:
			me.Kind = ModelFatal
		}
		if apierr.RequestID != "" {
			me.Code += " request_id=" + apierr.RequestID
		}
		return me
	}
	return &ModelError{Kind: ModelProvider, Code: "network", Err: errors.New("transport error")}
}
