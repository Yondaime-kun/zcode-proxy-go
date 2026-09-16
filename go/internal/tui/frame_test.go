package tui

import (
	"strings"
	"testing"

	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
)

func TestBuildFrame(t *testing.T) {
	state := &FrameState{
		Version:       "4.6.5-go",
		ConfigPath:    "config.yaml",
		Provider:      "zai",
		Plan:          "coding-plan",
		LoggedIn:      true,
		ApiKeyPreview: "7bd0f6bc…",
		ActiveAccount: "default",
		AccountCount:  2,
		ServerStatus:  "running",
		ServerURL:     "http://127.0.0.1:8383",
		ModelCount:    4,
		LogTotal:      10,
		LogView: []LogLine{
			{Seq: 0, Level: "info", Text: "Server listening on :8383"},
			{Seq: 1, Level: "warn", Text: "Quota warn"},
		},
		LogFollowing: true,
		Width:        80,
		Height:       24,
	}

	frame := BuildFrame(state)
	if len(frame.Text) == 0 {
		t.Fatalf("expected non-empty frame text")
	}

	// Verify key elements are in output
	plainText := StripAnsi(frame.Text)
	if !strings.Contains(plainText, "Settings & Login") {
		t.Errorf("expected Settings & Login in frame")
	}
	if !strings.Contains(plainText, "Proxy Server") {
		t.Errorf("expected Proxy Server in frame")
	}
	if !strings.Contains(plainText, "Logs (10)") {
		t.Errorf("expected Logs (10) in frame")
	}

	// Verify click regions
	if len(frame.Regions) == 0 {
		t.Errorf("expected click regions to be populated")
	}

	// Test with Session Metrics
	tracker := proxy.NewStatsTrackerWithPersistence("", nil)
	done1 := tracker.RecordRequestStart("glm-5.3", "127.0.0.1")
	done1(200, 1500, 500)
	done2 := tracker.RecordRequestStart("glm-5.3-flash", "103.28.12.5")
	done2(200, 800, 200)

	snap := tracker.Snapshot()
	state.Stats = &snap
	state.Width = 120

	frameWithStats := BuildFrame(state)
	plainStatsText := StripAnsi(frameWithStats.Text)

	if !strings.Contains(plainStatsText, "Session Metrics") {
		t.Errorf("expected Session Metrics card in frame")
	}
	if !strings.Contains(plainStatsText, "2 total · 2 ok · 0 err") {
		t.Errorf("expected request counts in Session Metrics card")
	}
	if !strings.Contains(plainStatsText, "2.30k in · 700 out · 3.00k total") {
		t.Errorf("expected token counts in Session Metrics card, got: %s", plainStatsText)
	}
	if !strings.Contains(plainStatsText, "127.0.0.1 (1)") {
		t.Errorf("expected client IP 127.0.0.1 in Session Metrics card, got: %s", plainStatsText)
	}
	if !strings.Contains(plainStatsText, "[r] reset") {
		t.Errorf("expected [r] reset in footer")
	}
}
