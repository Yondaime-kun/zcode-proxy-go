package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/yondaime-kun/zcode-proxy-go/internal/auth"
	"github.com/yondaime-kun/zcode-proxy-go/internal/claim"
	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
	"github.com/yondaime-kun/zcode-proxy-go/internal/server"
	"github.com/yondaime-kun/zcode-proxy-go/internal/tui"
)

var Version = "4.6.5-go"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			runTUI(nil)
		} else {
			runServe(nil)
		}
		return
	}

	cmd := args[0]
	switch cmd {
	case "tui":
		runTUI(args[1:])
	case "serve":
		runServe(args[1:])
	case "--cli":
		runServe(args[1:])
	case "auth":
		runAuth(args[1:])
	case "status":
		runStatus(args[1:])
	case "quota":
		runQuota(args[1:])
	case "claim":
		runClaim(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("zcode-proxy %s\n", Version)
	case "help", "--help", "-h":
		printHelp()
	default:
		if strings.HasSuffix(cmd, ".yaml") || strings.HasSuffix(cmd, ".yml") {
			if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
				runTUI(args)
			} else {
				runServe(args)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printHelp()
		os.Exit(1)
	}
}

func runTUI(args []string) {
	cfgPath := getConfigPath(args)
	if err := tui.RunTUI(cfgPath, false); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Printf(`zcode-proxy %s (Golang Ultra-Low RAM Edition)

Usage:
  zcode-proxy [config.yaml]        Start interactive TUI (or headless if non-TTY)
  zcode-proxy tui [config.yaml]    Start interactive Terminal UI
  zcode-proxy serve [config.yaml]  Start headless proxy server
  zcode-proxy --cli [config.yaml]  Start headless proxy server
  zcode-proxy auth login <zai|bigmodel> [--name <alias>] [--import]
                                   Login via OAuth with optional account alias
  zcode-proxy auth list            List all configured accounts
  zcode-proxy auth switch <alias>  Switch default active account
  zcode-proxy auth remove <alias>  Remove a configured account
  zcode-proxy auth status          Check authentication status
  zcode-proxy auth logout          Clear all stored credentials
  zcode-proxy status               All-in-one system & account pool dashboard
  zcode-proxy quota [account]      Check live balance/quota for accounts
  zcode-proxy claim [list|now]     List or claim trial/weekend packages for accounts
  zcode-proxy version              Show version
  zcode-proxy help                 Show this help

Examples:
  zcode-proxy
  zcode-proxy status
  zcode-proxy auth login zai --name acc1
  zcode-proxy auth login zai --name acc2
  zcode-proxy auth list
  zcode-proxy quota
  zcode-proxy claim list
`, Version)
}

func getConfigPath(args []string) string {
	for _, a := range args {
		if strings.HasSuffix(a, ".yaml") || strings.HasSuffix(a, ".yml") {
			return a
		}
	}
	if p := os.Getenv("ZCODE_PROXY_CONFIG"); p != "" {
		return p
	}
	return "config.yaml"
}

func runServe(args []string) {
	cfgPath := getConfigPath(args)
	if config.EnsureConfigFile(cfgPath) {
		log.Printf("Created %s from default configuration.", cfgPath)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	store, err := auth.LoadStore()
	if err != nil {
		log.Printf("Warning: failed to load credentials store: %v", err)
	}

	if store == nil || len(store.Accounts) == 0 {
		// Attempt auto-import from ~/.zcode/v2/config.json
		impCred, impErr := auth.ImportFromZCodeConfig(cfg.Provider)
		if impErr == nil && impCred != nil {
			log.Printf("Auto-imported credentials for %s from ~/.zcode/v2/config.json", cfg.Provider)
			_ = auth.SaveAccount("default", impCred)
			store = &auth.MultiCredentialStore{
				Active:   "default",
				Accounts: map[string]*auth.Credential{"default": impCred},
			}
		}
	}

	if store == nil || len(store.Accounts) == 0 {
		fmt.Fprintf(os.Stderr, "Not logged in. Run: zcode-proxy auth login %s [--name <alias>]\n", cfg.Provider)
		os.Exit(1)
	}

	pool := auth.NewAccountPool(store, cfg.Routing)
	log.Printf("Account pool loaded: %d account(s) (routing: %s, active: %q)", pool.TotalCount(), pool.Mode(), store.Active)

	// Auto-claim background scheduler for accounts with start-plan JWT
	if cfg.Claim.Enabled && cfg.Claim.Auto {
		claimCount := 0
		var claimAccs []string
		for name, acc := range store.Accounts {
			if acc != nil && acc.Jwt != "" {
				scheduler := claim.StartScheduler(cfg, name, acc)
				defer scheduler.Stop()
				claimCount++
				claimAccs = append(claimAccs, name)
			}
		}
		if claimCount > 0 {
			sort.Strings(claimAccs)
			log.Printf("  claim: auto ON (poll %ds, %d accounts: %s)", cfg.Claim.PollIntervalMs/1000, claimCount, strings.Join(claimAccs, ", "))
		}
	}

	srv := server.NewServerWithPool(cfg, pool)

	stopChan := make(chan os.Signal, 1)
	signal.Ignore(syscall.SIGHUP)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stopChan
	log.Println("\nShutting down zcode-proxy...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func runAuth(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: zcode-proxy auth <login|list|switch|remove|status|logout>")
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "list":
		store, err := auth.LoadStore()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading store: %v\n", err)
			os.Exit(1)
		}
		if store == nil || len(store.Accounts) == 0 {
			fmt.Println("No accounts configured yet. Run: zcode-proxy auth login <zai|bigmodel> [--name <alias>]")
			return
		}

		fmt.Printf("Configured Accounts (%d total, active: %s):\n\n", len(store.Accounts), store.Active)
		for name, cred := range store.Accounts {
			prefix := "  "
			if name == store.Active {
				prefix = "* "
			}
			keyDisplay := cred.ApiKey
			if len(keyDisplay) > 12 {
				keyDisplay = keyDisplay[:12] + "..."
			}
			jwtDisplay := "none"
			if cred.Jwt != "" {
				jwtDisplay = cred.Jwt
				if len(jwtDisplay) > 15 {
					jwtDisplay = jwtDisplay[:15] + "..."
				}
			}
			fmt.Printf("%s[%s] (provider: %s)\n", prefix, name, cred.Provider)
			if keyDisplay != "" && keyDisplay != "start-plan" {
				fmt.Printf("    API Key:        %s\n", keyDisplay)
			}
			fmt.Printf("    Start-Plan JWT: %s\n", jwtDisplay)
			if cred.UserId != "" {
				fmt.Printf("    User ID:        %s\n", cred.UserId)
			}
			fmt.Println()
		}

	case "switch":
		if len(args) < 2 {
			fmt.Println("Usage: zcode-proxy auth switch <alias>")
			os.Exit(1)
		}
		alias := args[1]
		if err := auth.SetActiveAccount(alias); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to switch account: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active account switched to %q\n", alias)

	case "remove":
		if len(args) < 2 {
			fmt.Println("Usage: zcode-proxy auth remove <alias>")
			os.Exit(1)
		}
		alias := args[1]
		if err := auth.RemoveAccount(alias); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove account: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Account %q removed.\n", alias)

	case "status":
		runStatus(args[1:])

	case "logout":
		if err := auth.ClearCredential(); err != nil {
			fmt.Fprintf(os.Stderr, "Logout error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Logged out. All stored credentials removed.")

	case "login":
		if len(args) < 2 {
			fmt.Println("Usage: zcode-proxy auth login <zai|bigmodel> [--name <alias>] [--import]")
			os.Exit(1)
		}
		provider := args[1]
		if provider != "zai" && provider != "bigmodel" {
			fmt.Println("Provider must be 'zai' or 'bigmodel'")
			os.Exit(1)
		}

		accountName := ""
		isImport := false
		for i := 2; i < len(args); i++ {
			if args[i] == "--import" {
				isImport = true
			} else if args[i] == "--name" && i+1 < len(args) {
				accountName = args[i+1]
				i++
			} else if strings.HasPrefix(args[i], "--name=") {
				accountName = strings.TrimPrefix(args[i], "--name=")
			}
		}

		if accountName == "" {
			existingStore, _ := auth.LoadStore()
			if existingStore == nil || len(existingStore.Accounts) == 0 || existingStore.Accounts["default"] == nil {
				accountName = "default"
			} else {
				for i := 2; ; i++ {
					candidate := fmt.Sprintf("acc%d", i)
					if _, exists := existingStore.Accounts[candidate]; !exists {
						accountName = candidate
						break
					}
				}
				fmt.Printf("Notice: 'default' account already exists. Saving new account as %q (use --name <alias> to specify custom name).\n\n", accountName)
			}
		}

		if isImport {
			cred, err := auth.ImportFromZCodeConfig(provider)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Import failed: %v\n", err)
				os.Exit(1)
			}
			if err := auth.SaveAccount(accountName, cred); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to save credential: %v\n", err)
				os.Exit(1)
			}
			keyPreview := cred.ApiKey
			if len(keyPreview) > 12 {
				keyPreview = keyPreview[:12] + "..."
			}
			fmt.Printf("Successfully imported %s credential to account %q\n", provider, accountName)
			fmt.Printf("  API Key: %s\n", keyPreview)
			if cred.Jwt != "" {
				jwtPreview := cred.Jwt
				if len(jwtPreview) > 15 {
					jwtPreview = jwtPreview[:15] + "..."
				}
				fmt.Printf("  Start-Plan JWT: %s\n", jwtPreview)
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()

		var res *auth.OAuthResult
		var err error
		if provider == "zai" {
			res, err = auth.LoginZai(ctx)
		} else {
			res, err = auth.LoginBigmodel(ctx)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\nResolving upstream API key...")
		resolver := auth.NewKeyResolver()
		cred, err := resolver.ResolveCodingPlanCredential(res.AccessToken, provider, res.UserId)
		if err != nil {
			if res.Jwt != "" {
				fmt.Printf("Notice: Coding-plan API key resolution skipped or failed (%v).\nStart-Plan JWT is active!\n", err)
				cred = &auth.Credential{
					Provider: provider,
					UserId:   res.UserId,
					Jwt:      res.Jwt,
					ApiKey:   "start-plan",
				}
			} else {
				fmt.Fprintf(os.Stderr, "Failed to resolve API key: %v\n", err)
				os.Exit(1)
			}
		} else {
			if res.Jwt != "" {
				cred.Jwt = res.Jwt
			}
		}

		if err := auth.SaveAccount(accountName, cred); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save credential: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\nLogged in successfully as %s (account: %s)!\n", provider, accountName)
		if cred.ApiKey != "" && cred.ApiKey != "start-plan" {
			keyPreview := cred.ApiKey
			if len(keyPreview) > 12 {
				keyPreview = keyPreview[:12] + "..."
			}
			fmt.Printf("  API Key: %s\n", keyPreview)
		}
		if cred.Jwt != "" {
			jwtPreview := cred.Jwt
			if len(jwtPreview) > 15 {
				jwtPreview = jwtPreview[:15] + "..."
			}
			fmt.Printf("  Start-Plan JWT: %s\n", jwtPreview)
		}
		fmt.Printf("  Store:   %s\n", auth.GetStorePath())

	default:
		fmt.Fprintf(os.Stderr, "Unknown auth subcommand: %s\n", sub)
		os.Exit(1)
	}
}

func runClaim(args []string) {
	mode := "now"
	if len(args) > 0 {
		mode = args[0]
	}
	if mode != "list" && mode != "now" {
		fmt.Println("Usage: zcode-proxy claim [list|now]")
		os.Exit(1)
	}

	cfgPath := getConfigPath(args)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	store, err := auth.LoadStore()
	if err != nil || store == nil || len(store.Accounts) == 0 {
		fmt.Fprintln(os.Stderr, "Claim requires active accounts. Run: zcode-proxy auth login <zai|bigmodel>")
		os.Exit(1)
	}

	for name, cred := range store.Accounts {
		if cred == nil || cred.Jwt == "" {
			fmt.Printf("Skipping account [%s]: no start-plan JWT\n", name)
			continue
		}

		fmt.Printf("\n=== Checking claims for account [%s] ===\n", name)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		client := claim.NewClient(cfg.Claim.Origin, cred.Jwt, cfg)
		plans, err := client.GetPreviews(ctx)
		if err != nil {
			fmt.Printf("Preview failed for [%s]: %v\n", name, err)
			cancel()
			continue
		}

		if len(plans) == 0 {
			fmt.Printf("No claimable plans right now for [%s].\n", name)
			cancel()
			continue
		}

		claim.PrintPlans(plans)

		if mode == "list" {
			cancel()
			continue
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
		fmt.Printf("[%s] Claiming target plan: %s...\n", name, target.PlanID)

		token, _ := proxy.SolveCaptchaOnDemand(ctx, cfg.Identity.AppVersion)
		outcome, err := client.Claim(ctx, target.PlanID, token.VerifyParam, token.Region)
		cancel()

		if err != nil {
			fmt.Printf("Claim error for [%s]: %v\n", name, err)
			continue
		}

		if outcome.OK {
			fmt.Printf("Successfully claimed %s for [%s]!\n", outcome.PlanID, name)
			if outcome.StartsAt != nil {
				fmt.Printf("  Activates: %s\n", time.Unix(*outcome.StartsAt, 0).Format(time.RFC3339))
			}
			if outcome.EndsAt != nil {
				fmt.Printf("  Expires:   %s\n", time.Unix(*outcome.EndsAt, 0).Format(time.RFC3339))
			}
		} else {
			fmt.Printf("Claim failed for [%s]: %s (code %d: %s)\n", name, outcome.FailureKind, outcome.Code, outcome.Message)
		}
	}
}

func runQuota(args []string) {
	cfgPath := getConfigPath(args)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	store, err := auth.LoadStore()
	if err != nil || store == nil || len(store.Accounts) == 0 {
		fmt.Fprintln(os.Stderr, "Not logged in. Run: zcode-proxy auth login <zai|bigmodel>")
		os.Exit(1)
	}

	targetAccount := ""
	for _, a := range args {
		if !strings.HasSuffix(a, ".yaml") && !strings.HasSuffix(a, ".yml") && !strings.HasPrefix(a, "-") {
			targetAccount = a
			break
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	balanceURL := fmt.Sprintf("%s/api/v1/zcode-plan/billing/balance?app_version=%s&platform=linux-x64", cfg.Claim.Origin, cfg.Identity.AppVersion)

	fmt.Printf("Querying live quota from %s...\n\n", cfg.Claim.Origin)

	for name, cred := range store.Accounts {
		if targetAccount != "" && name != targetAccount {
			continue
		}
		if cred == nil || cred.Jwt == "" {
			fmt.Printf("Account [%s]: no start-plan JWT\n\n", name)
			continue
		}

		req, err := http.NewRequest("GET", balanceURL, nil)
		if err != nil {
			continue
		}
		headers := proxy.BuildControlIdentityHeaders(cfg)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Authorization", "Bearer "+cred.Jwt)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Account [%s]: failed to fetch quota: %v\n\n", name, err)
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var res struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Balances []struct {
					ShowName       string `json:"show_name"`
					TotalUnits     int64  `json:"total_units"`
					UsedUnits      int64  `json:"used_units"`
					RemainingUnits int64  `json:"remaining_units"`
					UnitType       string `json:"unit_type"`
				} `json:"balances"`
			} `json:"data"`
		}

		_ = json.Unmarshal(bodyBytes, &res)
		if res.Code != 0 {
			fmt.Printf("Account [%s]: quota query returned code %d: %s\n\n", name, res.Code, res.Msg)
			continue
		}

		fmt.Printf("=== Quota Snapshot: Account [%s] ===\n", name)
		if len(res.Data.Balances) == 0 {
			fmt.Println("  No active balance buckets found.")
		} else {
			for _, b := range res.Data.Balances {
				fmt.Printf("  • %-16s : %10d / %-10d (%s remaining)\n", b.ShowName, b.RemainingUnits, b.TotalUnits, b.UnitType)
			}
		}
		fmt.Println()
	}
}

func formatNumber(n int64) string {
	in := fmt.Sprintf("%d", n)
	if len(in) <= 3 {
		return in
	}
	var out []byte
	rem := len(in) % 3
	if rem > 0 {
		out = append(out, in[:rem]...)
		if len(in) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}

func runStatus(args []string) {
	cfgPath := getConfigPath(args)
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	store, err := auth.LoadStore()
	if err != nil || store == nil || len(store.Accounts) == 0 {
		impCred, _ := auth.ImportFromZCodeConfig(cfg.Provider)
		if impCred != nil {
			fmt.Println("Not logged in to store, but ~/.zcode/v2/config.json is available.")
			fmt.Println("Run: zcode-proxy auth login zai --import")
			return
		}
		fmt.Println("Not logged in. Run: zcode-proxy auth login <zai|bigmodel>")
		return
	}

	fmt.Printf("zcode-proxy %s (System & Account Pool Dashboard)\n\n", Version)
	fmt.Printf("Configuration:\n")
	fmt.Printf("  • Provider: %s | Plan: %s | Routing: %s | Port: %d\n\n", cfg.Provider, cfg.Plan, cfg.Routing, cfg.Server.Port)

	fmt.Printf("Account Pool (%d accounts, Active: %s):\n", len(store.Accounts), store.Active)

	client := &http.Client{Timeout: 10 * time.Second}
	balanceURL := fmt.Sprintf("%s/api/v1/zcode-plan/billing/balance?app_version=%s&platform=linux-x64", cfg.Claim.Origin, cfg.Identity.AppVersion)

	poolTotalRemaining := make(map[string]int64)
	poolTotalUnits := make(map[string]int64)

	for name, cred := range store.Accounts {
		marker := "  "
		if name == store.Active {
			marker = "* "
		}
		keyDisplay := cred.ApiKey
		if len(keyDisplay) > 12 {
			keyDisplay = keyDisplay[:12] + "..."
		}
		jwtDisplay := "none"
		if cred.Jwt != "" {
			jwtDisplay = cred.Jwt
			if len(jwtDisplay) > 15 {
				jwtDisplay = jwtDisplay[:15] + "..."
			}
		}

		fmt.Printf("%s[%s] (provider: %s)\n", marker, name, cred.Provider)
		if keyDisplay != "" && keyDisplay != "start-plan" {
			fmt.Printf("    API Key:        %s\n", keyDisplay)
		}
		fmt.Printf("    Start-Plan JWT: %s\n", jwtDisplay)

		if cred.Jwt == "" {
			fmt.Println("    • No Start-Plan JWT (quota unavailable)")
			fmt.Println()
			continue
		}

		req, err := http.NewRequest("GET", balanceURL, nil)
		if err != nil {
			fmt.Println()
			continue
		}
		headers := proxy.BuildControlIdentityHeaders(cfg)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Authorization", "Bearer "+cred.Jwt)
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("    • Quota query error: %v\n\n", err)
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var res struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Balances []struct {
					ShowName       string `json:"show_name"`
					TotalUnits     int64  `json:"total_units"`
					RemainingUnits int64  `json:"remaining_units"`
					UnitType       string `json:"unit_type"`
				} `json:"balances"`
			} `json:"data"`
		}

		_ = json.Unmarshal(bodyBytes, &res)
		if res.Code != 0 {
			fmt.Printf("    • Quota error code %d: %s\n", res.Code, res.Msg)
		} else if len(res.Data.Balances) == 0 {
			fmt.Println("    • No active balances")
		} else {
			for _, b := range res.Data.Balances {
				pct := float64(0)
				if b.TotalUnits > 0 {
					pct = (float64(b.RemainingUnits) / float64(b.TotalUnits)) * 100
				}
				fmt.Printf("    • %-16s : %10s / %-10s %s (%.1f%%)\n",
					b.ShowName,
					formatNumber(b.RemainingUnits),
					formatNumber(b.TotalUnits),
					b.UnitType,
					pct,
				)
				poolTotalRemaining[b.ShowName] += b.RemainingUnits
				poolTotalUnits[b.ShowName] += b.TotalUnits
			}
		}
		fmt.Println()
	}

	if len(poolTotalRemaining) > 0 {
		fmt.Println("Pool Total Capacity:")
		for modelName, rem := range poolTotalRemaining {
			tot := poolTotalUnits[modelName]
			pct := float64(0)
			if tot > 0 {
				pct = (float64(rem) / float64(tot)) * 100
			}
			fmt.Printf("  • %-16s : %10s / %-10s tokens (%.1f%% remaining)\n",
				modelName,
				formatNumber(rem),
				formatNumber(tot),
				pct,
			)
		}
		fmt.Println()
	}
}
