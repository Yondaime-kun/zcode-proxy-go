package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/yondaime-kun/zcode-proxy-go/internal/config"
)

const (
	AnthropicSdkUa = "ai-sdk/anthropic/3.0.81"
	AnthropicVersion = "2023-06-01"
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomUUID() string {
	var u [16]byte
	_, _ = rand.Read(u[:])
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

func normalizeOsCategory(platform string) string {
	switch platform {
	case "darwin":
		return "macos"
	case "windows", "win32":
		return "windows"
	default:
		return "linux"
	}
}

func resolvePlatform() string {
	if p := os.Getenv("ZCODE_IDENTITY_PLATFORM"); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

func resolveArch() string {
	if a := os.Getenv("ZCODE_IDENTITY_ARCH"); a != "" {
		return a
	}
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func resolveOsRelease() string {
	if r := os.Getenv("ZCODE_IDENTITY_RELEASE"); r != "" {
		return r
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		return strings.TrimSpace(string(b))
	}
	return "6.8.0-49-generic"
}

func resolveLanguage() string {
	if l := os.Getenv("ZCODE_IDENTITY_CLIENT_LANGUAGE"); l != "" {
		return l
	}
	if lang := os.Getenv("LANG"); lang != "" {
		parts := strings.Split(lang, ".")
		return strings.ReplaceAll(parts[0], "_", "-")
	}
	return "en-US"
}

func resolveTimezone() string {
	if tz := os.Getenv("ZCODE_IDENTITY_CLIENT_TIMEZONE"); tz != "" {
		return tz
	}
	if tz, _ := time.Now().Zone(); tz != "" {
		return tz
	}
	return "Asia/Shanghai"
}

func BuildLlmIdentityHeaders(cfg *config.Config) map[string]string {
	appVer := cfg.Identity.AppVersion
	if appVer == "" {
		appVer = "3.14.0"
	}
	platform := resolvePlatform()
	arch := resolveArch()
	release := resolveOsRelease()
	sourceTitle := cfg.Identity.SourceTitle
	if sourceTitle == "" {
		sourceTitle = "cli"
	}
	referer := cfg.Identity.RefererOrigin
	if referer == "" {
		referer = "https://zcode.z.ai"
	}

	headers := map[string]string{
		"HTTP-Referer":        referer,
		"User-Agent":          fmt.Sprintf("ZCode/%s %s", appVer, AnthropicSdkUa),
		"X-ZCode-App-Version": appVer,
		"X-Title":             fmt.Sprintf("Z Code@%s", sourceTitle),
		"X-Release-Channel":   "production",
		"X-Client-Language":   resolveLanguage(),
		"X-Client-Timezone":   resolveTimezone(),
		"X-ZCode-Agent":       "glm",
		"X-Platform":          fmt.Sprintf("%s-%s", platform, arch),
		"X-Os-Category":       normalizeOsCategory(platform),
		"X-Os-Version":        release,
	}

	return headers
}

func BuildControlIdentityHeaders(cfg *config.Config) map[string]string {
	appVer := cfg.Identity.AppVersion
	if appVer == "" {
		appVer = "3.14.0"
	}
	platform := resolvePlatform()
	arch := resolveArch()
	release := resolveOsRelease()
	sourceTitle := cfg.Identity.SourceTitle
	if sourceTitle == "" {
		sourceTitle = "cli"
	}
	referer := cfg.Identity.RefererOrigin
	if referer == "" {
		referer = "https://zcode.z.ai"
	}

	headers := map[string]string{
		"User-Agent":          fmt.Sprintf("ZCode/%s", appVer),
		"HTTP-Referer":        referer,
		"X-Title":             fmt.Sprintf("Z Code@%s", sourceTitle),
		"X-ZCode-App-Version": appVer,
		"X-Platform":          fmt.Sprintf("%s-%s", platform, arch),
		"X-Release-Channel":   "production",
		"X-Client-Language":   resolveLanguage(),
		"X-Client-Timezone":   resolveTimezone(),
		"X-Os-Category":       normalizeOsCategory(platform),
		"X-Os-Version":        release,
	}

	if cfg.Identity.DeviceMid != "" {
		headers["X-Device-Mid"] = cfg.Identity.DeviceMid
	}

	return headers
}

func BuildTraceHeaders(plan string) map[string]string {
	headers := map[string]string{
		"x-request-id":         randomUUID(),
		"x-zcode-session-type": "main",
		"x-zcode-trace-id":     randomUUID(),
	}
	if plan != "start-plan" {
		headers["x-query-id"] = randomUUID()
		headers["x-session-id"] = randomUUID()
	}
	return headers
}

func BuildAnthropicMetadataUserId(deviceMid string, sessionID string) string {
	type payload struct {
		DeviceID    string `json:"device_id,omitempty"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	p := payload{
		DeviceID:    deviceMid,
		AccountUUID: "",
		SessionID:   sessionID,
	}
	b, _ := json.Marshal(p)
	return string(b)
}
