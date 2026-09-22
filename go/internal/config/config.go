package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type AuthConfig struct {
	ProxyApiKey string `yaml:"proxyApiKey"`
}

type ProviderUrls struct {
	AnthropicBase string `yaml:"anthropicBase"`
	OpenAIBase    string `yaml:"openaiBase"`
}

type IdentityConfig struct {
	AppVersion    string `yaml:"appVersion"`
	SourceTitle   string `yaml:"sourceTitle"`
	RefererOrigin string `yaml:"refererOrigin"`
	DeviceMid     string `yaml:"deviceMid"`
}

type ClientIdentityConfig struct {
	Mode        string `yaml:"mode"`
	TTLSeconds  int    `yaml:"ttlSeconds"`
	MaxSessions int    `yaml:"maxSessions"`
}

type ResponsesStoreConfig struct {
	MaxEntries int   `yaml:"maxEntries"`
	TTLMs      int64 `yaml:"ttlMs"`
}

type ResponsesConfig struct {
	Enabled bool                 `yaml:"enabled"`
	Store   ResponsesStoreConfig `yaml:"store"`
}

type ClaimConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Auto           bool   `yaml:"auto"`
	Origin         string `yaml:"origin"`
	PollIntervalMs int    `yaml:"pollIntervalMs"`
	CooldownMs     int    `yaml:"cooldownMs"`
	PlanId         string `yaml:"planId"`
}

type CaptchaSettings struct {
	MaxRetries int `yaml:"maxRetries"`
}

type SecurityConfig struct {
	Whitelist []string `yaml:"whitelist"`
	Blacklist []string `yaml:"blacklist"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type Config struct {
	Server         ServerConfig            `yaml:"server"`
	Auth           AuthConfig              `yaml:"auth"`
	Provider       string                  `yaml:"provider"`
	Plan           string                  `yaml:"plan"`
	Routing        string                  `yaml:"routing"` // "failover" (default) or "round-robin"
	Providers      map[string]ProviderUrls `yaml:"providers"`
	DefaultModel   string                  `yaml:"defaultModel"`
	Models         []string                `yaml:"models"`
	Identity       IdentityConfig          `yaml:"identity"`
	ClientIdentity ClientIdentityConfig    `yaml:"clientIdentity"`
	Responses      ResponsesConfig         `yaml:"responses"`
	Claim          ClaimConfig             `yaml:"claim"`
	Logging        LoggingConfig           `yaml:"logging"`
	Captcha        CaptchaSettings         `yaml:"captcha"`
	Security       SecurityConfig          `yaml:"security"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 8080,
			Host: "0.0.0.0",
		},
		Auth: AuthConfig{
			ProxyApiKey: "",
		},
		Provider: "zai",
		Plan:     "coding-plan",
		Routing:  "failover",
		Captcha: CaptchaSettings{
			MaxRetries: 3,
		},
		Providers: map[string]ProviderUrls{
			"zai": {
				AnthropicBase: "https://api.z.ai/api/anthropic",
				OpenAIBase:    "https://api.z.ai/api/coding/paas/v4",
			},
			"bigmodel": {
				AnthropicBase: "https://open.bigmodel.cn/api/anthropic",
				OpenAIBase:    "https://open.bigmodel.cn/api/coding/paas/v4",
			},
		},
		DefaultModel: "glm-4.6",
		Models: []string{
			"glm-4.5-air",
			"glm-4.6",
			"glm-4.6v",
			"glm-4.7",
			"glm-5",
			"glm-5-turbo",
			"glm-5v-turbo",
			"glm-5.1",
			"glm-5.2",
			"glm-5.3",
			"glm-5.3-flash",
		},
		Identity: IdentityConfig{
			AppVersion:    "3.14.0",
			SourceTitle:   "cli",
			RefererOrigin: "https://zcode.z.ai",
			DeviceMid:     "",
		},
		ClientIdentity: ClientIdentityConfig{
			Mode:        "observe",
			TTLSeconds:  900,
			MaxSessions: 1024,
		},
		Responses: ResponsesConfig{
			Enabled: true,
			Store: ResponsesStoreConfig{
				MaxEntries: 1000,
				TTLMs:      86400000,
			},
		},
		Claim: ClaimConfig{
			Enabled:        true,
			Auto:           true,
			Origin:         "https://zcode.z.ai",
			PollIntervalMs: 300000,
			CooldownMs:     60000,
			PlanId:         "",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

func GenerateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse YAML config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Environment variable overrides
	if portStr := os.Getenv("ZCODE_PROXY_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			cfg.Server.Port = p
		}
	}
	if host := os.Getenv("ZCODE_PROXY_HOST"); host != "" {
		cfg.Server.Host = host
	}
	if apiKey := os.Getenv("ZCODE_PROXY_API_KEY"); apiKey != "" {
		cfg.Auth.ProxyApiKey = apiKey
	}
	if prov := os.Getenv("ZCODE_PROVIDER"); prov != "" {
		cfg.Provider = prov
	}
	if plan := os.Getenv("ZCODE_PLAN"); plan != "" {
		cfg.Plan = plan
	}
	if routing := os.Getenv("ZCODE_ROUTING"); routing != "" {
		cfg.Routing = routing
	}
	if appVer := os.Getenv("ZCODE_APP_VERSION"); appVer != "" {
		cfg.Identity.AppVersion = appVer
	}
	if devMid := os.Getenv("ZCODE_IDENTITY_DEVICE_MID"); devMid != "" {
		cfg.Identity.DeviceMid = devMid
	}
	if envRetries := os.Getenv("ZCODE_CAPTCHA_MAX_RETRIES"); envRetries != "" {
		if r, err := strconv.Atoi(envRetries); err == nil && r >= 0 {
			cfg.Captcha.MaxRetries = r
		}
	}
	if cfg.Captcha.MaxRetries <= 0 {
		cfg.Captcha.MaxRetries = 3
	}
	if envWhite := os.Getenv("ZCODE_IP_WHITELIST"); envWhite != "" {
		for _, ip := range strings.Split(envWhite, ",") {
			if trimmed := strings.TrimSpace(ip); trimmed != "" {
				cfg.Security.Whitelist = append(cfg.Security.Whitelist, trimmed)
			}
		}
	}
	if envBlack := os.Getenv("ZCODE_IP_BLACKLIST"); envBlack != "" {
		for _, ip := range strings.Split(envBlack, ",") {
			if trimmed := strings.TrimSpace(ip); trimmed != "" {
				cfg.Security.Blacklist = append(cfg.Security.Blacklist, trimmed)
			}
		}
	}

	// Ensure DeviceMid is populated
	if cfg.Identity.DeviceMid == "" {
		cfg.Identity.DeviceMid = GenerateUUID()
		// Try writing back to config file if it exists
		if data != nil {
			_ = saveUpdatedDeviceMid(path, cfg.Identity.DeviceMid)
		}
	}

	return cfg, nil
}

func saveUpdatedDeviceMid(path string, mid string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(raw)
	if strings.Contains(content, "deviceMid:") {
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "deviceMid:") {
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = fmt.Sprintf(`%sdeviceMid: "%s"`, indent, mid)
				break
			}
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
	}
	return nil
}

func EnsureConfigFile(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return false
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	def := DefaultConfig()
	def.Identity.DeviceMid = GenerateUUID()
	out, err := yaml.Marshal(def)
	if err == nil {
		_ = os.WriteFile(path, out, 0644)
		return true
	}
	return false
}

// UpdateConfigYaml updates top-level scalar keys (e.g. provider, plan) in config.yaml preserving line structure.
func UpdateConfigYaml(path string, updates map[string]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(raw)
	lines := strings.Split(content, "\n")
	for key, val := range updates {
		found := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, key+":") {
				lines[i] = fmt.Sprintf(`%s: "%s"`, key, val)
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, fmt.Sprintf(`%s: "%s"`, key, val))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}
