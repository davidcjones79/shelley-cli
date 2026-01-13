package llm

import "context"

// StreamEventType represents the type of streaming event
type StreamEventType int

//go:generate go tool golang.org/x/tools/cmd/stringer -type=StreamEventType -output=stream_string.go

const (
	StreamEventTextDelta StreamEventType = iota
	StreamEventToolUseStart
	StreamEventToolInputDelta
	StreamEventThinkingDelta
	StreamEventContentBlockStop
	StreamEventMessageComplete
	StreamEventRequestStart // Emitted when request begins (before API call)
)

// StreamEvent represents a streaming event from the LLM
type StreamEvent struct {
	Type StreamEventType

	// For text/thinking deltas
	Text string

	// For tool use
	ToolUseID string
	ToolName  string
	ToolInput string // partial JSON input

	// For content block tracking
	ContentIndex int

	// For message complete
	Response *Response
}

// StreamCallback is called for each streaming event
type StreamCallback func(event StreamEvent) error

// Streamer is an optional interface for services that support streaming
type Streamer interface {
	// DoStream sends a streaming request to an LLM
	// The callback is called for each streaming event
	// Returns the final complete response
	DoStream(ctx context.Context, req *Request, callback StreamCallback) (*Response, error)
}
