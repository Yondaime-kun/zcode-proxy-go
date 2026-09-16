package tui

import (
	"testing"
)

func TestKeyParser(t *testing.T) {
	kp := NewKeyParser()

	// Simple chars
	actions := kp.Feed("s")
	if len(actions) != 1 || actions[0].Type != ActionChar || actions[0].Key != "s" {
		t.Fatalf("expected char 's', got %v", actions)
	}

	// Arrow up
	actions = kp.Feed("\x1b[A")
	if len(actions) != 1 || actions[0].Type != ActionUp {
		t.Fatalf("expected up arrow, got %v", actions)
	}

	// Ctrl-C
	actions = kp.Feed("\x03")
	if len(actions) != 1 || actions[0].Type != ActionCtrlC {
		t.Fatalf("expected ctrl-c, got %v", actions)
	}

	// Mouse click at (10, 5) -> SGR 1-based: x=11, y=6, button 0
	actions = kp.Feed("\x1b[<0;11;6M")
	if len(actions) != 1 || actions[0].Type != ActionClick || actions[0].X != 10 || actions[0].Y != 5 {
		t.Fatalf("expected click at (10, 5), got %v", actions)
	}

	// Mouse wheel up (button 64)
	actions = kp.Feed("\x1b[<64;11;6M")
	if len(actions) != 1 || actions[0].Type != ActionWheelUp {
		t.Fatalf("expected wheel up, got %v", actions)
	}

	// Partial sequence across chunks
	actions = kp.Feed("\x1b[")
	if len(actions) != 0 {
		t.Fatalf("expected no actions for partial chunk, got %v", actions)
	}
	actions = kp.Feed("B")
	if len(actions) != 1 || actions[0].Type != ActionDown {
		t.Fatalf("expected down arrow after completion, got %v", actions)
	}
}
