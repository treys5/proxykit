package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const CloudBackendURL = "https://proxykit-backend.proxykit.workers.dev"

// ── Cloud auth state ──────────────────────────────────────────────────────────

type CloudAuth struct {
	Token        string `json:"token"`
	UserID       string `json:"user_id"`
	LicenseHint  string `json:"license_hint"` // last 4 chars of license key
	Plan         string `json:"plan"`
	MembershipID string `json:"membership_id,omitempty"`
}

var (
	cloudMu   sync.RWMutex
	cloudAuth *CloudAuth // nil = not logged in
)

// loadCloudAuth reads the stored token from disk on startup.
func loadCloudAuth() {
	var auth CloudAuth
	if err := readJSONFile("cloud_auth.json", &auth); err == nil && auth.Token != "" {
		cloudMu.Lock()
		cloudAuth = &auth
		cloudMu.Unlock()
	}
}

func saveCloudAuth(auth *CloudAuth) {
	writeJSONFile("cloud_auth.json", auth)
}

func clearCloudAuth() {
	cloudMu.Lock()
	cloudAuth = nil
	cloudMu.Unlock()
	writeJSONFile("cloud_auth.json", &CloudAuth{})
}

// GetCloudAuth returns a copy of the current auth (nil-safe).
func GetCloudAuth() *CloudAuth {
	cloudMu.RLock()
	defer cloudMu.RUnlock()
	if cloudAuth == nil {
		return nil
	}
	cp := *cloudAuth
	return &cp
}

// ── Device ID (single-instance key locking) ───────────────────────────────────

// GetOrCreateDeviceID returns a persistent random device identifier stored in
// device_id.json.  Generated once on first run, stable forever.
func GetOrCreateDeviceID() string {
	var stored struct {
		ID string `json:"id"`
	}
	if readJSONFile("device_id.json", &stored) == nil && stored.ID != "" {
		return stored.ID
	}
	b := make([]byte, 16)
	io.ReadFull(rand.Reader, b)
	stored.ID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	writeJSONFile("device_id.json", &stored)
	return stored.ID
}

// ── User preferences ──────────────────────────────────────────────────────────

type UserPreferences struct {
	DiscordWebhookURL    string `json:"discord_webhook_url"`
	GlobalDiscordOpt     bool   `json:"global_discord_opt"`
	NotifyPXChanges      bool   `json:"notify_px_changes"`
	NotifyProviderIssues bool   `json:"notify_provider_issues"`
}

func CloudGetPreferences() (*UserPreferences, error) {
	auth := GetCloudAuth()
	if auth == nil {
		return nil, fmt.Errorf("not signed in")
	}
	b, status, err := cloudGet("/user/preferences", auth.Token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("status %d", status)
	}
	var prefs UserPreferences
	if err := json.Unmarshal(b, &prefs); err != nil {
		return nil, err
	}
	return &prefs, nil
}

func CloudSetPreferences(prefs UserPreferences) error {
	auth := GetCloudAuth()
	if auth == nil {
		return fmt.Errorf("not signed in")
	}
	_, status, err := cloudPost("/user/preferences", prefs, auth.Token)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("status %d", status)
	}
	return nil
}

// ── Anonymous suggestions ─────────────────────────────────────────────────────

func CloudSendSuggestion(body, category string) error {
	_, status, err := cloudPost("/suggestions", map[string]string{
		"body":     body,
		"category": category,
	}, "")
	if err != nil {
		return err
	}
	if status != 200 && status != 201 {
		return fmt.Errorf("status %d", status)
	}
	return nil
}

// ── Remote PX config ──────────────────────────────────────────────────────────

// RemotePXConfig is the admin-managed PX site configuration served by the backend.
type RemotePXConfig struct {
	Sites     []RemotePXSite `json:"sites"`
	IntervalM int            `json:"interval_m"`
}

// RemotePXSite is a single site entry from the backend PX config.
type RemotePXSite struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Protection string   `json:"protection"`
	BodyKW     []string `json:"body_kw"`
	HardCodes  []int    `json:"hard_codes"`
	HeaderKeys []string `json:"header_keys"`
	Enabled    bool     `json:"enabled"`
}

// CloudFetchPXConfig pulls the admin-managed PX site list from the backend.
// No auth required — config is public.
func CloudFetchPXConfig() (*RemotePXConfig, error) {
	b, status, err := cloudGet("/px/config", "")
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("status %d", status)
	}
	var cfg RemotePXConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &cfg, nil
}

// CloudTransferDevice binds this device to the given license key,
// replacing any previous device binding and issuing a fresh session token.
func CloudTransferDevice(licenseKey string) (*CloudAuth, error) {
	b, status, err := cloudPost("/auth/transfer-device", map[string]string{
		"license_key": licenseKey,
		"device_id":   GetOrCreateDeviceID(),
	}, "")
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	if status != 200 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("transfer failed (%d)", status)
	}
	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID           string `json:"id"`
			LicenseHint  string `json:"license_hint"`
			Plan         string `json:"plan"`
			MembershipID string `json:"membership_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	auth := &CloudAuth{
		Token:        resp.Token,
		UserID:       resp.User.ID,
		LicenseHint:  resp.User.LicenseHint,
		Plan:         resp.User.Plan,
		MembershipID: resp.User.MembershipID,
	}
	cloudMu.Lock()
	cloudAuth = auth
	cloudMu.Unlock()
	saveCloudAuth(auth)
	return auth, nil
}

// ── API helpers ───────────────────────────────────────────────────────────────

func cloudPost(path string, payload any, token string) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest("POST", CloudBackendURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

func cloudGet(path, token string, timeoutSecs ...int) ([]byte, int, error) {
	req, err := http.NewRequest("GET", CloudBackendURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	timeout := 15 * time.Second
	if len(timeoutSecs) > 0 && timeoutSecs[0] > 0 {
		timeout = time.Duration(timeoutSecs[0]) * time.Second
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// ── Auth: Whop license key validation ────────────────────────────────────────

// CloudValidateKey validates a PT- license key against our D1 database.
// Keys are generated automatically when a Whop purchase is made.
func CloudValidateKey(licenseKey string) (*CloudAuth, error) {
	b, status, err := cloudPost("/auth/validate-key", map[string]string{
		"license_key": licenseKey,
		"device_id":   GetOrCreateDeviceID(),
	}, "")
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	if status != 200 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("validation failed (%d)", status)
	}

	var resp struct {
		Token string `json:"token"`
		User  struct {
			ID           string `json:"id"`
			LicenseHint  string `json:"license_hint"`
			Plan         string `json:"plan"`
			MembershipID string `json:"membership_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}

	auth := &CloudAuth{
		Token:        resp.Token,
		UserID:       resp.User.ID,
		LicenseHint:  resp.User.LicenseHint,
		Plan:         resp.User.Plan,
		MembershipID: resp.User.MembershipID,
	}
	cloudMu.Lock()
	cloudAuth = auth
	cloudMu.Unlock()
	saveCloudAuth(auth)
	return auth, nil
}

// CloudLogout revokes the session token and clears local state.
func CloudLogout() {
	auth := GetCloudAuth()
	if auth == nil {
		return
	}
	go cloudPost("/auth/logout", nil, auth.Token)
	clearCloudAuth()
}

// CloudVerifyToken confirms the stored token is still valid with the server.
// Returns nil if expired or offline (non-fatal).
func CloudVerifyToken() *CloudAuth {
	auth := GetCloudAuth()
	if auth == nil {
		return nil
	}
	b, status, err := cloudGet("/auth/me", auth.Token)
	if err != nil || status != 200 {
		return nil
	}
	var resp struct {
		ID          string `json:"id"`
		LicenseHint string `json:"license_hint"`
		Plan        string `json:"plan"`
	}
	if json.Unmarshal(b, &resp) != nil {
		return nil
	}
	cloudMu.Lock()
	cloudAuth.LicenseHint = resp.LicenseHint
	cloudAuth.Plan        = resp.Plan
	cloudMu.Unlock()
	return GetCloudAuth()
}

// ── Result sync ───────────────────────────────────────────────────────────────

// SyncJobResult pushes a completed job's sanitised summary to the cloud.
// Called automatically from RunJob; no-op if not logged in.
// Never transmits proxy host/port/credentials.
func SyncJobResult(job *Job, results []ProxyResult) {
	auth := GetCloudAuth()
	if auth == nil {
		return
	}

	// Build sanitised proxy list — egress IP + metadata only
	type safeProxy struct {
		EgressIP string `json:"egress_ip,omitempty"`
		IpType   string `json:"ip_type,omitempty"`
		AvgMs    int    `json:"avg_ms"`
		Score    int    `json:"score"`
		ISP      string `json:"isp,omitempty"`
		Country  string `json:"country,omitempty"`
	}
	top := make([]safeProxy, 0, 100)
	for i, r := range results {
		if i >= 100 {
			break
		}
		avgMs := 0
		if r.AvgMs != nil {
			avgMs = *r.AvgMs
		}
		isp, country := "", ""
		if r.IpInfo != nil {
			isp     = r.IpInfo.ISP
			country = r.IpInfo.Country
		}
		top = append(top, safeProxy{
			EgressIP: r.EgressIP,
			IpType:   r.IpType,
			AvgMs:    avgMs,
			Score:    r.Score,
			ISP:      isp,
			Country:  country,
		})
	}

	// Compute aggregate stats
	passRate := 0.0
	if job.Total > 0 {
		passRate = float64(job.Passed) / float64(job.Total) * 100
	}
	avgMs, count := 0, 0
	for _, r := range results {
		if r.AvgMs != nil {
			avgMs += *r.AvgMs
			count++
		}
	}
	if count > 0 {
		avgMs /= count
	}

	payload := map[string]any{
		"job_id":      job.JobID,
		"list_name":   job.ListName,
		"total":       job.Total,
		"passed":      job.Passed,
		"failed":      job.Failed,
		"pass_rate":   passRate,
		"avg_ms":      avgMs,
		"ip_analysis": AnalyzeEgressIPs(results),
		"data_usage":  job.DataUsage,
		"top_proxies": top,
	}

	b, status, err := cloudPost("/results/sync", payload, auth.Token)
	if err != nil || status != 200 {
		_ = b // non-fatal
	}
}
