package tts

import "strings"

type SSEEvent struct {
	ID    string
	Event string
	Data  string
}

type SSEParser struct {
	id    string
	event string
	data  []string
}

const (
	KeyEvent = "event"
)

func (p *SSEParser) Reset() {
	p.id = ""
	p.event = ""
	p.data = nil
}

func (p *SSEParser) FeedLine(line string) *SSEEvent {
	// 空行表示一个事件结束
	if line == "" {
		if len(p.data) == 0 && p.id == "" && p.event == "" {
			return nil
		}
		ev := &SSEEvent{
			ID:    p.id,
			Event: p.event,
			Data:  strings.Join(p.data, "\n"),
		}
		p.Reset()
		return ev
	}

	// 注释行 : ping
	if strings.HasPrefix(line, ":") {
		return nil
	}

	// field: value
	if i := strings.Index(line, ":"); i >= 0 {
		field := line[:i]
		value := strings.TrimLeft(line[i+1:], " ")
		switch field {
		case "id":
			p.id = value
		case KeyEvent:
			p.event = value
		case "data":
			p.data = append(p.data, value)
		case "retry":
			// ignore
		}
	}
	return nil
}
