package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ── Data directory (set by main) ──────────────────────────────────────────────

var DataDir = "."

// ── JSON helpers ──────────────────────────────────────────────────────────────

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func readJSONFile(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(DataDir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSONFile(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(DataDir, name), data, 0644)
}

// ── Results directory ─────────────────────────────────────────────────────────

func resultsDir() string {
	return filepath.Join(DataDir, "results")
}

func ensureResultsDir() {
	os.MkdirAll(resultsDir(), 0755)
}

func writeResultFile(filename string, v any) error {
	ensureResultsDir()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(resultsDir(), filename), data, 0644)
}

// ── Score config ──────────────────────────────────────────────────────────────

var scoreConfigMu sync.Mutex
var scoreConfigCache *ScoreConfig

func loadScoreWeights() ScoreWeights {
	scoreConfigMu.Lock()
	defer scoreConfigMu.Unlock()
	if scoreConfigCache != nil {
		return scoreConfigCache.Weights
	}
	var cfg ScoreConfig
	if err := readJSONFile("score-config.json", &cfg); err == nil {
		scoreConfigCache = &cfg
		return cfg.Weights
	}
	scoreConfigCache = &ScoreConfig{Weights: DefaultScoreWeights}
	return DefaultScoreWeights
}

func saveScoreConfig(cfg ScoreConfig) {
	scoreConfigMu.Lock()
	defer scoreConfigMu.Unlock()
	scoreConfigCache = &cfg
	writeJSONFile("score-config.json", cfg)
}

// ── Analytics config ──────────────────────────────────────────────────────────

func loadAnalyticsConfig() AnalyticsConfig {
	var cfg AnalyticsConfig
	if err := readJSONFile("analytics.json", &cfg); err != nil {
		return AnalyticsConfig{OptIn: true}
	}
	return cfg
}

func saveAnalyticsConfig(cfg AnalyticsConfig) {
	writeJSONFile("analytics.json", cfg)
}

func getOrCreateClientID() string {
	cfg := loadAnalyticsConfig()
	if cfg.ClientID != "" {
		return cfg.ClientID
	}
	cfg.ClientID = generateID()
	saveAnalyticsConfig(cfg)
	return cfg.ClientID
}

// ── Notify config ─────────────────────────────────────────────────────────────

func loadNotifyConfig() NotifyConfig {
	// Default: all notification types enabled.
	// readJSONFile only overrides fields present in the JSON, so pre-existing
	// files without the new enable_* keys keep the default "true" values.
	cfg := NotifyConfig{
		EnablePXChanges:      true,
		EnableJobComplete:    true,
		EnableProviderIssues: true,
		EnableSystemAlerts:   true,
		EnableDropTest:       true,
		EnableDropPX:         true,
	}
	readJSONFile("notify.json", &cfg) // silently ignore file-not-found
	return cfg
}

func saveNotifyConfig(cfg NotifyConfig) {
	writeJSONFile("notify.json", cfg)
}

// ── Calibration data ──────────────────────────────────────────────────────────

type CalibrationEntry struct {
	Date               string  `json:"date"`
	AvgBytesPerProxy   int     `json:"avg_bytes_per_proxy"`
	ProxyCount         int     `json:"proxy_count"`
}

func loadCalibrationData() []CalibrationEntry {
	var data []CalibrationEntry
	readJSONFile("calibration.json", &data)
	return data
}

func loadIntegrations() []Integration {
	var data []Integration
	readJSONFile("integrations.json", &data)
	if data == nil {
		data = []Integration{}
	}
	return data
}

func saveIntegrations(data []Integration) {
	writeJSONFile("integrations.json", data)
}

// ── Providers ─────────────────────────────────────────────────────────────────

func loadProviders() map[string]*Provider {
	var data map[string]*Provider
	readJSONFile("providers.json", &data)
	if data == nil {
		data = map[string]*Provider{}
	}
	return data
}

func saveProviders(data map[string]*Provider) {
	writeJSONFile("providers.json", data)
}

// ── Saved proxy lists ─────────────────────────────────────────────────────────

// Lists are stored as a map keyed by list ID (mirrors the frontend's proxyLists shape).
func loadProxyLists() map[string]ProxyList {
	var data map[string]ProxyList
	readJSONFile("proxy_lists.json", &data)
	if data == nil {
		data = map[string]ProxyList{}
	}
	return data
}

func saveProxyLists(data map[string]ProxyList) {
	writeJSONFile("proxy_lists.json", data)
}

func appendCalibrationData(entry CalibrationEntry) {
	data := loadCalibrationData()
	data = append(data, entry)
	if len(data) > 20 {
		data = data[len(data)-20:]
	}
	writeJSONFile("calibration.json", data)
}
