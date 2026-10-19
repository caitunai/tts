package qwenaudiotts

import (
	"testing"

	"github.com/caitunai/tts/internal/tts"
)

func TestSessionSegmentBoundaries(t *testing.T) {
	type step struct {
		event string
		text  string
		want  []string
	}
	tests := []struct {
		name     string
		segments []*inputSegment
		steps    []step
	}{
		{
			name: "multiple sentences and final trailing audio",
			segments: []*inputSegment{
				{id: "one", remainingText: "First.Second."},
				{id: "two", remainingText: "Last.", isLast: true},
			},
			steps: []step{
				{event: "sentence-begin", text: "First.", want: []string{"segment_start:one"}},
				{event: "audio", want: []string{"audio:one"}},
				{event: "sentence-end", text: "First."},
				{event: "sentence-begin", text: "Second."},
				{event: "audio", want: []string{"audio:one"}},
				{event: "sentence-end", text: "Second.", want: []string{"segment_end:one"}},
				{event: "sentence-begin", text: "Last.", want: []string{"segment_start:two"}},
				{event: "audio", want: []string{"audio:two"}},
				{event: "sentence-end", text: "Last."},
				{event: "audio", want: []string{"audio:two"}},
				{event: "task-finished", want: []string{"segment_end:two", "session_end:"}},
			},
		},
		{
			name: "sentence joins input fragments",
			segments: []*inputSegment{
				{id: "one", remainingText: "Hello"},
				{id: "two", remainingText: "world.", isLast: true},
			},
			steps: []step{
				{event: "sentence-begin", text: "Hello world.", want: []string{"segment_start:one"}},
				{event: "audio", want: []string{"audio:one"}},
				{event: "sentence-end", text: "Hello world.", want: []string{"segment_end:one"}},
				{event: "task-finished", want: []string{"segment_start:two", "segment_end:two", "session_end:"}},
			},
		},
		{
			name: "missing metadata waits for task completion",
			segments: []*inputSegment{
				{id: "one", remainingText: "Hello."},
				{id: "two", remainingText: "World.", isLast: true},
			},
			steps: []step{
				{event: "audio", want: []string{"segment_start:one", "audio:one"}},
				{event: "sentence-end"},
				{event: "audio", want: []string{"audio:one"}},
				{event: "sentence-end"},
				{event: "task-finished", want: []string{"segment_end:one", "segment_start:two", "segment_end:two", "session_end:"}},
			},
		},
		{
			name: "mismatch does not partially consume input",
			segments: []*inputSegment{
				{id: "one", remainingText: "Hello."},
				{id: "two", remainingText: "World.", isLast: true},
			},
			steps: []step{
				{event: "sentence-begin", text: "Hello.", want: []string{"segment_start:one"}},
				{event: "audio", want: []string{"audio:one"}},
				{event: "sentence-end", text: "Hello.changed"},
				{event: "sentence-end", text: "Hello.", want: []string{"segment_end:one"}},
				{event: "sentence-begin", text: "World.", want: []string{"segment_start:two"}},
				{event: "audio", want: []string{"audio:two"}},
				{event: "sentence-end"},
				{event: "audio", want: []string{"audio:two"}},
				{event: "task-finished", want: []string{"segment_end:two", "session_end:"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSession(sessionConfig{id: "session", provider: "qwen_audio"})
			s.segments = tt.segments
			s.startedFlag = true
			for _, step := range tt.steps {
				switch step.event {
				case "audio":
					s.handleAudioMessage([]byte("ogg audio"))
				case "task-finished":
					if s.handleTextMessage([]byte(`{"header":{"event":"task-finished"},"payload":{}}`)) {
						t.Fatal("task-finished did not end reading")
					}
				default:
					s.handleResultGenerated(protocolMessage{Payload: protocolPayload{
						Output: &protocolOutput{Type: step.event, OriginalText: step.text},
					}})
				}
				for _, want := range step.want {
					event := nextProviderEvent(t, s.Events())
					if got := string(event.Type) + ":" + event.SegmentID; got != want {
						t.Fatalf("after %s (%q): event = %q, want %q", step.event, step.text, got, want)
					}
				}
				select {
				case event := <-s.Events():
					t.Fatalf("after %s (%q): unexpected event %s:%s", step.event, step.text, event.Type, event.SegmentID)
				default:
				}
			}
			if len(s.segments) != 0 {
				t.Fatalf("pending segments after task-finished = %d", len(s.segments))
			}
		})
	}
}

var _ tts.ProviderTextStreamSession = (*session)(nil)
