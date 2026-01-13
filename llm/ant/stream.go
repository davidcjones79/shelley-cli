package ant

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"shelley.exe.dev/llm"
)

// DoStream sends a streaming request to Claude and calls the callback for each event
func (s *Service) DoStream(ctx context.Context, ir *llm.Request, callback llm.StreamCallback) (*llm.Response, error) {
	startTime := time.Now()
	request := s.fromLLMRequest(ir)
	request.Stream = true

	var payload []byte
	var err error
	if s.DumpLLM || testing.Testing() {
		payload, err = json.MarshalIndent(request, "", " ")
	} else {
		payload, err = json.Marshal(request)
		payload = append(payload, '\n')
	}
	if err != nil {
		return nil, err
	}

	url := cmp.Or(s.URL, DefaultURL)
	httpc := cmp.Or(s.HTTPC, http.DefaultClient)

	if s.DumpLLM {
		if err := llm.DumpToFile("request_stream", url, payload); err != nil {
			slog.WarnContext(ctx, "failed to dump request to file", "error", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.APIKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %v (url=%s, model=%s): %s", resp.Status, url, cmp.Or(s.Model, DefaultModel), body)
	}

	// Parse SSE stream
	return s.parseStreamResponse(ctx, resp.Body, callback, startTime)
}

// parseStreamResponse parses the SSE stream from Anthropic
func (s *Service) parseStreamResponse(ctx context.Context, body io.Reader, callback llm.StreamCallback, startTime time.Time) (*llm.Response, error) {
	var finalResponse *llm.Response
	var contentBlocks []content
	var currentUsage usage
	var messageID, model string
	var stopReason string

	// Buffer for accumulating tool input JSON
	toolInputBuffers := make(map[int]*strings.Builder)

	scanner := newSSEScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		eventType, data := scanner.Event()
		if eventType == "" || data == nil {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			slog.WarnContext(ctx, "failed to parse stream event", "error", err, "data", string(data))
			continue
		}

		switch eventType {
		case "message_start":
			if event.Message != nil {
				messageID = event.Message.ID
				model = event.Message.Model
				if event.Message.Usage.InputTokens > 0 {
					currentUsage = event.Message.Usage
				}
			}

		case "content_block_start":
			idx := event.Index
			// Ensure we have space for this content block
			for len(contentBlocks) <= idx {
				contentBlocks = append(contentBlocks, content{})
			}
			if event.ContentBlock != nil {
				contentBlocks[idx] = *event.ContentBlock
				// Notify about tool use start
				if event.ContentBlock.Type == "tool_use" {
					if err := callback(llm.StreamEvent{
						Type:         llm.StreamEventToolUseStart,
						ContentIndex: idx,
						ToolUseID:    event.ContentBlock.ID,
						ToolName:     event.ContentBlock.ToolName,
					}); err != nil {
						return nil, err
					}
				}
			}

		case "content_block_delta":
			idx := event.Index
			if event.Delta != nil {
				switch event.Delta.Type {
				case "text_delta":
					if idx < len(contentBlocks) && contentBlocks[idx].Text != nil {
						*contentBlocks[idx].Text += event.Delta.Text
					} else if idx < len(contentBlocks) {
						text := event.Delta.Text
						contentBlocks[idx].Text = &text
					}
					if err := callback(llm.StreamEvent{
						Type:         llm.StreamEventTextDelta,
						ContentIndex: idx,
						Text:         event.Delta.Text,
					}); err != nil {
						return nil, err
					}

				case "thinking_delta":
					if idx < len(contentBlocks) {
						contentBlocks[idx].Thinking += event.Delta.Thinking
					}
					if err := callback(llm.StreamEvent{
						Type:         llm.StreamEventThinkingDelta,
						ContentIndex: idx,
						Text:         event.Delta.Thinking,
					}); err != nil {
						return nil, err
					}

				case "input_json_delta":
					if toolInputBuffers[idx] == nil {
						toolInputBuffers[idx] = &strings.Builder{}
					}
					toolInputBuffers[idx].WriteString(event.Delta.PartialJSON)
					if err := callback(llm.StreamEvent{
						Type:         llm.StreamEventToolInputDelta,
						ContentIndex: idx,
						ToolInput:    event.Delta.PartialJSON,
					}); err != nil {
						return nil, err
					}
				}
			}

		case "content_block_stop":
			idx := event.Index
			// Finalize tool input if this was a tool_use block
			if idx < len(contentBlocks) && contentBlocks[idx].Type == "tool_use" {
				if buf, ok := toolInputBuffers[idx]; ok {
					contentBlocks[idx].ToolInput = json.RawMessage(buf.String())
				}
			}
			if err := callback(llm.StreamEvent{
				Type:         llm.StreamEventContentBlockStop,
				ContentIndex: idx,
			}); err != nil {
				return nil, err
			}

		case "message_delta":
			if event.Delta != nil && event.Delta.StopReason != "" {
				stopReason = event.Delta.StopReason
			}
			if event.Usage != nil {
				currentUsage.OutputTokens = event.Usage.OutputTokens
			}

		case "message_stop":
			endTime := time.Now()
			finalResponse = &llm.Response{
				ID:         messageID,
				Type:       "message",
				Role:       llm.MessageRoleAssistant,
				Model:      model,
				Content:    make([]llm.Content, len(contentBlocks)),
				StopReason: toLLMStopReason[stopReason],
				Usage: llm.Usage{
					InputTokens:              currentUsage.InputTokens,
					CacheCreationInputTokens: currentUsage.CacheCreationInputTokens,
					CacheReadInputTokens:     currentUsage.CacheReadInputTokens,
					OutputTokens:             currentUsage.OutputTokens,
				},
				StartTime: &startTime,
				EndTime:   &endTime,
			}
			// Convert content blocks
			for i, cb := range contentBlocks {
				finalResponse.Content[i] = toLLMContent(cb)
			}
			if err := callback(llm.StreamEvent{
				Type:     llm.StreamEventMessageComplete,
				Response: finalResponse,
			}); err != nil {
				return nil, err
			}

		case "error":
			return nil, fmt.Errorf("stream error: %s", string(data))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	if finalResponse == nil {
		return nil, fmt.Errorf("stream ended without message_stop")
	}

	return finalResponse, nil
}

// sseScanner reads Server-Sent Events from an io.Reader
type sseScanner struct {
	scanner   *bufio.Scanner
	eventType string
	data      []byte
}

func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{
		scanner: bufio.NewScanner(r),
	}
}

func (s *sseScanner) Scan() bool {
	s.eventType = ""
	s.data = nil

	var dataLines [][]byte
	for s.scanner.Scan() {
		line := s.scanner.Bytes()

		// Empty line signals end of event
		if len(line) == 0 {
			if s.eventType != "" || len(dataLines) > 0 {
				// Combine data lines
				if len(dataLines) > 0 {
					s.data = bytes.Join(dataLines, []byte("\n"))
				}
				return true
			}
			continue
		}

		// Parse field
		if bytes.HasPrefix(line, []byte("event: ")) {
			s.eventType = string(bytes.TrimPrefix(line, []byte("event: ")))
		} else if bytes.HasPrefix(line, []byte("data: ")) {
			dataLines = append(dataLines, bytes.TrimPrefix(line, []byte("data: ")))
		}
		// Ignore other fields (id, retry, comments)
	}

	// Handle final event if stream ended without empty line
	if s.eventType != "" || len(dataLines) > 0 {
		if len(dataLines) > 0 {
			s.data = bytes.Join(dataLines, []byte("\n"))
		}
		return true
	}

	return false
}

func (s *sseScanner) Event() (string, []byte) {
	return s.eventType, s.data
}

func (s *sseScanner) Err() error {
	return s.scanner.Err()
}
