package tui

import (
	"regexp"
	"strings"
)

// ansiRegex matches CSI sequences, OSC sequences, and two-byte ESC sequences.
var ansiRegex = regexp.MustCompile(`\x1b(?:\[[0-9;?<=>! ]*[A-Za-z~@` + "`" + `\\]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[@-Z\\-_=>])`)

type rangeEntry struct {
	start rune
	end   rune
}

// wideRanges contains rune ranges that occupy 2 terminal cells (East Asian wide/fullwidth, emojis).
var wideRanges = []rangeEntry{
	{0x1100, 0x115f}, {0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf},
	{0x4e00, 0x9fff}, {0xa000, 0xa4c6}, {0xa960, 0xa97c}, {0xac00, 0xd7a3},
	{0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe52}, {0xfe54, 0xfe66},
	{0xfe68, 0xfe6b}, {0xff01, 0xff60}, {0xffe0, 0xffe6},
	{0x1f300, 0x1f64f}, {0x1f680, 0x1f6ff}, {0x1f900, 0x1f9ff},
	{0x20000, 0x2fffd}, {0x30000, 0x3fffd},
}

// zeroRanges contains rune ranges that occupy 0 terminal cells (combining marks, zero-width).
var zeroRanges = []rangeEntry{
	{0x0300, 0x036f}, {0x200b, 0x200f}, {0x20d0, 0x20ff}, {0xfe00, 0xfe0f},
}

func inRanges(r rune, ranges []rangeEntry) bool {
	lo := 0
	hi := len(ranges) - 1
	for lo <= hi {
		mid := (lo + hi) / 2
		if r < ranges[mid].start {
			hi = mid - 1
		} else if r > ranges[mid].end {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

// RuneWidth returns the display width in terminal cells of a single rune.
func RuneWidth(r rune) int {
	if r == 0 {
		return 0
	}
	if r < 32 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if inRanges(r, zeroRanges) {
		return 0
	}
	if inRanges(r, wideRanges) {
		return 2
	}
	return 1
}

// StripAnsi removes ANSI escape sequences so the string can be measured or sliced.
func StripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// DisplayWidth returns the terminal cell width of string s (ANSI sequences count as 0).
func DisplayWidth(s string) int {
	clean := StripAnsi(s)
	w := 0
	for _, r := range clean {
		w += RuneWidth(r)
	}
	return w
}

// TruncateToWidth truncates string s to max display cells, appending ellipsis if cut.
func TruncateToWidth(s string, max int, ellipsis string) string {
	if max <= 0 {
		return ""
	}
	clean := StripAnsi(s)
	if DisplayWidth(clean) <= max {
		return clean
	}
	ellWidth := DisplayWidth(ellipsis)
	w := 0
	var out strings.Builder
	for _, r := range clean {
		rw := RuneWidth(r)
		if w+rw > max-ellWidth {
			break
		}
		out.WriteRune(r)
		w += rw
	}
	out.WriteString(ellipsis)
	return out.String()
}

// PadEndWidth pads string s with trailing spaces until it reaches width display cells.
func PadEndWidth(s string, width int) string {
	curW := DisplayWidth(s)
	pad := width - curW
	if pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// PadStartWidth pads string s with leading spaces until it reaches width display cells.
func PadStartWidth(s string, width int) string {
	curW := DisplayWidth(s)
	pad := width - curW
	if pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}
