package provider

import "context"

type IterationRequest struct {
	SchemaVersion int
	RunID         string
	Iteration     int
	Provider      string
	Model         string
	Prompt        string
	StartedAt     string
	Limits        Limits
}

type Limits struct {
	MaxIterations int
	MaxTokens     int
}

type ProviderEvent struct {
	Time         string      `json:"time"`
	Kind         string      `json:"kind"`
	Activity     string      `json:"activity,omitempty"`
	Usage        *UsageDelta `json:"usage,omitempty"`
	ErrorCode    string      `json:"error_code,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

type UsageDelta struct {
	InputTokensDelta      int `json:"input_tokens_delta"`
	OutputTokensDelta     int `json:"output_tokens_delta"`
	CacheReadTokensDelta  int `json:"cache_read_tokens_delta"`
	CacheWriteTokensDelta int `json:"cache_write_tokens_delta"`
}

type UsageTotals struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type IterationResult struct {
	SchemaVersion int         `json:"schema_version"`
	Success       bool        `json:"success"`
	Summary       string      `json:"summary"`
	Changes       []string    `json:"changes"`
	Learnings     []string    `json:"learnings"`
	Activity      string      `json:"activity"`
	Usage         UsageTotals `json:"usage"`
	ProviderRaw   any         `json:"provider_raw,omitempty"`
	ErrorCode     string      `json:"error_code,omitempty"`
	ErrorMessage  string      `json:"error_message,omitempty"`
}

type Runner interface {
	RunIteration(ctx context.Context, req IterationRequest, onEvent func(ProviderEvent)) (IterationResult, error)
}

type AskContext struct {
	Iteration   int
	TotalTokens int
	Prompt      string
	Model       string
	Notes       []string
}

type AskUpdate struct {
	Thinking string
}

type AskEventKind string

const (
	AskEventThinking    AskEventKind = "thinking"
	AskEventAnswerDelta AskEventKind = "answer_delta"
	AskEventAnswerFinal AskEventKind = "answer_final"
	AskEventError       AskEventKind = "error"
)

type AskEvent struct {
	Kind          AskEventKind
	Update        AskUpdate
	ResponseDelta string
	Response      string
	Err           error
}

type Asker interface {
	Ask(ctx context.Context, question string, in AskContext) <-chan AskEvent
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

const (
	ErrAbortedByUser = "aborted_by_user"
	ErrMaxTokens     = "max_tokens_reached"
	ErrProviderTime  = "provider_timeout"
	ErrProtocol      = "provider_protocol_error"
)
