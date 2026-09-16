package tui

import (
	"testing"
)

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"hello", 5},
		{"\x1b[31mhello\x1b[0m", 5},
		{"你好", 4},
		{"● running", 9},
		{"🚀 fire", 7},
		{"", 0},
	}

	for _, c := range cases {
		got := DisplayWidth(c.input)
		if got != c.want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestTruncateToWidth(t *testing.T) {
	cases := []struct {
		input string
		max   int
		ell   string
		want  string
	}{
		{"hello world", 5, "…", "hell…"},
		{"hello world", 20, "…", "hello world"},
		{"你好世界", 5, "…", "你好…"},
		{"你好世界", 4, "…", "你…"},
		{"\x1b[31mhello\x1b[0m world", 8, "…", "hello w…"},
	}

	for _, c := range cases {
		got := TruncateToWidth(c.input, c.max, c.ell)
		if got != c.want {
			t.Errorf("TruncateToWidth(%q, %d, %q) = %q, want %q", c.input, c.max, c.ell, got, c.want)
		}
	}
}
