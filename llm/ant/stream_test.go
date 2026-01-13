package ant

import (
	"bytes"
	"encoding/json"
	"testing"

	"shelley.exe.dev/llm"
)

func TestSSEScanner(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-sonnet","content":[],"stop_reason":null,"usage":{"input_tokens":100,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`
	scanner := newSSEScanner(bytes.NewReader([]byte(input)))
	
	expectedEvents := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	
	i := 0
	for scanner.Scan() {
		eventType, data := scanner.Event()
		if eventType == "" {
			continue
		}
		if i >= len(expectedEvents) {
			t.Fatalf("too many events: got %q", eventType)
		}
		if eventType != expectedEvents[i] {
			t.Errorf("event %d: expected %q, got %q", i, expectedEvents[i], eventType)
		}
		if data == nil {
			t.Errorf("event %d: data is nil", i)
		}
		i++
	}
	
	if err := scanner.Err(); err != nil {
		t.Errorf("scanner error: %v", err)
	}
	
	if i != len(expectedEvents) {
		t.Errorf("expected %d events, got %d", len(expectedEvents), i)
	}
}

func TestStreamEventParsing(t *testing.T) {
	// Test that we can parse each event type
	events := []struct {
		eventType string
		data      string
	}{
		{"message_start", `{"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","model":"claude-3-sonnet","content":[],"usage":{"input_tokens":100}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	
	for _, e := range events {
		var event streamEvent
		if err := json.Unmarshal([]byte(e.data), &event); err != nil {
			t.Errorf("failed to parse %s: %v", e.eventType, err)
		}
	}
}

// TestStreamerInterface verifies Service implements Streamer
func TestStreamerInterface(t *testing.T) {
	var _ llm.Streamer = (*Service)(nil)
}
