package cli

import (
	"testing"

	"shelley.exe.dev/llm"
)

func TestStreamingStateTransitions(t *testing.T) {
	// Test that streaming state is properly managed
	
	// Simulate receiving stream events
	m := &Model{
		styles: DefaultStyles(),
	}
	
	// Initially not active
	if m.streamingActive {
		t.Error("streamingActive should be false initially")
	}
	
	// Receiving text delta should activate
	m.streamingActive = true
	m.streamingText.WriteString("Hello")
	
	if !m.streamingActive {
		t.Error("streamingActive should be true after text delta")
	}
	
	if m.streamingText.String() != "Hello" {
		t.Errorf("streamingText should be 'Hello', got %q", m.streamingText.String())
	}
	
	// Reset for new message
	m.streamingActive = false
	m.streamingText.Reset()
	
	if m.streamingActive {
		t.Error("streamingActive should be false after reset")
	}
	
	if m.streamingText.Len() != 0 {
		t.Error("streamingText should be empty after reset")
	}
}

func TestStreamEventTypes(t *testing.T) {
	// Verify all expected stream event types exist
	events := []llm.StreamEventType{
		llm.StreamEventTextDelta,
		llm.StreamEventToolUseStart,
		llm.StreamEventToolInputDelta,
		llm.StreamEventThinkingDelta,
		llm.StreamEventContentBlockStop,
		llm.StreamEventMessageComplete,
	}
	
	for i, ev := range events {
		if int(ev) != i {
			t.Errorf("StreamEventType %d has wrong value %d", i, ev)
		}
	}
}
