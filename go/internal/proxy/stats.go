package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ModelStats struct {
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type ClientStats struct {
	IP       string `json:"ip"`
	Requests int64  `json:"requests"`
}

type StatsSnapshot struct {
	TotalRequests   int64
	SuccessRequests int64
	ErrorRequests   int64
	ActiveRequests  int64
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	AvgLatency      time.Duration
	Models          []ModelStats
	Clients         []ClientStats
}

type PersistedStats struct {
	TotalRequests   int64                 `json:"total_requests"`
	SuccessRequests int64                 `json:"success_requests"`
	ErrorRequests   int64                 `json:"error_requests"`
	InputTokens     int64                 `json:"input_tokens"`
	OutputTokens    int64                 `json:"output_tokens"`
	TotalTokens     int64                 `json:"total_tokens"`
	TotalDurationMs int64                 `json:"total_duration_ms"`
	Models          map[string]ModelStats `json:"models"`
	Clients         map[string]int64      `json:"clients"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type StatsTracker struct {
	mu            sync.RWMutex
	filePath      string
	totalRequests int64
	successReqs   int64
	errorReqs     int64
	activeReqs    int64
	inputTokens   int64
	outputTokens  int64
	totalTokens   int64
	totalDuration time.Duration
	models        map[string]*ModelStats
	clients       map[string]int64
	onUpdate      func()
}

func DefaultStatsPath() string {
	if p := os.Getenv("ZCODE_STATS_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "stats.json"
	}
	return filepath.Join(home, ".zcode-proxy", "stats.json")
}

func NewStatsTracker(onUpdate func()) *StatsTracker {
	return NewStatsTrackerWithPersistence(DefaultStatsPath(), onUpdate)
}

func NewStatsTrackerWithPersistence(filePath string, onUpdate func()) *StatsTracker {
	t := &StatsTracker{
		filePath: filePath,
		models:   make(map[string]*ModelStats),
		clients:  make(map[string]int64),
		onUpdate: onUpdate,
	}
	_ = t.Load()
	return t
}

func (s *StatsTracker) RecordRequestStart(model string, clientIP string) func(statusCode int, inTokens, outTokens int64) {
	if model == "" {
		model = "unknown"
	}
	if clientIP == "" {
		clientIP = "unknown"
	}

	s.mu.Lock()
	s.totalRequests++
	s.activeReqs++
	if s.clients == nil {
		s.clients = make(map[string]int64)
	}
	s.clients[clientIP]++
	if s.onUpdate != nil {
		go s.onUpdate()
	}
	s.mu.Unlock()

	start := time.Now()

	return func(statusCode int, inTokens, outTokens int64) {
		duration := time.Since(start)

		s.mu.Lock()
		defer s.mu.Unlock()

		if s.activeReqs > 0 {
			s.activeReqs--
		}

		if statusCode >= 200 && statusCode < 400 {
			s.successReqs++
		} else {
			s.errorReqs++
		}

		s.inputTokens += inTokens
		s.outputTokens += outTokens
		s.totalTokens += (inTokens + outTokens)
		s.totalDuration += duration

		m, exists := s.models[model]
		if !exists {
			m = &ModelStats{Model: model}
			s.models[model] = m
		}
		m.Requests++
		m.InputTokens += inTokens
		m.OutputTokens += outTokens
		m.TotalTokens += (inTokens + outTokens)

		go s.Save()

		if s.onUpdate != nil {
			go s.onUpdate()
		}
	}
}

func (s *StatsTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalRequests = 0
	s.successReqs = 0
	s.errorReqs = 0
	s.activeReqs = 0
	s.inputTokens = 0
	s.outputTokens = 0
	s.totalTokens = 0
	s.totalDuration = 0
	s.models = make(map[string]*ModelStats)
	s.clients = make(map[string]int64)

	go s.Save()

	if s.onUpdate != nil {
		go s.onUpdate()
	}
}

func (s *StatsTracker) Snapshot() StatsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var avgLatency time.Duration
	if s.totalRequests > 0 {
		avgLatency = time.Duration(int64(s.totalDuration) / s.totalRequests)
	}

	var modelList []ModelStats
	for _, m := range s.models {
		modelList = append(modelList, *m)
	}

	sort.Slice(modelList, func(i, j int) bool {
		if modelList[i].TotalTokens != modelList[j].TotalTokens {
			return modelList[i].TotalTokens > modelList[j].TotalTokens
		}
		return modelList[i].Requests > modelList[j].Requests
	})

	var clientList []ClientStats
	for ip, count := range s.clients {
		clientList = append(clientList, ClientStats{
			IP:       ip,
			Requests: count,
		})
	}
	sort.Slice(clientList, func(i, j int) bool {
		return clientList[i].Requests > clientList[j].Requests
	})

	return StatsSnapshot{
		TotalRequests:   s.totalRequests,
		SuccessRequests: s.successReqs,
		ErrorRequests:   s.errorReqs,
		ActiveRequests:  s.activeReqs,
		InputTokens:     s.inputTokens,
		OutputTokens:    s.outputTokens,
		TotalTokens:     s.totalTokens,
		AvgLatency:      avgLatency,
		Models:          modelList,
		Clients:         clientList,
	}
}

func (s *StatsTracker) Save() error {
	if s.filePath == "" {
		return nil
	}
	s.mu.RLock()
	data := PersistedStats{
		TotalRequests:   s.totalRequests,
		SuccessRequests: s.successReqs,
		ErrorRequests:   s.errorReqs,
		InputTokens:     s.inputTokens,
		OutputTokens:    s.outputTokens,
		TotalTokens:     s.totalTokens,
		TotalDurationMs: s.totalDuration.Milliseconds(),
		Models:          make(map[string]ModelStats),
		Clients:         make(map[string]int64),
		UpdatedAt:       time.Now(),
	}
	for k, v := range s.models {
		data.Models[k] = *v
	}
	for k, v := range s.clients {
		data.Clients[k] = v
	}
	s.mu.RUnlock()

	_ = os.MkdirAll(filepath.Dir(s.filePath), 0755)
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmpFile := fmt.Sprintf("%s.tmp.%d", s.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, s.filePath)
}

func (s *StatsTracker) Load() error {
	if s.filePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var data PersistedStats
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalRequests = data.TotalRequests
	s.successReqs = data.SuccessRequests
	s.errorReqs = data.ErrorRequests
	s.inputTokens = data.InputTokens
	s.outputTokens = data.OutputTokens
	s.totalTokens = data.TotalTokens
	s.totalDuration = time.Duration(data.TotalDurationMs) * time.Millisecond
	s.models = make(map[string]*ModelStats)
	for k, v := range data.Models {
		m := v
		s.models[k] = &m
	}
	s.clients = make(map[string]int64)
	for k, v := range data.Clients {
		s.clients[k] = v
	}
	return nil
}

// FormatTokens converts an integer count to human-readable format (e.g. 850, 12.5k, 1.25M).
func FormatTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		v := float64(n) / 1000.0
		if v < 10.0 {
			return fmt.Sprintf("%.2fk", v)
		}
		return fmt.Sprintf("%.1fk", v)
	}
	v := float64(n) / 1_000_000.0
	formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
	return formatted + "M"
}

// FormatLatency formats a duration nicely (e.g. 450ms, 1.2s).
func FormatLatency(d time.Duration) string {
	if d <= 0 {
		return "0ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
