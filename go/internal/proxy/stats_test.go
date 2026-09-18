package proxy

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStatsTracker(t *testing.T) {
	st := NewStatsTrackerWithPersistence("", nil)

	// 1. Initial snapshot should be zero
	snap := st.Snapshot()
	if snap.TotalRequests != 0 || snap.ActiveRequests != 0 {
		t.Fatalf("expected 0 requests initially, got %v", snap)
	}

	// 2. Start request
	done1 := st.RecordRequestStart("glm-5.3", "127.0.0.1")
	snap = st.Snapshot()
	if snap.TotalRequests != 1 || snap.ActiveRequests != 1 {
		t.Fatalf("expected 1 total, 1 active, got %v", snap)
	}

	// 3. Complete request
	done1(200, 1500, 300)
	snap = st.Snapshot()
	if snap.TotalRequests != 1 || snap.ActiveRequests != 0 || snap.SuccessRequests != 1 {
		t.Fatalf("expected 1 total, 0 active, 1 success, got %v", snap)
	}
	if snap.InputTokens != 1500 || snap.OutputTokens != 300 || snap.TotalTokens != 1800 {
		t.Fatalf("expected 1500 in, 300 out, 1800 total, got in=%d out=%d tot=%d",
			snap.InputTokens, snap.OutputTokens, snap.TotalTokens)
	}

	// 4. Multiple models and error request
	done2 := st.RecordRequestStart("glm-5.3-flash", "103.28.12.5")
	done2(500, 200, 0)

	snap = st.Snapshot()
	if snap.TotalRequests != 2 || snap.SuccessRequests != 1 || snap.ErrorRequests != 1 {
		t.Fatalf("expected 2 total, 1 ok, 1 err, got %v", snap)
	}
	if len(snap.Models) != 2 {
		t.Fatalf("expected 2 models tracked, got %d", len(snap.Models))
	}
	if len(snap.Clients) != 2 {
		t.Fatalf("expected 2 clients tracked, got %d", len(snap.Clients))
	}

	// Model sorting: glm-5.3 has 1800 tokens, glm-5.3-flash has 200 tokens
	if snap.Models[0].Model != "glm-5.3" {
		t.Errorf("expected glm-5.3 first in sorted models, got %s", snap.Models[0].Model)
	}

	// 5. Reset
	st.Reset()
	snap = st.Snapshot()
	if snap.TotalRequests != 0 || snap.TotalTokens != 0 || len(snap.Models) != 0 || len(snap.Clients) != 0 {
		t.Fatalf("expected empty snapshot after reset, got %v", snap)
	}
}

func TestStatsTrackerConcurrent(t *testing.T) {
	st := NewStatsTrackerWithPersistence("", nil)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			model := "glm-5.3"
			if id%2 == 0 {
				model = "glm-5.3-flash"
			}
			ip := "127.0.0.1"
			if id%3 == 0 {
				ip = "10.0.0.1"
			}
			done := st.RecordRequestStart(model, ip)
			time.Sleep(2 * time.Millisecond)
			done(200, 100, 50)
		}(i)
	}

	wg.Wait()
	snap := st.Snapshot()
	if snap.TotalRequests != 50 || snap.ActiveRequests != 0 {
		t.Errorf("expected 50 total, 0 active, got total=%d active=%d", snap.TotalRequests, snap.ActiveRequests)
	}
	if snap.TotalTokens != 50*150 {
		t.Errorf("expected %d total tokens, got %d", 50*150, snap.TotalTokens)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.00k"},
		{1500, "1.50k"},
		{12400, "12.4k"},
		{999900, "999.9k"},
		{1000000, "1M"},
		{1250000, "1.25M"},
		{20000000, "20M"},
	}

	for _, c := range cases {
		got := FormatTokens(c.input)
		if got != c.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStatsPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	statsPath := filepath.Join(tmpDir, "stats.json")

	st1 := NewStatsTrackerWithPersistence(statsPath, nil)
	done1 := st1.RecordRequestStart("glm-5.3", "127.0.0.1")
	done1(200, 1000, 500)
	done2 := st1.RecordRequestStart("glm-5.3-flash", "192.168.1.10")
	done2(200, 200, 100)

	// Explicit save to ensure file written, and wait for async goroutines to settle
	time.Sleep(30 * time.Millisecond)
	if err := st1.Save(); err != nil {
		t.Fatalf("failed to save stats: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	// Create new tracker from same file
	st2 := NewStatsTrackerWithPersistence(statsPath, nil)
	snap := st2.Snapshot()

	if snap.TotalRequests != 2 {
		t.Errorf("expected 2 total requests loaded, got %d", snap.TotalRequests)
	}
	if snap.InputTokens != 1200 || snap.OutputTokens != 600 || snap.TotalTokens != 1800 {
		t.Errorf("expected 1200 in, 600 out, 1800 total, got in=%d out=%d tot=%d",
			snap.InputTokens, snap.OutputTokens, snap.TotalTokens)
	}
	if len(snap.Models) != 2 {
		t.Errorf("expected 2 models loaded, got %d", len(snap.Models))
	}
	if len(snap.Clients) != 2 {
		t.Errorf("expected 2 clients loaded, got %d", len(snap.Clients))
	}

	// Test reset
	st2.Reset()
	if err := st2.Save(); err != nil {
		t.Fatalf("failed to save reset stats: %v", err)
	}

	st3 := NewStatsTrackerWithPersistence(statsPath, nil)
	snap3 := st3.Snapshot()
	if snap3.TotalRequests != 0 || snap3.TotalTokens != 0 {
		t.Errorf("expected 0 requests after reset, got %d", snap3.TotalRequests)
	}

	// Allow any asynchronous persistence goroutines to finish before TempDir cleanup
	time.Sleep(50 * time.Millisecond)
}

