package tui

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/claim"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
	"github.com/yondaime-kun/zcode-proxy-go/internal/server"
)

const (
	renderInterval = 33 * time.Millisecond
	toastDuration  = 2600 * time.Millisecond
	pageScrollSize = 10
)

var version = "4.6.8-go"

type App struct {
	cfgPath         string
	debug           bool
	cfg             *config.Config
	store           *auth.MultiCredentialStore
	pool            *auth.AccountPool
	pane            *LogPane
	stats           *proxy.StatsTracker
	server          *server.Server
	serverStatus    string // "stopped", "starting", "running", "error"
	serverURL       string
	serverError     string
	toast           *Toast
	toastTimer      *time.Timer
	renderTimer     *time.Timer
	lastRenderAt    time.Time
	loginInFlight   bool
	loginHint       string
	quotaText       string
	quotaInFlight    bool
	claimInFlight    bool
	lastStoreModTime time.Time
	activeRegions    []ClickRegion
	mu              sync.Mutex
	renderTimerMu   sync.Mutex
	renderMu        sync.Mutex
	stopChan        chan struct{}
	actionChan      chan KeyAction
	termState       *term.State
	claimSchedulers []*claim.Scheduler
}

type tuiLogWriter struct {
	pane *LogPane
	app  *App
	file *os.File
}

func (w *tuiLogWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	level := "info"
	lower := strings.ToLower(text)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fail") || strings.Contains(lower, "panic") {
		level = "error"
	} else if strings.Contains(lower, "warn") {
		level = "warn"
	}

	w.pane.Push(text, level)
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	w.app.ScheduleRender()
	return len(p), nil
}

func RunTUI(cfgPath string, debug bool) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("zcode-proxy: TUI requires an interactive terminal (TTY)")
	}

	// Make terminal raw
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to initialize raw terminal: %w", err)
	}

	// Alt screen + hide cursor + SGR mouse tracking
	os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h\x1b[2J")

	app := &App{
		cfgPath:      cfgPath,
		debug:        debug,
		pane:         NewLogPane(2000),
		serverStatus: "stopped",
		stopChan:     make(chan struct{}),
		actionChan:   make(chan KeyAction, 128),
		termState:    oldState,
	}
	app.stats = proxy.NewStatsTracker(app.ScheduleRender)

	// Restore function
	defer app.cleanup()

	// Optional logfile
	var logFile *os.File
	if logPath := os.Getenv("ZCODE_TUI_LOGFILE"); logPath != "" {
		logFile, _ = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	// Redirect logger
	lw := &tuiLogWriter{pane: app.pane, app: app, file: logFile}
	origLogOutput := log.Writer()
	origLogFlags := log.Flags()
	log.SetOutput(lw)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origLogOutput)
		log.SetFlags(origLogFlags)
	}()

	// Load configuration
	if config.EnsureConfigFile(cfgPath) {
		log.Printf("Created %s from template.", cfgPath)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	app.cfg = cfg

	// Load credentials store
	app.refreshAuth()

	// Signal handling
	sigChan := make(chan os.Signal, 2)
	signals := append([]os.Signal{os.Interrupt, syscall.SIGTERM}, resizeSignals...)
	signal.Notify(sigChan, signals...)
	defer signal.Stop(sigChan)

	// Background input reader
	go app.readInputLoop()

	// Watchdog timer (bounds worst-case freezes)
	watchdog := time.NewTicker(2 * time.Second)
	defer watchdog.Stop()

	// Boot info
	log.Printf("zcode-proxy TUI (Go) — config: %s", cfgPath)
	log.Printf("provider: %s · plan: %s", app.cfg.Provider, app.cfg.Plan)

	app.renderNow()

	// Auto-start proxy if credentials exist
	if app.isLoggedIn() {
		go app.startProxy()
		go app.refreshQuota()
	} else {
		app.setToast("not logged in — press l to login", "err")
	}

	quotaTicker := time.NewTicker(2 * time.Minute)
	defer quotaTicker.Stop()

	storeCheckTicker := time.NewTicker(1 * time.Second)
	defer storeCheckTicker.Stop()

	// Event Loop
	for {
		select {
		case <-app.stopChan:
			return nil

		case <-storeCheckTicker.C:
			app.checkStoreUpdates()

		case <-quotaTicker.C:
			go app.refreshQuota()

		case sig := <-sigChan:
			if isResizeSignal(sig) {
				app.ScheduleRender()
			} else {
				return nil
			}

		case action := <-app.actionChan:
			if action.Type == ActionCtrlC {
				return nil
			}
			app.handleAction(action)

		case <-watchdog.C:
			app.mu.Lock()
			stale := time.Since(app.lastRenderAt) > 3*time.Second
			app.mu.Unlock()
			if stale {
				app.ScheduleRender()
			}
		}
	}
}

func (a *App) cleanup() {
	a.mu.Lock()
	a.stopProxyLocked()
	a.mu.Unlock()

	// Exit alt screen, restore cursor, disable mouse
	os.Stdout.WriteString("\x1b[0m\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l")
	if a.termState != nil {
		_ = term.Restore(int(os.Stdin.Fd()), a.termState)
	}
}

func (a *App) readInputLoop() {
	parser := NewKeyParser()
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			select {
			case <-a.stopChan:
				return
			default:
				a.actionChan <- KeyAction{Type: ActionCtrlC}
				return
			}
		}
		if n > 0 {
			actions := parser.Feed(string(buf[:n]))
			for _, act := range actions {
				select {
				case <-a.stopChan:
					return
				case a.actionChan <- act:
				}
			}
		}
	}
}

func (a *App) refreshAuth() {
	a.mu.Lock()
	defer a.mu.Unlock()

	store, err := auth.LoadStore()
	if err != nil {
		a.store = &auth.MultiCredentialStore{
			Active:   "default",
			Accounts: make(map[string]*auth.Credential),
		}
	} else {
		a.store = store
	}

	if len(a.store.Accounts) == 0 {
		// Try import from zcode config
		if impCred, err := auth.ImportFromZCodeConfig(a.cfg.Provider); err == nil && impCred != nil {
			a.store.Accounts["default"] = impCred
			a.store.Active = "default"
			_ = a.store.Save()
		}
	}

	if a.pool == nil {
		a.pool = auth.NewAccountPool(a.store, a.cfg.Routing)
	} else {
		a.pool.ReloadFromStore(a.store)
	}
	if fi, err := os.Stat(auth.GetStorePath()); err == nil {
		a.lastStoreModTime = fi.ModTime()
	}
	a.scheduleRenderLocked()
}

func (a *App) checkStoreUpdates() {
	p := auth.GetStorePath()
	fi, err := os.Stat(p)
	if err != nil {
		return
	}
	mod := fi.ModTime()
	a.mu.Lock()
	if mod.After(a.lastStoreModTime) {
		a.lastStoreModTime = mod
		a.mu.Unlock()

		a.refreshAuth()
		go a.refreshQuota()
		log.Printf("[auth] credentials updated from disk (%d accounts, active: %s)", len(a.store.Accounts), a.store.Active)
		a.setToast(fmt.Sprintf("accounts updated (%d total)", len(a.store.Accounts)), "ok")
		return
	}
	a.mu.Unlock()
}

func (a *App) refreshQuota() {
	a.mu.Lock()
	if a.quotaInFlight {
		a.mu.Unlock()
		return
	}
	cred := a.getActiveCred()
	if cred == nil || cred.Jwt == "" {
		a.quotaText = "no start-plan JWT"
		a.scheduleRenderLocked()
		a.mu.Unlock()
		return
	}
	a.quotaInFlight = true
	jwt := cred.Jwt
	cfg := a.cfg
	a.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client := claim.NewClient(cfg.Claim.Origin, jwt, cfg)
		balances, err := client.GetBalances(ctx)

		a.mu.Lock()
		a.quotaInFlight = false
		if err != nil {
			a.quotaText = "fetch failed"
		} else {
			a.quotaText = claim.FormatQuotaSummary(balances)
		}
		a.scheduleRenderLocked()
		a.mu.Unlock()
	}()
}

func (a *App) claimNow() {
	a.mu.Lock()
	if a.claimInFlight {
		a.mu.Unlock()
		return
	}
	cred := a.getActiveCred()
	accName := a.store.Active
	if accName == "" {
		accName = "default"
	}
	if cred == nil || cred.Jwt == "" {
		a.setToastLocked(fmt.Sprintf("[%s] no start-plan JWT", accName), "warn")
		a.mu.Unlock()
		return
	}
	a.claimInFlight = true
	jwt := cred.Jwt
	cfg := a.cfg
	tag := fmt.Sprintf("[%s]", accName)
	if cred.UserId != "" {
		uid := cred.UserId
		if len(uid) > 8 {
			uid = uid[:8] + "…"
		}
		tag = fmt.Sprintf("[%s · %s]", accName, uid)
	}
	a.setToastLocked(fmt.Sprintf("%s checking claims...", tag), "info")
	a.scheduleRenderLocked()
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.claimInFlight = false
			a.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		client := claim.NewClient(cfg.Claim.Origin, jwt, cfg)
		plans, err := client.GetPreviews(ctx)
		if err != nil {
			log.Printf("[claim] %s preview failed: %v", tag, err)
			a.setToast(fmt.Sprintf("%s claim check failed", tag), "err")
			return
		}
		if len(plans) == 0 {
			log.Printf("[claim] %s no claimable plans right now", tag)
			a.setToast(fmt.Sprintf("%s no claimable plans", tag), "info")
			return
		}

		target := &plans[0]
		wanted := cfg.Claim.PlanId
		if wanted != "" {
			for i := range plans {
				if plans[i].PlanID == wanted {
					target = &plans[i]
					break
				}
			}
		}

		log.Printf("[claim] %s found active plan: %s (%s, priority %d). Claiming...", tag, target.PlanID, target.Name, target.Priority)
		token, err := proxy.SolveCaptchaOnDemand(ctx, cfg.Identity.AppVersion)
		if err != nil || token == nil || token.VerifyParam == "" {
			log.Printf("[claim] %s captcha required for claim but solver failed: %v", tag, err)
			a.setToast(fmt.Sprintf("%s claim failed: captcha solver failed", tag), "warn")
			return
		}
		outcome, err := client.Claim(ctx, target.PlanID, token.VerifyParam, token.Region)
		if err != nil {
			log.Printf("[claim] %s claim request failed: %v", tag, err)
			a.setToast(fmt.Sprintf("%s claim failed: %v", tag, err), "err")
			return
		}

		if outcome.OK {
			log.Printf("[claim] %s SUCCESS: Successfully claimed %s", tag, outcome.PlanID)
			a.setToast(fmt.Sprintf("%s claimed %s!", tag, outcome.PlanID), "ok")
			go a.refreshQuota()
		} else {
			log.Printf("[claim] %s Claim failed: %s (code %d: %s)", tag, outcome.FailureKind, outcome.Code, outcome.Message)
			a.setToast(fmt.Sprintf("%s claim failed: %s", tag, outcome.FailureKind), "warn")
		}
	}()
}

func (a *App) isLoggedIn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store != nil && len(a.store.Accounts) > 0
}

func (a *App) getActiveCred() *auth.Credential {
	if a.store == nil || len(a.store.Accounts) == 0 {
		return nil
	}
	if cred, ok := a.store.Accounts[a.store.Active]; ok {
		return cred
	}
	for _, c := range a.store.Accounts {
		return c
	}
	return nil
}

func (a *App) ScheduleRender() {
	a.renderTimerMu.Lock()
	defer a.renderTimerMu.Unlock()
	if a.renderTimer != nil {
		return
	}
	a.renderTimer = time.AfterFunc(renderInterval, func() {
		a.renderTimerMu.Lock()
		a.renderTimer = nil
		a.renderTimerMu.Unlock()
		a.renderNow()
	})
}

func (a *App) scheduleRenderLocked() {
	a.ScheduleRender()
}

func (a *App) setToastLocked(text string, kind string) {
	a.toast = &Toast{Text: text, Kind: kind}
	if a.toastTimer != nil {
		a.toastTimer.Stop()
	}
	a.toastTimer = time.AfterFunc(toastDuration, func() {
		a.mu.Lock()
		a.toast = nil
		a.toastTimer = nil
		a.scheduleRenderLocked()
		a.mu.Unlock()
	})
	a.scheduleRenderLocked()
}

func (a *App) setToast(text string, kind string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setToastLocked(text, kind)
}

func (a *App) renderNow() {
	a.renderMu.Lock()
	defer a.renderMu.Unlock()

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		w, h, err = term.GetSize(int(os.Stdin.Fd()))
	}
	if err != nil || w <= 0 || h <= 0 {
		w = 80
		h = 24
	}

	a.mu.Lock()
	activeAccount := ""
	apiKeyPreview := ""
	accountCount := 0
	loggedIn := false
	if a.store != nil && len(a.store.Accounts) > 0 {
		loggedIn = true
		accountCount = len(a.store.Accounts)
		activeAccount = a.store.Active
		if cred := a.getActiveCred(); cred != nil {
			if len(cred.ApiKey) > 8 {
				apiKeyPreview = cred.ApiKey[:8] + "…"
			} else {
				apiKeyPreview = cred.ApiKey
			}
		}
	}

	paneLines := max(4, h-20)
	if h < 18 {
		paneLines = max(4, h-14)
	}
	view := a.pane.View(paneLines)
	var statsSnap *proxy.StatsSnapshot
	if a.stats != nil {
		snap := a.stats.Snapshot()
		statsSnap = &snap
	}
	state := &FrameState{
		Version:          version,
		ConfigPath:       a.cfgPath,
		Provider:         a.cfg.Provider,
		Plan:             a.cfg.Plan,
		LoggedIn:         loggedIn,
		ApiKeyPreview:    apiKeyPreview,
		ActiveAccount:    activeAccount,
		AccountCount:     accountCount,
		QuotaText:        a.quotaText,
		LoginInFlight:    a.loginInFlight,
		LoginHint:        a.loginHint,
		ServerStatus:     a.serverStatus,
		ServerURL:        a.serverURL,
		ServerError:      a.serverError,
		ModelCount:       len(a.cfg.Models),
		ResponsesEnabled: a.cfg.Responses.Enabled,
		ClaimAuto:        a.cfg.Claim.Enabled && a.cfg.Claim.Auto,
		WhitelistCount:   len(a.cfg.Security.Whitelist),
		BlacklistCount:   len(a.cfg.Security.Blacklist),
		Stats:            statsSnap,
		LogTotal:         view.Total,
		LogView:          view.Lines,
		LogFollowing:     a.pane.Following(),
		LogFromBottom:    view.FromBottom,
		Toast:            a.toast,
		Width:            w,
		Height:           h,
	}
	a.lastRenderAt = time.Now()
	a.mu.Unlock()

	frame := BuildFrame(state)

	a.mu.Lock()
	a.activeRegions = frame.Regions
	a.mu.Unlock()

	// Write frame directly to terminal cursor home
	os.Stdout.WriteString("\x1b[H" + frame.Text)
}

func (a *App) startProxy() {
	a.mu.Lock()
	if a.serverStatus == "running" || a.serverStatus == "starting" {
		a.mu.Unlock()
		return
	}
	a.serverStatus = "starting"
	a.serverError = ""
	a.scheduleRenderLocked()
	a.mu.Unlock()

	if !a.isLoggedIn() {
		a.mu.Lock()
		a.serverStatus = "stopped"
		a.setToastLocked("not logged in — press l to login", "err")
		a.mu.Unlock()
		return
	}

	srv := server.NewServerWithPool(a.cfg, a.pool)
	if a.stats != nil {
		srv.ProxyHandler().SetStats(a.stats)
	}
	a.mu.Lock()
	a.server = srv
	a.mu.Unlock()

	// Start server goroutine
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			a.mu.Lock()
			a.serverStatus = "error"
			a.serverError = err.Error()
			a.setToastLocked(fmt.Sprintf("proxy failed: %v", err), "err")
			a.mu.Unlock()
		}
	}()

	// Wait brief moment to verify bound port
	time.Sleep(150 * time.Millisecond)

	a.mu.Lock()
	if a.serverStatus == "error" {
		a.mu.Unlock()
		return
	}
	a.serverStatus = "running"
	a.serverURL = fmt.Sprintf("http://%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	a.setToastLocked(fmt.Sprintf("proxy started on %s", a.serverURL), "ok")
	a.scheduleRenderLocked()

	claimEnabled := a.cfg.Claim.Enabled && a.cfg.Claim.Auto
	var eligibleAccounts map[string]*auth.Credential
	if claimEnabled && a.store != nil {
		eligibleAccounts = make(map[string]*auth.Credential)
		for name, acc := range a.store.Accounts {
			if acc != nil && acc.Jwt != "" {
				eligibleAccounts[name] = acc
			}
		}
	}
	a.mu.Unlock()

	// Start background claim schedulers outside of a.mu lock
	if claimEnabled && len(eligibleAccounts) > 0 {
		var newSchedulers []*claim.Scheduler
		var claimAccs []string
		for name, acc := range eligibleAccounts {
			scheduler := claim.StartScheduler(a.cfg, name, acc, func(accName, planID string) {
				a.setToast(fmt.Sprintf("[%s] claimed %s", accName, planID), "ok")
				go a.refreshQuota()
			})
			newSchedulers = append(newSchedulers, scheduler)
			claimAccs = append(claimAccs, name)
		}

		a.mu.Lock()
		if a.serverStatus == "running" {
			a.claimSchedulers = append(a.claimSchedulers, newSchedulers...)
		} else {
			for _, s := range newSchedulers {
				s.Stop()
			}
		}
		a.mu.Unlock()

		if len(claimAccs) > 0 {
			sort.Strings(claimAccs)
			log.Printf("[claim] auto ON for %d accounts: %s (poll %ds)", len(claimAccs), strings.Join(claimAccs, ", "), a.cfg.Claim.PollIntervalMs/1000)
		}
	}
}

func (a *App) stopProxy() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopProxyLocked()
}

func (a *App) stopProxyLocked() {
	if a.server == nil {
		return
	}
	for _, s := range a.claimSchedulers {
		s.Stop()
	}
	a.claimSchedulers = nil

	srv := a.server
	a.server = nil
	a.serverStatus = "stopped"
	a.serverURL = ""
	a.setToastLocked("proxy stopped", "info")

	go func(s *server.Server) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}(srv)
}

func (a *App) toggleProxy() {
	a.mu.Lock()
	running := a.serverStatus == "running" || a.serverStatus == "starting"
	a.mu.Unlock()

	if running {
		a.stopProxy()
	} else {
		go a.startProxy()
	}
}

func (a *App) switchProvider() {
	a.mu.Lock()
	if a.serverStatus == "running" || a.serverStatus == "starting" {
		a.setToastLocked("stop proxy before switching provider (press s)", "err")
		a.mu.Unlock()
		return
	}
	next := "zai"
	if a.cfg.Provider == "zai" {
		next = "bigmodel"
	}
	a.cfg.Provider = next
	_ = config.UpdateConfigYaml(a.cfgPath, map[string]string{"provider": next})
	a.setToastLocked(fmt.Sprintf("provider → %s", next), "ok")
	a.mu.Unlock()
	log.Printf("config updated: provider=%s", next)
}

func (a *App) switchPlan() {
	a.mu.Lock()
	if a.serverStatus == "running" || a.serverStatus == "starting" {
		a.setToastLocked("stop proxy before switching plan (press s)", "err")
		a.mu.Unlock()
		return
	}
	next := "coding-plan"
	if a.cfg.Plan == "coding-plan" {
		next = "start-plan"
	}
	a.cfg.Plan = next
	_ = config.UpdateConfigYaml(a.cfgPath, map[string]string{"plan": next})
	a.setToastLocked(fmt.Sprintf("plan → %s", next), "ok")
	a.mu.Unlock()
	log.Printf("config updated: plan=%s", next)
}

func (a *App) switchAccount() {
	a.mu.Lock()
	if a.store == nil || len(a.store.Accounts) <= 1 {
		a.setToastLocked("only 1 account configured", "info")
		a.mu.Unlock()
		return
	}

	var aliases []string
	for k := range a.store.Accounts {
		aliases = append(aliases, k)
	}
	curIdx := 0
	for i, k := range aliases {
		if k == a.store.Active {
			curIdx = i
			break
		}
	}
	nextIdx := (curIdx + 1) % len(aliases)
	nextAlias := aliases[nextIdx]
	a.store.Active = nextAlias
	_ = a.store.Save()
	if a.pool != nil {
		a.pool.ReloadFromStore(a.store)
	}
	a.setToastLocked(fmt.Sprintf("switched active account → %s", nextAlias), "ok")
	a.scheduleRenderLocked()
	a.mu.Unlock()

	log.Printf("switched active account to %s", nextAlias)
	go a.refreshQuota()
}

func (a *App) logout() {
	a.mu.Lock()
	if a.serverStatus == "running" || a.serverStatus == "starting" {
		a.setToastLocked("stop proxy before logging out (press s)", "err")
		a.mu.Unlock()
		return
	}
	_ = auth.ClearCredential()
	a.mu.Unlock()
	a.refreshAuth()
	a.setToast("logged out", "ok")
	log.Printf("all credentials cleared")
}

func (a *App) startLogin() {
	a.mu.Lock()
	if a.loginInFlight {
		a.setToastLocked("login already in progress", "info")
		a.mu.Unlock()
		return
	}
	a.loginInFlight = true
	a.loginHint = "open authorization URL in Logs below…"
	provider := a.cfg.Provider
	a.scheduleRenderLocked()
	a.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		var oauthResult *auth.OAuthResult
		var err error
		if provider == "bigmodel" {
			oauthResult, err = auth.LoginBigmodel(ctx)
		} else {
			oauthResult, err = auth.LoginZai(ctx)
		}

		a.mu.Lock()
		a.loginHint = "resolving credentials…"
		a.scheduleRenderLocked()
		a.mu.Unlock()

		if err != nil {
			a.mu.Lock()
			a.loginInFlight = false
			a.loginHint = ""
			a.scheduleRenderLocked()
			a.mu.Unlock()

			log.Printf("OAuth login failed: %v", err)
			a.setToast(fmt.Sprintf("login failed: %v", err), "err")
			return
		}

		resolver := auth.NewKeyResolver()
		cred, err := resolver.ResolveCodingPlanCredential(oauthResult.AccessToken, provider, oauthResult.UserId)
		if err != nil {
			if oauthResult.Jwt != "" {
				log.Printf("Coding-plan API key resolution skipped/failed (%v), using Start-Plan JWT", err)
				cred = &auth.Credential{
					Provider: provider,
					UserId:   oauthResult.UserId,
					Jwt:      oauthResult.Jwt,
					ApiKey:   "start-plan",
				}
			} else {
				a.mu.Lock()
				a.loginInFlight = false
				a.loginHint = ""
				a.scheduleRenderLocked()
				a.mu.Unlock()

				log.Printf("Failed to resolve credential: %v", err)
				a.setToast(fmt.Sprintf("key resolve failed: %v", err), "err")
				return
			}
		} else {
			if oauthResult.Jwt != "" {
				cred.Jwt = oauthResult.Jwt
			}
		}

		a.mu.Lock()
		a.loginInFlight = false
		a.loginHint = ""
		a.scheduleRenderLocked()
		a.mu.Unlock()

		store, _ := auth.LoadStore()
		if store == nil {
			store = &auth.MultiCredentialStore{
				Accounts: make(map[string]*auth.Credential),
			}
		}
		if store.Accounts == nil {
			store.Accounts = make(map[string]*auth.Credential)
		}

		accountName := ""
		for name, existing := range store.Accounts {
			if existing == nil {
				continue
			}
			if (oauthResult.UserId != "" && existing.UserId == oauthResult.UserId) ||
				(cred.ApiKey != "" && cred.ApiKey != "start-plan" && existing.ApiKey == cred.ApiKey) {
				accountName = name
				break
			}
		}

		if accountName == "" {
			if _, exists := store.Accounts["default"]; !exists {
				accountName = "default"
			} else {
				for i := 2; ; i++ {
					candidate := fmt.Sprintf("acc%d", i)
					if _, exists := store.Accounts[candidate]; !exists {
						accountName = candidate
						break
					}
				}
			}
		}

		store.Accounts[accountName] = cred
		store.Active = accountName
		if err := store.Save(); err != nil {
			log.Printf("Failed to save credential: %v", err)
			a.setToast("failed to save credential", "err")
			return
		}

		a.refreshAuth()
		go a.refreshQuota()
		log.Printf("OAuth login successful for %s (saved as [%s], %d accounts configured)", provider, accountName, len(store.Accounts))
		a.setToast(fmt.Sprintf("logged in as [%s] (%d accounts)", accountName, len(store.Accounts)), "ok")

		a.mu.Lock()
		shouldStart := a.serverStatus == "stopped" || a.serverStatus == "error"
		a.mu.Unlock()
		if shouldStart {
			go a.startProxy()
		}
	}()
}

func (a *App) handleAction(action KeyAction) {
	switch action.Type {
	case ActionClick:
		a.mu.Lock()
		regions := a.activeRegions
		a.mu.Unlock()
		hit := FindRegion(regions, action.Y, action.X)
		if hit != nil {
			a.dispatchClick(*hit)
		}

	case ActionWheelUp:
		a.pane.ScrollUp(3)
		a.ScheduleRender()

	case ActionWheelDown:
		a.pane.ScrollDown(3)
		a.ScheduleRender()

	case ActionUp:
		a.pane.ScrollUp(1)
		a.ScheduleRender()

	case ActionDown:
		a.pane.ScrollDown(1)
		a.ScheduleRender()

	case ActionPageUp:
		a.pane.ScrollUp(pageScrollSize)
		a.ScheduleRender()

	case ActionPageDown:
		a.pane.ScrollDown(pageScrollSize)
		a.ScheduleRender()

	case ActionHome:
		a.pane.ScrollUp(a.pane.Count())
		a.ScheduleRender()

	case ActionEnd:
		a.pane.FollowBottom()
		a.ScheduleRender()

	case ActionChar:
		switch strings.ToLower(action.Key) {
		case "q":
			close(a.stopChan)
		case "s":
			a.toggleProxy()
		case "l":
			a.startLogin()
		case "o":
			a.logout()
		case "a":
			a.switchAccount()
		case "u":
			go a.refreshQuota()
			a.setToast("refreshing live quota...", "info")
		case "m":
			a.claimNow()
		case "r":
			if a.stats != nil {
				a.stats.Reset()
				a.setToast("session metrics reset", "ok")
				a.ScheduleRender()
			}
		case "p":
			a.switchProvider()
		case "t":
			a.switchPlan()
		case "c":
			a.pane.Clear()
			a.ScheduleRender()
		case "g":
			a.pane.FollowBottom()
			a.ScheduleRender()
		case "k":
			a.pane.ScrollUp(1)
			a.ScheduleRender()
		case "j":
			a.pane.ScrollDown(1)
			a.ScheduleRender()
		}
	}
}

func (a *App) dispatchClick(action ClickAction) {
	switch action.Kind {
	case ClickActionKind("key"):
		a.handleAction(KeyAction{Type: ActionChar, Key: action.Key})
	case ClickProvider:
		a.switchProvider()
	case ClickPlan:
		a.switchPlan()
	case ClickFollow:
		a.pane.FollowBottom()
		a.ScheduleRender()
	case ClickAccount:
		a.switchAccount()
	}
}
