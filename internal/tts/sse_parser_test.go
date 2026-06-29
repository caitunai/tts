package tts

import "testing"

func TestSSEParserParsesEventOnBlankLine(t *testing.T) {
	var parser SSEParser

	if event := parser.FeedLine("id: evt_001"); event != nil {
		t.Fatalf("FeedLine returned event before blank line: %#v", event)
	}
	if event := parser.FeedLine("event: audio.delta"); event != nil {
		t.Fatalf("FeedLine returned event before blank line: %#v", event)
	}
	if event := parser.FeedLine(`data: {"chunk":"abc"}`); event != nil {
		t.Fatalf("FeedLine returned event before blank line: %#v", event)
	}

	event := parser.FeedLine("")
	if event == nil {
		t.Fatal("FeedLine blank line returned nil, want event")
	}
	if event.ID != "evt_001" {
		t.Fatalf("ID = %q, want evt_001", event.ID)
	}
	if event.Event != "audio.delta" {
		t.Fatalf("Event = %q, want audio.delta", event.Event)
	}
	if event.Data != `{"chunk":"abc"}` {
		t.Fatalf("Data = %q, want json payload", event.Data)
	}
}

func TestSSEParserJoinsMultipleDataLines(t *testing.T) {
	var parser SSEParser

	parser.FeedLine("data: first")
	parser.FeedLine("data: second")
	parser.FeedLine("data: third")

	event := parser.FeedLine("")
	if event == nil {
		t.Fatal("FeedLine blank line returned nil, want event")
	}
	if event.Data != "first\nsecond\nthird" {
		t.Fatalf("Data = %q, want joined data lines", event.Data)
	}
}

func TestSSEParserIgnoresCommentsRetryAndUnknownFields(t *testing.T) {
	var parser SSEParser

	if event := parser.FeedLine(": keep-alive"); event != nil {
		t.Fatalf("comment returned event: %#v", event)
	}
	parser.FeedLine("retry: 1000")
	parser.FeedLine("unknown: ignored")
	parser.FeedLine("data: ok")

	event := parser.FeedLine("")
	if event == nil {
		t.Fatal("FeedLine blank line returned nil, want event")
	}
	if event.ID != "" {
		t.Fatalf("ID = %q, want empty", event.ID)
	}
	if event.Event != "" {
		t.Fatalf("Event = %q, want empty", event.Event)
	}
	if event.Data != "ok" {
		t.Fatalf("Data = %q, want ok", event.Data)
	}
}

func TestSSEParserParsesConsecutiveEvents(t *testing.T) {
	var parser SSEParser

	parser.FeedLine("event: start")
	parser.FeedLine("data: one")
	first := parser.FeedLine("")
	if first == nil {
		t.Fatal("first event is nil")
	}

	parser.FeedLine("event: end")
	parser.FeedLine("data: two")
	second := parser.FeedLine("")
	if second == nil {
		t.Fatal("second event is nil")
	}

	if first.Event != "start" || first.Data != "one" {
		t.Fatalf("first event = %#v, want start/one", first)
	}
	if second.Event != "end" || second.Data != "two" {
		t.Fatalf("second event = %#v, want end/two", second)
	}
}

func TestSSEParserResetDropsBufferedEvent(t *testing.T) {
	var parser SSEParser

	parser.FeedLine("id: stale")
	parser.FeedLine("event: stale")
	parser.FeedLine("data: stale")
	parser.Reset()

	if event := parser.FeedLine(""); event != nil {
		t.Fatalf("blank line after reset returned event: %#v", event)
	}

	parser.FeedLine("data: fresh")
	event := parser.FeedLine("")
	if event == nil {
		t.Fatal("fresh event is nil")
	}
	if event.ID != "" || event.Event != "" || event.Data != "fresh" {
		t.Fatalf("event = %#v, want only fresh data", event)
	}
}

func TestSSEParserBlankLineWithoutBufferedFieldsReturnsNil(t *testing.T) {
	var parser SSEParser

	if event := parser.FeedLine(""); event != nil {
		t.Fatalf("blank line returned event: %#v", event)
	}
}
