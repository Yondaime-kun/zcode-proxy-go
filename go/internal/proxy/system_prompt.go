package proxy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

//go:embed zcode_system.json
var zcodeSystemJSON []byte

type zcodeSystemData struct {
	CliPrefix      string   `json:"cliPrefix"`
	StableSections []string `json:"stableSections"`
	DynamicSections struct {
		BeforeEnvironment string `json:"beforeEnvironment"`
		AfterEnvironment  string `json:"afterEnvironment"`
	} `json:"dynamicSections"`
	Environment struct {
		Heading        string `json:"heading"`
		InvokedLine    string `json:"invokedLine"`
		CwdLabel       string `json:"cwdLabel"`
		GitLabel       string `json:"gitLabel"`
		GitNo          string `json:"gitNo"`
		PlatformLabel  string `json:"platformLabel"`
		ShellLabel     string `json:"shellLabel"`
		OsVersionLabel string `json:"osVersionLabel"`
		PoweredByLine  string `json:"poweredByLine"`
	} `json:"environment"`
	ContextPrefix struct {
		Intro              string `json:"intro"`
		Outro              string `json:"outro"`
		CurrentDateHeading string `json:"currentDateHeading"`
		CurrentDateLine    string `json:"currentDateLine"`
	} `json:"contextPrefix"`
	SystemReminder struct {
		Open  string `json:"open"`
		Close string `json:"close"`
	} `json:"systemReminder"`
}

var sysData zcodeSystemData

func init() {
	_ = json.Unmarshal(zcodeSystemJSON, &sysData)
}

type StartPlanSystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

func BuildEnvironmentSection(model string) string {
	e := sysData.Environment
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = "/workspace"
	}
	if envCwd := os.Getenv("ZCODE_IDENTITY_ENV_CWD"); envCwd != "" {
		cwd = envCwd
	}

	platform := resolvePlatform()
	shellRaw := os.Getenv("SHELL")
	if shellRaw == "" {
		shellRaw = os.Getenv("COMSPEC")
	}
	shell := "bash"
	if shellRaw != "" {
		parts := strings.Split(shellRaw, "/")
		if len(parts) > 0 {
			shell = parts[len(parts)-1]
		}
	}

	osVersionParts := []string{platform, resolveOsRelease(), resolveArch()}
	var filtered []string
	for _, p := range osVersionParts {
		if strings.TrimSpace(p) != "" {
			filtered = append(filtered, strings.TrimSpace(p))
		}
	}
	osVersion := strings.Join(filtered, " ")

	lines := []string{
		e.Heading,
		e.InvokedLine,
		fmt.Sprintf("- %s: %s", e.CwdLabel, cwd),
		fmt.Sprintf("- %s: %s", e.GitLabel, e.GitNo),
		fmt.Sprintf("- %s: %s", e.PlatformLabel, platform),
		fmt.Sprintf("- %s: %s", e.ShellLabel, shell),
		fmt.Sprintf("- %s: %s", e.OsVersionLabel, osVersion),
	}
	if model != "" {
		lines = append(lines, strings.ReplaceAll(e.PoweredByLine, "{model}", model))
	}
	return strings.Join(lines, "\n")
}

func BuildStartPlanSystem(model string, existingSystem interface{}) []StartPlanSystemBlock {
	stable := strings.Join(sysData.StableSections, "\n\n")
	dynamic := strings.Join([]string{
		sysData.DynamicSections.BeforeEnvironment,
		BuildEnvironmentSection(model),
		sysData.DynamicSections.AfterEnvironment,
	}, "\n\n")

	eph := &CacheControl{Type: "ephemeral"}
	official := []StartPlanSystemBlock{
		{Type: "text", Text: sysData.CliPrefix, CacheControl: eph},
		{Type: "text", Text: stable, CacheControl: eph},
		{Type: "text", Text: fmt.Sprintf("\n\n%s", dynamic), CacheControl: eph},
	}

	if existingSystem != nil {
		switch s := existingSystem.(type) {
		case string:
			if strings.TrimSpace(s) != "" {
				official = append(official, StartPlanSystemBlock{Type: "text", Text: s})
			}
		case []interface{}:
			for _, item := range s {
				if m, ok := item.(map[string]interface{}); ok {
					if txt, ok := m["text"].(string); ok && strings.TrimSpace(txt) != "" {
						official = append(official, StartPlanSystemBlock{Type: "text", Text: txt})
					}
				}
			}
		}
	}

	return official
}

func BuildContextPrefixMessage() map[string]interface{} {
	dateStr := time.Now().Format("2006-01-02")
	cp := sysData.ContextPrefix
	dateSection := fmt.Sprintf("%s\n%s", cp.CurrentDateHeading, strings.ReplaceAll(cp.CurrentDateLine, "{date}", dateStr))
	body := strings.Join([]string{cp.Intro, dateSection, "", cp.Outro}, "\n")

	return map[string]interface{}{
		"role": "user",
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("%s%s%s", sysData.SystemReminder.Open, body, sysData.SystemReminder.Close),
			},
		},
	}
}
