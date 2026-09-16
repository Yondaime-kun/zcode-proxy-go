package tui

import (
	"strings"
	"sync"
)

type LogLine struct {
	Seq   int
	Level string
	Text  string
}

type LogView struct {
	Lines      []LogLine
	Total      int
	FromBottom int
}

type LogPane struct {
	mu       sync.RWMutex
	capacity int
	lines    []LogLine
	nextSeq  int
	offset   int // lines hidden below viewport (0 = following tail)
}

func NewLogPane(capacity int) *LogPane {
	if capacity <= 0 {
		capacity = 2000
	}
	return &LogPane{
		capacity: capacity,
		lines:    make([]LogLine, 0, 128),
	}
}

func (p *LogPane) Push(text string, level string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	clean := StripAnsi(text)
	clean = strings.ReplaceAll(clean, "\r", "")
	parts := strings.Split(clean, "\n")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	for _, part := range parts {
		p.lines = append(p.lines, LogLine{
			Seq:   p.nextSeq,
			Level: level,
			Text:  part,
		})
		p.nextSeq++
	}

	if len(p.lines) > p.capacity {
		excess := len(p.lines) - p.capacity
		p.lines = p.lines[excess:]
	}
}

func (p *LogPane) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.lines)
}

func (p *LogPane) Following() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.offset == 0
}

func (p *LogPane) ScrollUp(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 {
		n = 0
	}
	p.offset += n
	if p.offset > len(p.lines) {
		p.offset = len(p.lines)
	}
}

func (p *LogPane) ScrollDown(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 {
		n = 0
	}
	p.offset -= n
	if p.offset < 0 {
		p.offset = 0
	}
}

func (p *LogPane) FollowBottom() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offset = 0
}

func (p *LogPane) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lines = p.lines[:0]
	p.offset = 0
}

func (p *LogPane) View(height int) LogView {
	p.mu.RLock()
	defer p.mu.RUnlock()

	rows := height
	if rows < 1 {
		rows = 1
	}

	total := len(p.lines)
	if total == 0 {
		return LogView{
			Lines:      nil,
			Total:      0,
			FromBottom: 0,
		}
	}

	// At max scrollback (offset == total), the viewport pins to oldest rows
	end := total - p.offset
	minEnd := rows
	if minEnd > total {
		minEnd = total
	}
	if end < minEnd {
		end = minEnd
	}

	start := end - rows
	if start < 0 {
		start = 0
	}

	linesSlice := make([]LogLine, end-start)
	copy(linesSlice, p.lines[start:end])

	fromBottom := total - end
	if fromBottom < 0 {
		fromBottom = 0
	}

	return LogView{
		Lines:      linesSlice,
		Total:      total,
		FromBottom: fromBottom,
	}
}
