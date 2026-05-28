package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ── AI keys storage ───────────────────────────────────────────────────────────

type AiKeysFile struct {
	Keys            map[string]string `json:"keys"`
	DefaultProvider string            `json:"default_provider"`
}

func loadAiKeys() AiKeysFile {
	var f AiKeysFile
	if err := readJSONFile("ai-keys.json", &f); err != nil {
		f.Keys = map[string]string{}
	}
	if f.Keys == nil {
		f.Keys = map[string]string{}
	}
	return f
}

func saveAiKeys(f AiKeysFile) {
	writeJSONFile("ai-keys.json", f)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// handleAiKeys — GET/POST /api/ai-keys
func handleAiKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		f := loadAiKeys()
		// Return masked hints so the UI can show "key saved" state
		hints := map[string]string{}
		for k, v := range f.Keys {
			if v != "" {
				hints[k] = maskKey(v)
			}
		}
		writeJSON(w, 200, map[string]any{
			"keys":             hints,
			"default_provider": f.DefaultProvider,
		})

	case "POST":
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(body, &req); err != nil {
			errJSON(w, 400, "invalid JSON")
			return
		}
		f := loadAiKeys()
		if dp, ok := req["default_provider"]; ok {
			f.DefaultProvider = dp
		}
		if provider, ok := req["provider"]; ok {
			key := strings.TrimSpace(req["key"])
			if key == "" {
				delete(f.Keys, provider)
			} else {
				f.Keys[provider] = key
			}
			// Auto-set default if none chosen
			if f.DefaultProvider == "" && key != "" {
				f.DefaultProvider = provider
			}
		}
		saveAiKeys(f)
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// maskKey returns a short hint like "sk-ant-***...WXYZ" so the UI shows the
// key is saved without exposing the full secret.
func maskKey(k string) string {
	if len(k) <= 8 {
		return "***"
	}
	return k[:6] + "***" + k[len(k)-4:]
}

// handleAiAnalyze — POST /api/ai-analyze
// Body: { job_id, session_id, compare_job_id }
func handleAiAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}

	var req struct {
		JobID        string `json:"job_id"`
		SessionID    string `json:"session_id"`
		CompareJobID string `json:"compare_job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, 400, "invalid JSON")
		return
	}

	// Load AI keys
	f := loadAiKeys()
	provider, key := pickAiProvider(f)
	if key == "" {
		errJSON(w, 400, "No AI API keys configured — add a key in Settings → AI")
		return
	}

	// Build prompt
	var prompt string
	var buildErr error
	switch {
	case req.CompareJobID != "" && req.JobID != "":
		prompt, buildErr = buildComparePrompt(req.JobID, req.CompareJobID)
	case req.JobID != "":
		prompt, buildErr = buildJobPrompt(req.JobID)
	case req.SessionID != "":
		prompt, buildErr = buildSessionPrompt(req.SessionID)
	default:
		errJSON(w, 400, "job_id or session_id required")
		return
	}
	if buildErr != nil {
		errJSON(w, 500, "failed to load results: "+buildErr.Error())
		return
	}

	// Call AI
	response, err := callAI(provider, key, prompt)
	if err != nil {
		errJSON(w, 502, "AI request failed: "+err.Error())
		return
	}

	writeJSON(w, 200, map[string]string{
		"response": response,
		"provider": provider,
	})
}

// ── Provider selection ────────────────────────────────────────────────────────

func pickAiProvider(f AiKeysFile) (string, string) {
	order := []string{"claude", "openai", "gemini"}
	if f.DefaultProvider != "" {
		if v, ok := f.Keys[f.DefaultProvider]; ok && v != "" {
			return f.DefaultProvider, v
		}
	}
	for _, p := range order {
		if v, ok := f.Keys[p]; ok && v != "" {
			return p, v
		}
	}
	return "", ""
}

// ── Prompt builders ───────────────────────────────────────────────────────────

type resultFileSummary struct {
	JobID      string         `json:"job_id"`
	Config     *JobConfig     `json:"config"`
	Stats      map[string]int `json:"stats"`
	DataUsage  *DataUsage     `json:"data_usage"`
	IpAnalysis *IpAnalysis    `json:"ip_analysis"`
}

// safeJobID rejects any job ID that would escape the results directory
// (path traversal, absolute paths, etc.).
func safeJobID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	// Disallow any path separators or directory traversal sequences
	if strings.ContainsAny(id, `/\:`) || strings.Contains(id, "..") {
		return false
	}
	return true
}

func loadResultSummary(jobID string) (*resultFileSummary, error) {
	if !safeJobID(jobID) {
		return nil, fmt.Errorf("invalid job ID")
	}
	fp := filepath.Join(resultsDir(), jobID+".json")
	// Ensure the resolved path is still inside resultsDir (belt-and-suspenders)
	rDir, _ := filepath.Abs(resultsDir())
	rPath, _ := filepath.Abs(fp)
	if !strings.HasPrefix(rPath, rDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("invalid job ID")
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("result not found for job %s", jobID)
	}
	var s resultFileSummary
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func buildJobPrompt(jobID string) (string, error) {
	s, err := loadResultSummary(jobID)
	if err != nil {
		return "", err
	}
	return formatJobPrompt("proxy test run", s), nil
}

func buildComparePrompt(jobID1, jobID2 string) (string, error) {
	s1, err := loadResultSummary(jobID1)
	if err != nil {
		return "", err
	}
	s2, err := loadResultSummary(jobID2)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("You are a proxy testing analyst. Compare these two proxy pool test results:\n\n")
	sb.WriteString("**Pool A:**\n")
	sb.WriteString(summaryBlock(s1))
	sb.WriteString("\n**Pool B:**\n")
	sb.WriteString(summaryBlock(s2))
	sb.WriteString("\nWhich pool performed better and why? Focus on pass rate, latency, IP quality (residential vs datacenter), bot-protection detections, and diversity. Give a concise recommendation of which pool to use.")
	return sb.String(), nil
}

func buildSessionPrompt(sessionID string) (string, error) {
	if !safeJobID(sessionID) {
		return "", fmt.Errorf("invalid session ID")
	}
	rDir, _ := filepath.Abs(resultsDir())
	fp := filepath.Join(resultsDir(), "session_"+sessionID+".json")
	rPath, _ := filepath.Abs(fp)
	if !strings.HasPrefix(rPath, rDir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid session ID")
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return "", fmt.Errorf("session not found: %s", sessionID)
	}
	// Sessions have a similar stats shape — just format raw JSON excerpt
	var raw map[string]any
	json.Unmarshal(data, &raw)

	var sb strings.Builder
	sb.WriteString("You are a proxy testing analyst. Here is a summary of a proxy testing session (multiple runs):\n\n")
	if name, ok := raw["name"].(string); ok {
		sb.WriteString(fmt.Sprintf("Session: %s\n", name))
	}
	if runs, ok := raw["runs"].([]any); ok {
		sb.WriteString(fmt.Sprintf("Total runs: %d\n", len(runs)))
	}
	// Embed a compact JSON excerpt (first 2000 chars to avoid token limits)
	compact, _ := json.Marshal(raw)
	excerpt := string(compact)
	if len(excerpt) > 2000 {
		excerpt = excerpt[:2000] + "..."
	}
	sb.WriteString("\nData: ")
	sb.WriteString(excerpt)
	sb.WriteString("\n\nProvide a brief, actionable analysis of this proxy session. Highlight trends across runs, quality issues, and recommendations.")
	return sb.String(), nil
}

func formatJobPrompt(label string, s *resultFileSummary) string {
	var sb strings.Builder
	sb.WriteString("You are a proxy testing analyst. Here are the results from a recent ")
	sb.WriteString(label)
	sb.WriteString(":\n\n")
	sb.WriteString(summaryBlock(s))
	sb.WriteString("\nProvide a brief, actionable analysis. Cover: overall quality verdict, any red flags (high datacenter %, bot detections, reused IPs), and 2-3 specific recommendations for improving results.")
	return sb.String()
}

func summaryBlock(s *resultFileSummary) string {
	if s == nil {
		return "(no data)\n"
	}
	var sb strings.Builder
	total := s.Stats["total"]
	passed := s.Stats["passed"]
	failed := s.Stats["failed"]
	passRate := 0.0
	if total > 0 {
		passRate = float64(passed) / float64(total) * 100
	}
	sb.WriteString(fmt.Sprintf("- Total proxies: %d  |  Passed: %d (%.1f%%)  |  Failed: %d\n", total, passed, passRate, failed))

	if s.DataUsage != nil {
		du := s.DataUsage
		if du.HttpbinPassPct != nil {
			sb.WriteString(fmt.Sprintf("- Connectivity (httpbin): %.1f%%\n", *du.HttpbinPassPct))
		}
		if du.TargetPassPct != nil {
			sb.WriteString(fmt.Sprintf("- Target pass rate: %.1f%%\n", *du.TargetPassPct))
		}
		if du.PxChallengePct > 0 {
			sb.WriteString(fmt.Sprintf("- Bot-protection triggered: %.1f%% of requests\n", du.PxChallengePct))
		}
		if len(du.VendorCounts) > 0 {
			vendors := []string{}
			for v, n := range du.VendorCounts {
				vendors = append(vendors, fmt.Sprintf("%s×%d", v, n))
			}
			sb.WriteString("- Bot vendors detected: " + strings.Join(vendors, ", ") + "\n")
		}
	}
	if s.IpAnalysis != nil {
		ia := s.IpAnalysis
		sb.WriteString(fmt.Sprintf("- Unique IPs: %d  |  IP reuse rate: %.1f%%\n", ia.UniqueIPs, ia.ReuseRatePct))
		sb.WriteString(fmt.Sprintf("- IP types: Residential %.1f%%  |  Datacenter %.1f%%  |  Mobile %d\n",
			ia.ResidentialPct, ia.DatacenterPct, ia.MobileCount))
		sb.WriteString(fmt.Sprintf("- ISP diversity: %d ISPs  |  Quality score: %d/100\n", ia.ISPDiversity, ia.QualityScore))
		if ia.FlaggedCount > 0 {
			sb.WriteString(fmt.Sprintf("- Proxy-flagged IPs: %d\n", ia.FlaggedCount))
		}
	}
	if s.Config != nil {
		if s.Config.TargetURL != "" {
			sb.WriteString(fmt.Sprintf("- Target: %s\n", s.Config.TargetURL))
		}
	}
	return sb.String()
}

// ── AI provider calls ─────────────────────────────────────────────────────────

func callAI(provider, key, prompt string) (string, error) {
	switch provider {
	case "claude":
		return callClaude(key, prompt)
	case "openai":
		return callOpenAI(key, prompt)
	case "gemini":
		return callGemini(key, prompt)
	default:
		return "", fmt.Errorf("unknown provider: %s", provider)
	}
}

func callClaude(key, prompt string) (string, error) {
	payload := map[string]any{
		"model":      "claude-3-5-haiku-20241022",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(rb, &out)
	if out.Error.Message != "" {
		return "", fmt.Errorf("Claude: %s", out.Error.Message)
	}
	if len(out.Content) > 0 {
		return out.Content[0].Text, nil
	}
	return "", fmt.Errorf("empty response from Claude")
}

func callOpenAI(key, prompt string) (string, error) {
	payload := map[string]any{
		"model":      "gpt-4o-mini",
		"max_tokens": 1024,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("content-type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(rb, &out)
	if out.Error.Message != "" {
		return "", fmt.Errorf("OpenAI: %s", out.Error.Message)
	}
	if len(out.Choices) > 0 {
		return out.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("empty response from OpenAI")
}

func callGemini(key, prompt string) (string, error) {
	payload := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
	}
	body, _ := json.Marshal(payload)
	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=" + key
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(rb, &out)
	if out.Error.Message != "" {
		return "", fmt.Errorf("Gemini: %s", out.Error.Message)
	}
	if len(out.Candidates) > 0 && len(out.Candidates[0].Content.Parts) > 0 {
		return out.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("empty response from Gemini")
}

// ── Restart / update ──────────────────────────────────────────────────────────

func handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	// Re-launch self and exit — give the response time to flush first
	go func() {
		time.Sleep(300 * time.Millisecond)
		// On Windows the exe can't replace itself while running, so we just
		// re-exec the current binary and let the old process exit cleanly.
		self := os.Args[0]
		p, err := os.StartProcess(self, os.Args, &os.ProcAttr{
			Files: []*os.File{nil, nil, nil},
		})
		if err == nil {
			p.Release()
		}
		os.Exit(0)
	}()
}

// handleCheckUpdate polls the cloud backend for a newer version.
// Only works when the user has an active session (auth token).
func handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	cloudMu.RLock()
	auth := cloudAuth
	cloudMu.RUnlock()

	if auth == nil || auth.Token == "" {
		writeJSON(w, 200, map[string]any{"update_available": false, "reason": "not_authenticated"})
		return
	}

	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, "GET", CloudBackendURL+"/update/latest", nil)
	if err != nil {
		writeJSON(w, 200, map[string]any{"update_available": false})
		return
	}
	req.Header.Set("Authorization", "Bearer "+auth.Token)
	req.Header.Set("User-Agent", "ProxyKit/"+AppVersion)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		writeJSON(w, 200, map[string]any{"update_available": false})
		return
	}
	defer resp.Body.Close()

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		writeJSON(w, 200, map[string]any{"update_available": false})
		return
	}

	latestVer, _ := data["version"].(string)
	downloadURL, _ := data["download_url"].(string)
	notes, _ := data["notes"].(string)

	if latestVer == "" || downloadURL == "" {
		writeJSON(w, 200, map[string]any{"update_available": false, "current": AppVersion})
		return
	}

	if semverCmp(latestVer, AppVersion) <= 0 {
		writeJSON(w, 200, map[string]any{
			"update_available": false,
			"current":         AppVersion,
			"latest":          latestVer,
		})
		return
	}

	writeJSON(w, 200, map[string]any{
		"update_available": true,
		"version":         latestVer,
		"download_url":    downloadURL,
		"notes":           notes,
		"current":         AppVersion,
	})
}

// semverCmp compares two semver strings "a" and "b".
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func semverCmp(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			va, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			vb, _ = strconv.Atoi(pb[i])
		}
		if va > vb {
			return 1
		}
		if va < vb {
			return -1
		}
	}
	return 0
}

// handleApplyUpdate downloads a new exe and schedules a self-replace on exit.
func handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}

	var req struct {
		DownloadURL string `json:"download_url"`
		Version     string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DownloadURL == "" {
		errJSON(w, 400, "download_url required")
		return
	}

	// Only allow downloads from trusted sources
	trusted := []string{
		"https://github.com/treys5/proxykit/",
		"https://objects.githubusercontent.com/",
		"https://proxykit-backend.proxykit.workers.dev/",
	}
	allowed := false
	for _, pfx := range trusted {
		if strings.HasPrefix(req.DownloadURL, pfx) {
			allowed = true
			break
		}
	}
	if !allowed {
		errJSON(w, 400, "update URL not from trusted source")
		return
	}

	// Determine paths
	self := os.Args[0]
	if !filepath.IsAbs(self) {
		if abs, err := filepath.Abs(self); err == nil {
			self = abs
		}
	}
	dir := filepath.Dir(self)
	exeName := filepath.Base(self)
	newExe := filepath.Join(dir, "ProxyKit_update.exe")

	// Download the new exe
	dlResp, err := http.Get(req.DownloadURL)
	if err != nil {
		errJSON(w, 500, "download failed: "+err.Error())
		return
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		errJSON(w, 500, fmt.Sprintf("download HTTP %d", dlResp.StatusCode))
		return
	}

	f, err := os.Create(newExe)
	if err != nil {
		errJSON(w, 500, "cannot create update file: "+err.Error())
		return
	}
	_, copyErr := io.Copy(f, dlResp.Body)
	f.Close()
	if copyErr != nil {
		os.Remove(newExe)
		errJSON(w, 500, "download incomplete: "+copyErr.Error())
		return
	}

	// Write a batch script that waits for this process to exit, swaps the exe,
	// and launches the new version. The batch deletes itself on completion.
	pid := os.Getpid()
	batPath := filepath.Join(dir, "proxykit_update.bat")
	bat := fmt.Sprintf(`@echo off
:waitloop
tasklist /FI "PID eq %d" 2>nul | find /I "%d" >nul
if not errorlevel 1 (timeout /t 1 /nobreak >nul & goto waitloop)
move /y "%s" "%s" >nul 2>&1
start "" "%s"
(goto) 2>nul & del "%%~f0"
`, pid, pid, newExe, filepath.Join(dir, exeName), filepath.Join(dir, exeName))

	if err := os.WriteFile(batPath, []byte(bat), 0755); err != nil {
		os.Remove(newExe)
		errJSON(w, 500, "cannot write update script")
		return
	}

	// Success — tell the frontend to proceed with the countdown + restart
	ver := req.Version
	if ver == "" {
		ver = AppVersion
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": ver})

	// Launch the updater bat and exit after giving the response time to flush
	go func() {
		time.Sleep(350 * time.Millisecond)
		cmd := exec.Command("cmd", "/c", "start", "/min", "", batPath)
		cmd.Start() //nolint:errcheck
		os.Exit(0)
	}()
}
