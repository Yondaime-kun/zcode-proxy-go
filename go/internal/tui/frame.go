package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
)

type Seg struct {
	T string
	C string
}

type ClickActionKind string

const (
	ClickKey      ClickActionKind = "key"
	ClickProvider ClickActionKind = "provider"
	ClickPlan     ClickActionKind = "plan"
	ClickFollow   ClickActionKind = "follow"
	ClickAccount  ClickActionKind = "account"
)

type ClickAction struct {
	Kind  ClickActionKind
	Key   string
	Value string
}

type ClickRegion struct {
	Row    int
	Col    int
	Width  int
	Action ClickAction
}

type Frame struct {
	Text    string
	Regions []ClickRegion
}

func FindRegion(regions []ClickRegion, row, col int) *ClickAction {
	for _, r := range regions {
		if r.Row == row && col >= r.Col && col < r.Col+r.Width {
			return &r.Action
		}
	}
	return nil
}

type Toast struct {
	Text string
	Kind string // "ok", "err", "info"
}

type FrameState struct {
	Version          string
	ConfigPath       string
	Provider         string
	Plan             string
	LoggedIn         bool
	ApiKeyPreview    string
	ActiveAccount    string
	AccountCount     int
	QuotaText        string
	LoginInFlight    bool
	LoginHint        string
	ServerStatus     string // "stopped", "starting", "running", "error"
	ServerURL        string
	ServerError      string
	ModelCount       int
	ResponsesEnabled bool
	ClaimAuto        bool
	WhitelistCount   int
	BlacklistCount   int
	Stats            *proxy.StatsSnapshot
	LogTotal         int
	LogView          []LogLine
	LogFollowing     bool
	LogFromBottom    int
	Toast            *Toast
	Width            int
	Height           int
}

const (
	colorDim   = "90"
	colorRed   = "31"
	colorGreen = "32"
	colorAmber = "33"
	colorCyan  = "36"
	colorBold  = "1"
	labelWidth = 12

	btnGreen    = "38;2;255;255;255;48;2;35;134;54"
	btnRed      = "38;2;255;255;255;48;2;218;54;51"
	btnBlue     = "38;2;255;255;255;48;2;31;111;235"
	btnGray     = "38;2;201;209;217;48;2;48;54;61"
	btnSelected = btnBlue
	hintChip    = "30;103"
)

func paint(code, text string) string {
	if code == "" {
		return text
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, text)
}

func segWidth(segs []Seg) int {
	w := 0
	for _, s := range segs {
		w += DisplayWidth(s.T)
	}
	return w
}

func paintSegs(segs []Seg) string {
	var sb strings.Builder
	for _, s := range segs {
		if s.C != "" {
			sb.WriteString(paint(s.C, s.T))
		} else {
			sb.WriteString(s.T)
		}
	}
	return sb.String()
}

func segPlain(segs []Seg) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteString(s.T)
	}
	return sb.String()
}

func renderTopBorder(w int, left []Seg, right []Seg) string {
	l := left
	r := right
	if len(r) == 0 {
		if 5+segWidth(l) > w-1 {
			l = []Seg{{T: TruncateToWidth(segPlain(l), max(0, w-7), "…")}}
		}
		fill := max(1, w-5-segWidth(l))
		return paint(colorDim, "╭─ ") + paintSegs(l) + paint(colorDim, " "+strings.Repeat("─", fill)+"╮")
	}

	lw := segWidth(l)
	rw := segWidth(r)
	fill := w - 8 - lw - rw
	if fill < 1 {
		budget := max(0, w-9-lw)
		r = []Seg{{T: TruncateToWidth(segPlain(r), budget, "…")}}
		fill = max(1, w-8-lw-segWidth(r))
		if fill < 1 {
			l = []Seg{{T: TruncateToWidth(segPlain(l), max(0, w-8-segWidth(r)), "…")}}
			fill = max(1, w-8-segWidth(l)-segWidth(r))
		}
	}
	return paint(colorDim, "╭─ ") + paintSegs(l) + paint(colorDim, fmt.Sprintf(" %s ", strings.Repeat("─", fill))) +
		paintSegs(r) + paint(colorDim, " ─╮")
}

func renderBottomBorder(w int) string {
	return paint(colorDim, fmt.Sprintf("╰%s╯", strings.Repeat("─", max(0, w-2))))
}

type part struct {
	t      string
	c      string
	action *ClickAction
}

func composeRow(w int, row int, label *string, parts []part, chrome bool) (string, []ClickRegion) {
	contentW := max(0, w-func() int {
		if chrome {
			return 4
		}
		return 2
	}())

	prefixPlain := "  "
	if label != nil {
		prefixPlain = "  " + PadEndWidth(*label, labelWidth-2)
	}
	prefixW := DisplayWidth(prefixPlain)
	prefixPainted := prefixPlain
	if label != nil {
		prefixPainted = paint(colorDim, prefixPlain)
	}

	var regions []ClickRegion
	var painted []string
	col := prefixW

	for _, p := range parts {
		pw := DisplayWidth(p.t)
		if col+pw > contentW {
			if p.action == nil && contentW > col {
				p.t = TruncateToWidth(p.t, contentW-col, "…")
				pw = DisplayWidth(p.t)
			} else {
				break
			}
		}
		if p.action != nil {
			regionCol := col
			if chrome {
				regionCol += 2
			}
			regions = append(regions, ClickRegion{
				Row:    row,
				Col:    regionCol,
				Width:  pw,
				Action: *p.action,
			})
		}
		if p.c != "" {
			painted = append(painted, paint(p.c, p.t))
		} else {
			painted = append(painted, p.t)
		}
		col += pw
	}

	if !chrome {
		return prefixPainted + strings.Join(painted, ""), regions
	}
	pad := strings.Repeat(" ", max(0, contentW-col))
	line := fmt.Sprintf("%s %s%s%s %s", paint(colorDim, "│"), prefixPainted, strings.Join(painted, ""), pad, paint(colorDim, "│"))
	return line, regions
}

func optionParts(slots [2]string, selected string, makeAction func(string) ClickAction, disabled bool) []part {
	var parts []part
	for i, v := range slots {
		if i > 0 {
			parts = append(parts, part{t: "  "})
		}
		isSelected := v == selected

		var color string
		var text string
		if isSelected {
			text = fmt.Sprintf(" ● %s ", v)
			color = btnSelected
		} else {
			text = fmt.Sprintf(" ○ %s ", v)
			if disabled {
				color = colorDim
			} else {
				color = btnGray
			}
		}

		var act *ClickAction
		if !disabled {
			a := makeAction(v)
			act = &a
		}
		parts = append(parts, part{
			t:      text,
			c:      color,
			action: act,
		})
	}
	if disabled {
		parts = append(parts, part{t: "  "}, part{t: "(stop to switch)", c: colorAmber})
	}
	return parts
}

func logLineCode(level string) string {
	switch level {
	case "error":
		return colorRed
	case "warn":
		return colorAmber
	default:
		return ""
	}
}

var logUrlRegex = regexp.MustCompile(`https?://[^\s]+`)

func formatLogLine(rawText string, maxW int) (string, string) {
	match := logUrlRegex.FindString(rawText)
	if match == "" {
		return TruncateToWidth(rawText, maxW, "…"), ""
	}

	truncated := TruncateToWidth(rawText, maxW, "…")

	trimmed := strings.TrimSpace(rawText)
	if strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "http://") {
		linked := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", match, truncated)
		return linked, colorCyan + ";4"
	}

	truncMatch := logUrlRegex.FindString(truncated)
	if truncMatch != "" {
		linked := fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", match, truncMatch)
		return strings.Replace(truncated, truncMatch, linked, 1), ""
	}

	return truncated, ""
}

func BuildFrame(s *FrameState) Frame {
	w := s.Width
	h := s.Height
	var regions []ClickRegion
	var lines []string

	emit := func(line string) int {
		lines = append(lines, line+"\x1b[0m\x1b[K")
		return len(lines) - 1
	}

	if w < 40 || h < 14 {
		emit("zcode-proxy: terminal too small for the TUI.")
		emit(fmt.Sprintf("   need >= 40x14, got %dx%d", w, h))
		return Frame{Text: strings.Join(lines, "\r\n") + "\x1b[0m\x1b[J", Regions: regions}
	}

	var pill []Seg
	if s.LoginInFlight {
		pill = []Seg{{T: "● logging in…", C: colorAmber}}
	} else if s.LoggedIn {
		pill = []Seg{{T: "● ready", C: colorGreen}}
	} else {
		pill = []Seg{{T: "● logged out", C: colorAmber}}
	}

	busy := s.ServerStatus == "running" || s.ServerStatus == "starting"

	// --- Settings & Login Card ---
	emit(renderTopBorder(w, []Seg{{T: "Settings & Login", C: colorBold}}, pill))

	pLabel := "Provider"
	pRow, pRegs := composeRow(w, len(lines), &pLabel, optionParts(
		[2]string{"zai", "bigmodel"},
		s.Provider,
		func(v string) ClickAction { return ClickAction{Kind: ClickProvider, Value: v} },
		busy,
	), true)
	emit(pRow)
	regions = append(regions, pRegs...)

	planLabel := "Plan"
	planRow, planRegs := composeRow(w, len(lines), &planLabel, optionParts(
		[2]string{"coding-plan", "start-plan"},
		s.Plan,
		func(v string) ClickAction { return ClickAction{Kind: ClickPlan, Value: v} },
		busy,
	), true)
	emit(planRow)
	regions = append(regions, planRegs...)

	authLabel := "Auth"
	var authParts []part
	if s.LoggedIn {
		authParts = append(authParts,
			part{t: "● ", c: colorGreen},
			part{t: fmt.Sprintf("logged in · %s", s.ApiKeyPreview)},
		)
		if s.AccountCount > 1 {
			authParts = append(authParts, part{
				t: fmt.Sprintf(" (%d accs, active: %s)", s.AccountCount, s.ActiveAccount),
				c: colorDim,
			})
		}
	} else {
		authParts = append(authParts, part{t: "○ not logged in", c: colorAmber})
	}
	authRow, _ := composeRow(w, len(lines), &authLabel, authParts, true)
	emit(authRow)

	if s.LoggedIn && s.QuotaText != "" {
		quotaLabel := "Quota"
		quotaRow, _ := composeRow(w, len(lines), &quotaLabel, []part{
			{t: s.QuotaText, c: colorCyan},
		}, true)
		emit(quotaRow)
	}

	var loginButtons []part
	if s.LoginInFlight {
		loginButtons = append(loginButtons, part{t: " Logging in… ", c: colorDim})
	} else if s.LoggedIn {
		loginButtons = append(loginButtons, part{t: " Logged In ", c: colorDim}, part{t: "  "})
		if s.AccountCount > 1 {
			accAct := ClickAction{Kind: ClickActionKind("key"), Key: "a"}
			loginButtons = append(loginButtons, part{t: " Switch Acc ", c: btnBlue, action: &accAct}, part{t: "  "})
		}
		if s.Plan == "start-plan" {
			claimAct := ClickAction{Kind: ClickActionKind("key"), Key: "m"}
			loginButtons = append(loginButtons, part{t: " Claim ", c: btnBlue, action: &claimAct}, part{t: "  "})
		}
		if busy {
			loginButtons = append(loginButtons, part{t: " Logout ", c: colorDim})
		} else {
			logoutAct := ClickAction{Kind: ClickActionKind("key"), Key: "o"}
			loginButtons = append(loginButtons, part{t: " Logout ", c: btnGray, action: &logoutAct})
		}
	} else {
		loginAct := ClickAction{Kind: ClickActionKind("key"), Key: "l"}
		loginButtons = append(loginButtons,
			part{t: " OAuth Login ", c: btnGreen, action: &loginAct},
			part{t: "  "},
			part{t: " Logout ", c: colorDim},
		)
	}
	loginRow, loginRegs := composeRow(w, len(lines), nil, loginButtons, true)
	emit(loginRow)
	regions = append(regions, loginRegs...)

	if s.LoginInFlight && s.LoginHint != "" {
		lHintLabel := "Login"
		hintRow, _ := composeRow(w, len(lines), &lHintLabel, []part{
			{t: fmt.Sprintf(" ▸ %s ", s.LoginHint), c: hintChip},
		}, true)
		emit(hintRow)
	}

	emit(renderBottomBorder(w))
	emit("")

	// --- Proxy Server Card ---
	emit(renderTopBorder(w, []Seg{{T: "Proxy Server", C: colorBold}}, nil))

	statusLabel := "Status"
	var statusParts []part
	switch s.ServerStatus {
	case "running":
		statusParts = append(statusParts,
			part{t: "● running", c: colorGreen},
			part{t: "  " + s.ServerURL, c: colorCyan},
		)
	case "starting":
		statusParts = append(statusParts, part{t: "● starting…", c: colorAmber})
	case "error":
		statusParts = append(statusParts, part{t: "✗ failed", c: colorRed})
	default:
		statusParts = append(statusParts, part{t: "○ stopped", c: colorDim})
	}
	statusRow, _ := composeRow(w, len(lines), &statusLabel, statusParts, true)
	emit(statusRow)

	configLabel := "Config"
	var configItems []string
	configItems = append(configItems, fmt.Sprintf("%s · %s · %d models", s.Provider, s.Plan, s.ModelCount))
	if s.ResponsesEnabled {
		configItems = append(configItems, "/v1/responses")
	}
	if s.ClaimAuto {
		configItems = append(configItems, "claim:auto")
	}
	if s.WhitelistCount > 0 {
		configItems = append(configItems, fmt.Sprintf("whitelist:%d", s.WhitelistCount))
	}
	if s.BlacklistCount > 0 {
		configItems = append(configItems, fmt.Sprintf("blacklist:%d", s.BlacklistCount))
	}
	configItems = append(configItems, fmt.Sprintf("v%s", s.Version))
	configRow, _ := composeRow(w, len(lines), &configLabel, []part{
		{t: strings.Join(configItems, " · "), c: colorDim},
	}, true)
	emit(configRow)

	startEnabled := s.ServerStatus == "stopped" || s.ServerStatus == "error"
	stopEnabled := s.ServerStatus == "running"
	var serverButtons []part
	if startEnabled {
		startAct := ClickAction{Kind: ClickActionKind("key"), Key: "s"}
		serverButtons = append(serverButtons, part{t: " Start ", c: btnGreen, action: &startAct})
	} else {
		serverButtons = append(serverButtons, part{t: " Start ", c: colorDim})
	}
	serverButtons = append(serverButtons, part{t: "    "})
	if stopEnabled {
		stopAct := ClickAction{Kind: ClickActionKind("key"), Key: "s"}
		serverButtons = append(serverButtons, part{t: " Stop ", c: btnRed, action: &stopAct})
	} else {
		serverButtons = append(serverButtons, part{t: " Stop ", c: colorDim})
	}
	srvBtnRow, srvBtnRegs := composeRow(w, len(lines), nil, serverButtons, true)
	emit(srvBtnRow)
	regions = append(regions, srvBtnRegs...)

	if s.ServerStatus == "error" && s.ServerError != "" {
		errLabel := "Error"
		errRow, _ := composeRow(w, len(lines), &errLabel, []part{
			{t: s.ServerError, c: colorRed},
		}, true)
		emit(errRow)
	}

	emit(renderBottomBorder(w))
	emit("")

	// --- Session Metrics Card ---
	if s.Height >= 18 && s.Stats != nil {
		var activePill []Seg
		if s.Stats.ActiveRequests > 0 {
			activePill = []Seg{{T: fmt.Sprintf("● %d active", s.Stats.ActiveRequests), C: colorAmber}}
		} else {
			activePill = []Seg{{T: "● 0 active", C: colorDim}}
		}
		emit(renderTopBorder(w, []Seg{{T: "Session Metrics", C: colorBold}}, activePill))

		reqLabel := "Requests"
		reqParts := []part{
			{t: fmt.Sprintf("%d total", s.Stats.TotalRequests)},
			{t: " · ", c: colorDim},
			{t: fmt.Sprintf("%d ok", s.Stats.SuccessRequests), c: colorGreen},
			{t: " · ", c: colorDim},
			{t: fmt.Sprintf("%d err", s.Stats.ErrorRequests), c: ifThen(s.Stats.ErrorRequests > 0, colorRed, colorDim)},
		}
		if s.Stats.AvgLatency > 0 {
			reqParts = append(reqParts,
				part{t: " · ", c: colorDim},
				part{t: fmt.Sprintf("%s avg", proxy.FormatLatency(s.Stats.AvgLatency)), c: colorCyan},
			)
		}
		reqRow, _ := composeRow(w, len(lines), &reqLabel, reqParts, true)
		emit(reqRow)

		tokLabel := "Tokens"
		tokParts := []part{
			{t: fmt.Sprintf("%s in", proxy.FormatTokens(s.Stats.InputTokens)), c: colorCyan},
			{t: " · ", c: colorDim},
			{t: fmt.Sprintf("%s out", proxy.FormatTokens(s.Stats.OutputTokens)), c: colorAmber},
			{t: " · ", c: colorDim},
			{t: fmt.Sprintf("%s total", proxy.FormatTokens(s.Stats.TotalTokens)), c: colorBold},
		}
		tokRow, _ := composeRow(w, len(lines), &tokLabel, tokParts, true)
		emit(tokRow)

		modLabel := "Models"
		var modParts []part
		if len(s.Stats.Models) == 0 {
			modParts = append(modParts, part{t: "(no requests yet)", c: colorDim})
		} else {
			for i, m := range s.Stats.Models {
				if i > 0 {
					modParts = append(modParts, part{t: " · ", c: colorDim})
				}
				modParts = append(modParts, part{
					t: fmt.Sprintf("%s: %s (%d)", m.Model, proxy.FormatTokens(m.TotalTokens), m.Requests),
					c: colorDim,
				})
			}
		}
		modRow, _ := composeRow(w, len(lines), &modLabel, modParts, true)
		emit(modRow)

		clientLabel := "Clients"
		var clientParts []part
		if len(s.Stats.Clients) == 0 {
			clientParts = append(clientParts, part{t: "(no clients yet)", c: colorDim})
		} else {
			for i, c := range s.Stats.Clients {
				if i >= 4 {
					break
				}
				if i > 0 {
					clientParts = append(clientParts, part{t: " · ", c: colorDim})
				}
				clientParts = append(clientParts, part{
					t: fmt.Sprintf("%s (%d)", c.IP, c.Requests),
					c: colorCyan,
				})
			}
			if len(s.Stats.Clients) > 4 {
				clientParts = append(clientParts, part{
					t: fmt.Sprintf(" · +%d more", len(s.Stats.Clients)-4),
					c: colorDim,
				})
			}
		}
		clientRow, _ := composeRow(w, len(lines), &clientLabel, clientParts, true)
		emit(clientRow)

		emit(renderBottomBorder(w))
		emit("")
	}

	// --- Logs Card ---
	var logRight []Seg
	if s.LogFollowing {
		logRight = []Seg{{T: "following", C: colorGreen}}
	} else {
		logRight = []Seg{{T: fmt.Sprintf("▼ %d more · g follow", s.LogFromBottom), C: colorAmber}}
	}

	toastRowCount := 0
	if s.Toast != nil {
		toastRowCount = 1
	}
	fixed := len(lines) + 2 /* log borders */ + 1 /* footer */ + toastRowCount
	logRows := max(1, h-fixed)

	logBorderRow := emit(renderTopBorder(w, []Seg{
		{T: "Logs", C: colorBold},
		{T: fmt.Sprintf(" (%d)", s.LogTotal), C: colorDim},
	}, logRight))

	if !s.LogFollowing {
		followAct := ClickAction{Kind: ClickFollow}
		regions = append(regions, ClickRegion{
			Row:    logBorderRow,
			Col:    0,
			Width:  w,
			Action: followAct,
		})
	}

	if len(s.LogView) == 0 {
		emptyRow, _ := composeRow(w, len(lines), nil, []part{
			{t: "(no logs yet — start the server and send requests)", c: colorDim},
		}, true)
		emit(emptyRow)
	} else {
		visible := s.LogView
		if len(visible) > logRows {
			visible = visible[len(visible)-logRows:]
		}
		for _, l := range visible {
			formattedText, linkColor := formatLogLine(l.Text, w-6)
			color := logLineCode(l.Level)
			if color == "" && linkColor != "" {
				color = linkColor
			}
			row, _ := composeRow(w, len(lines), nil, []part{
				{t: formattedText, c: color},
			}, true)
			emit(row)
		}
	}
	emit(renderBottomBorder(w))

	// --- Toast & Footer ---
	if s.Toast != nil {
		toastColor := colorGreen
		if s.Toast.Kind == "err" {
			toastColor = colorRed
		} else if s.Toast.Kind == "info" {
			toastColor = colorCyan
		}
		emit(" " + paint(toastColor, TruncateToWidth("▸ "+s.Toast.Text, w-2, "…")))
	}

	footerRow := len(lines)
	var footerParts []part
	footerParts = append(footerParts, part{t: "↑↓ scroll", c: colorDim})

	footerItems := [][2]string{
		{"s", "start/stop"},
		{"l", "login"},
		{"o", "logout"},
		{"a", "acc"},
		{"u", "quota"},
		{"m", "claim"},
		{"r", "reset"},
		{"g", "follow"},
		{"q", "quit"},
		{"p", "provider"},
		{"t", "plan"},
		{"c", "clear"},
	}

	for _, item := range footerItems {
		k := item[0]
		lbl := item[1]
		act := ClickAction{Kind: ClickActionKind("key"), Key: k}
		footerParts = append(footerParts,
			part{t: " · ", c: colorDim},
			part{t: fmt.Sprintf("[%s] %s", k, lbl), c: colorDim, action: &act},
		)
	}

	footerLine, footerRegs := composeRow(w, footerRow, nil, footerParts, false)
	emit(footerLine)
	regions = append(regions, footerRegs...)

	return Frame{
		Text:    strings.Join(lines, "\r\n") + "\x1b[0m\x1b[J",
		Regions: regions,
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ifThen(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

