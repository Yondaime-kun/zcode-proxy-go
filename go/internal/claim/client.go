package claim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
	"github.com/yondaime-kun/zcode-proxy-go/internal/proxy"
)

type Client struct {
	origin     string
	jwt        string
	cfg        *config.Config
	httpClient *http.Client
}

func NewClient(origin, jwt string, cfg *config.Config) *Client {
	return &Client{
		origin:     origin,
		jwt:        jwt,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func claimPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32-x64"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "darwin-arm64"
		}
		return "darwin-x64"
	default:
		return "linux-x64"
	}
}

func (c *Client) GetPreviews(ctx context.Context) ([]ClaimablePlan, error) {
	appVer := c.cfg.Identity.AppVersion
	if appVer == "" {
		appVer = "3.11.2"
	}
	platform := claimPlatform()

	endpoint := fmt.Sprintf("%s/api/v1/zcode-plan/billing/preview?app_version=%s&platform=%s",
		c.origin, url.QueryEscape(appVer), url.QueryEscape(platform))

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	headers := proxy.BuildControlIdentityHeaders(c.cfg)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	if c.jwt != "" {
		req.Header.Set("Authorization", "Bearer "+c.jwt)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("preview request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("no campaign deployed yet (404)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("preview failed: HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var root struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Plans []ClaimablePlan `json:"plans"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &root); err != nil {
		return nil, fmt.Errorf("failed to parse preview response: %w", err)
	}

	if root.Code != 0 {
		return nil, fmt.Errorf("preview returned code %d: %s", root.Code, root.Msg)
	}

	return root.Data.Plans, nil
}

func (c *Client) Claim(ctx context.Context, planID string, captchaVerifyParam string, captchaRegion string) (*ClaimOutcome, error) {
	if c.jwt == "" {
		return &ClaimOutcome{
			OK:          false,
			PlanID:      planID,
			FailureKind: "login_required",
			Code:        401,
			Message:     "manual_claim_login_required",
		}, nil
	}

	endpoint := fmt.Sprintf("%s/api/v1/zcode-plan/billing/claim", c.origin)
	payload, _ := json.Marshal(map[string]string{"plan_id": planID})

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	headers := proxy.BuildControlIdentityHeaders(c.cfg)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.jwt)

	if captchaVerifyParam != "" {
		req.Header.Set("X-Aliyun-Captcha-Verify-Param", captchaVerifyParam)
	}
	if captchaRegion != "" {
		req.Header.Set("X-Aliyun-Captcha-Verify-Region", captchaRegion)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claim request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var root struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Plan *ClaimablePlan `json:"plan"`
		} `json:"data"`
	}
	_ = json.Unmarshal(bodyBytes, &root)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && root.Code == 0 && root.Data.Plan != nil {
		out := &ClaimOutcome{
			OK:     true,
			PlanID: planID,
		}
		if root.Data.Plan.StartsAt != nil {
			out.StartsAt = root.Data.Plan.StartsAt
		}
		if root.Data.Plan.EndsAt != nil {
			out.EndsAt = root.Data.Plan.EndsAt
		}
		return out, nil
	}

	code := root.Code
	if code == 0 && resp.StatusCode >= 400 {
		code = resp.StatusCode
	}
	msg := root.Msg
	if msg == "" {
		msg = string(bodyBytes)
	}

	out := &ClaimOutcome{
		OK:          false,
		PlanID:      planID,
		FailureKind: ClassifyClaimCode(code),
		Code:        code,
		Message:     msg,
	}
	if root.Data.Plan != nil && root.Data.Plan.EndsAt != nil {
		out.FailureEndsAt = root.Data.Plan.EndsAt
	}

	return out, nil
}

type BalanceBucket struct {
	ShowName       string `json:"show_name"`
	TotalUnits     int64  `json:"total_units"`
	UsedUnits      int64  `json:"used_units"`
	RemainingUnits int64  `json:"remaining_units"`
	UnitType       string `json:"unit_type"`
}

func (c *Client) GetBalances(ctx context.Context) ([]BalanceBucket, error) {
	appVer := c.cfg.Identity.AppVersion
	if appVer == "" {
		appVer = "3.11.2"
	}
	platform := claimPlatform()
	endpoint := fmt.Sprintf("%s/api/v1/zcode-plan/billing/balance?app_version=%s&platform=%s",
		c.origin, url.QueryEscape(appVer), url.QueryEscape(platform))

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	headers := proxy.BuildControlIdentityHeaders(c.cfg)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+c.jwt)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Balances []BalanceBucket `json:"balances"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("balance query code %d: %s", res.Code, res.Msg)
	}
	return res.Data.Balances, nil
}

func FormatBalance(units int64) string {
	if units >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(units)/1_000_000)
	}
	if units >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(units)/1_000)
	}
	return fmt.Sprintf("%d", units)
}

func FormatQuotaSummary(balances []BalanceBucket) string {
	if len(balances) == 0 {
		return "no balance buckets"
	}
	var parts []string
	for _, b := range balances {
		pct := 0.0
		if b.TotalUnits > 0 {
			pct = (float64(b.RemainingUnits) / float64(b.TotalUnits)) * 100
		}
		parts = append(parts, fmt.Sprintf("%s: %s (%.0f%%)", b.ShowName, FormatBalance(b.RemainingUnits), pct))
	}
	return strings.Join(parts, " · ")
}
