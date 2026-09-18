package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CaptchaConfig struct {
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix"`
	Region  string `json:"region"`
	SceneID string `json:"sceneId"`
}

type CaptchaToken struct {
	VerifyParam string
	Region      string
}

func FetchCaptchaConfig(ctx context.Context, appVersion string) (*CaptchaConfig, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	u := fmt.Sprintf("https://zcode.z.ai/api/v1/client/configs?app_version=%s&platform=win32-x64", url.QueryEscape(appVersion))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var root struct {
		Data struct {
			Configs struct {
				Captcha *CaptchaConfig `json:"captcha"`
			} `json:"configs"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		return nil, err
	}

	cfg := root.Data.Configs.Captcha
	if cfg == nil || !cfg.Enabled {
		return nil, fmt.Errorf("captcha not enabled or unavailable")
	}
	return cfg, nil
}

func extractCaptchaToken(output []byte) *CaptchaToken {
	str := string(output)
	idx := strings.Index(str, `{"verifyParam"`)
	if idx == -1 {
		idx = strings.Index(str, `{"verify_param"`)
	}
	if idx != -1 {
		sub := str[idx:]
		lastIdx := strings.LastIndex(sub, "}")
		if lastIdx != -1 {
			sub = sub[:lastIdx+1]
		}
		var tok struct {
			VerifyParam string `json:"verifyParam"`
			Region      string `json:"region"`
		}
		if err := json.Unmarshal([]byte(sub), &tok); err == nil && tok.VerifyParam != "" {
			return &CaptchaToken{
				VerifyParam: tok.VerifyParam,
				Region:      tok.Region,
			}
		}
	}
	return nil
}

var (
	cachedBinaryMu   sync.RWMutex
	cachedBinaryPath string
)

// SolveCaptchaOnDemand attempts to solve captcha if a solver command, binary, or script is available.
func SolveCaptchaOnDemand(ctx context.Context, appVersion string) (*CaptchaToken, error) {
	// Ensure ctx has a safety deadline (max 25 seconds) so a stuck child process never hangs forever
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
	}

	log.Printf("[captcha] solving captcha on-demand (app_version=%s)...", appVersion)

	// 1. Check custom solver command from env (explicit user/test override takes top precedence)
	if solverCmd := os.Getenv("ZCODE_CAPTCHA_SOLVER_CMD"); solverCmd != "" {
		parts := strings.Fields(solverCmd)
		cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], appVersion)...)
		out, err := cmd.Output()
		if err == nil {
			if tok := extractCaptchaToken(out); tok != nil {
				log.Printf("[captcha] custom solver succeeded (len=%d, region=%s)", len(tok.VerifyParam), tok.Region)
				return tok, nil
			}
		}
	}

	// 2. Fast path: try cached standalone binary path
	cachedBinaryMu.RLock()
	cachedPath := cachedBinaryPath
	cachedBinaryMu.RUnlock()

	if cachedPath != "" {
		cmd := exec.CommandContext(ctx, cachedPath, appVersion)
		out, err := cmd.Output()
		if err == nil {
			if tok := extractCaptchaToken(out); tok != nil {
				log.Printf("[captcha] cached binary solver (%s) succeeded (len=%d, region=%s)", cachedPath, len(tok.VerifyParam), tok.Region)
				return tok, nil
			}
		}
		// Invalidate cache if binary failed or disappeared
		cachedBinaryMu.Lock()
		cachedBinaryPath = ""
		cachedBinaryMu.Unlock()
	}

	// 3. Search standalone zcode-captcha-solver binary (next to executable, in cwd, or PATH)
	var solverBinaries []string
	if exe, err := os.Executable(); err == nil {
		solverBinaries = append(solverBinaries, filepath.Join(filepath.Dir(exe), "zcode-captcha-solver"))
	}
	solverBinaries = append(solverBinaries, "./zcode-captcha-solver", "zcode-captcha-solver")

	seenPaths := make(map[string]bool)
	for _, binPath := range solverBinaries {
		clean := filepath.Clean(binPath)
		if seenPaths[clean] {
			continue
		}
		seenPaths[clean] = true

		if _, err := os.Stat(binPath); err == nil || binPath == "zcode-captcha-solver" {
			cmd := exec.CommandContext(ctx, binPath, appVersion)
			out, err := cmd.Output()
			if err == nil {
				if tok := extractCaptchaToken(out); tok != nil {
					log.Printf("[captcha] binary solver (%s) succeeded (len=%d, region=%s)", binPath, len(tok.VerifyParam), tok.Region)
					cachedBinaryMu.Lock()
					cachedBinaryPath = binPath
					cachedBinaryMu.Unlock()
					return tok, nil
				}
			}
		}
	}

	// 3. Fallback: Check scripts/solve-captcha.ts with bun
	home, _ := os.UserHomeDir()
	bunPaths := []string{
		"bun",
		fmt.Sprintf("%s/.bun/bin/bun", home),
	}

	for _, bun := range bunPaths {
		scriptCandidates := []string{
			"scripts/solve-captcha.ts",
			"../scripts/solve-captcha.ts",
		}
		for _, sPath := range scriptCandidates {
			if _, err := os.Stat(sPath); err == nil {
				cmd := exec.CommandContext(ctx, bun, "run", sPath, appVersion)
				out, err := cmd.Output()
				if err == nil {
					if tok := extractCaptchaToken(out); tok != nil {
						log.Printf("[captcha] bun solver (%s) succeeded (len=%d, region=%s)", sPath, len(tok.VerifyParam), tok.Region)
						return tok, nil
					}
				} else {
					log.Printf("[captcha] solve-captcha.ts execution error: %v", err)
				}
			}
		}
	}

	log.Printf("[captcha] WARNING: no captcha token generated by any solver")
	return nil, fmt.Errorf("no captcha solver available or solver returned empty token")
}

// IsCaptchaChallenged checks if upstream response indicates an Aliyun captcha challenge or verification failure.
// Triggers on:
//  1. Response header: x-aliyun-captcha-verify-param present on non-2xx
//  2. HTTP 400 with in-body {"code":3007,...} (in-body challenge)
//  3. Non-2xx response body with code 3007 or message mentioning captcha verification failure
func IsCaptchaChallenged(statusCode int, header http.Header, body []byte) bool {
	if statusCode >= 200 && statusCode < 300 {
		return false
	}
	if header != nil && strings.TrimSpace(header.Get("x-aliyun-captcha-verify-param")) != "" {
		return true
	}
	if len(body) > 0 {
		bodyStr := string(body)
		if strings.Contains(bodyStr, `"code":3007`) || strings.Contains(bodyStr, `"code": 3007`) {
			return true
		}
		var parsed struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			if parsed.Code == 3007 {
				return true
			}
			lowerMsg := strings.ToLower(parsed.Msg)
			if strings.Contains(lowerMsg, "captcha verify failed") ||
				(strings.Contains(lowerMsg, "captcha") && strings.Contains(lowerMsg, "fail")) {
				return true
			}
		}
	}
	return false
}
