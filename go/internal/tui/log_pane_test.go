package tui

import (
	"testing"
)

func TestLogPane(t *testing.T) {
	pane := NewLogPane(5)

	pane.Push("line 1", "info")
	pane.Push("line 2\nline 3", "info")
	if pane.Count() != 3 {
		t.Fatalf("expected 3 lines, got %d", pane.Count())
	}

	view := pane.View(2)
	if len(view.Lines) != 2 {
		t.Fatalf("expected 2 lines in view, got %d", len(view.Lines))
	}
	if view.Lines[0].Text != "line 2" || view.Lines[1].Text != "line 3" {
		t.Errorf("unexpected lines: %v", view.Lines)
	}
	if !pane.Following() {
		t.Errorf("expected pane to be following")
	}

	// Scroll up
	pane.ScrollUp(1)
	if pane.Following() {
		t.Errorf("expected pane not to be following after scroll up")
	}
	view = pane.View(2)
	if view.Lines[0].Text != "line 1" || view.Lines[1].Text != "line 2" {
		t.Errorf("unexpected lines after scroll: %v", view.Lines)
	}

	// Follow bottom
	pane.FollowBottom()
	if !pane.Following() {
		t.Errorf("expected pane to be following after FollowBottom")
	}

	// Capacity overflow
	pane.Push("line 4", "info")
	pane.Push("line 5", "info")
	pane.Push("line 6", "info")
	if pane.Count() != 5 {
		t.Fatalf("expected capacity 5, got %d", pane.Count())
	}
	view = pane.View(5)
	if view.Lines[0].Text != "line 2" {
		t.Errorf("expected oldest line to be 'line 2', got %q", view.Lines[0].Text)
	}
}
