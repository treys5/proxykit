package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── CORS helper ───────────────────────────────────────────────────────────────

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ── Main router ───────────────────────────────────────────────────────────────

func NewRouter() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.HandleFunc("/", handleStatic)

	// API routes
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/events", handleSSE)
	mux.HandleFunc("/api/estimate", handleEstimate)
	mux.HandleFunc("/api/jobs", handleJobs)
	mux.HandleFunc("/api/jobs/", handleJobSub)
	mux.HandleFunc("/api/sessions/compare", handleSessionsCompare)
	mux.HandleFunc("/api/sessions", handleSessions)
	mux.HandleFunc("/api/sessions/", handleSessionSub)
	mux.HandleFunc("/api/providers/autopick", handleProvidersAutopick)
	mux.HandleFunc("/api/providers", handleProviders)
	mux.HandleFunc("/api/providers/", handleProviderSub)
	mux.HandleFunc("/api/schedules", handleSchedules)
	mux.HandleFunc("/api/schedules/", handleScheduleSub)
	mux.HandleFunc("/api/drops", handleDrops)
	mux.HandleFunc("/api/drops/", handleDropSub)
	mux.HandleFunc("/api/lists", handleLists)
	mux.HandleFunc("/api/integrations", handleIntegrations)
	mux.HandleFunc("/api/score-config", handleScoreConfig)
	mux.HandleFunc("/api/notify-config", handleNotifyConfig)
	mux.HandleFunc("/api/analytics/config", handleAnalyticsConfig)
	mux.HandleFunc("/api/analytics/settings", handleAnalyticsConfig)   // alias used by frontend
	mux.HandleFunc("/api/community-stats", handleCommunityStats)
	mux.HandleFunc("/api/analytics/community", handleCommunityStats)   // alias used by frontend

	// AI features
	mux.HandleFunc("/api/ai-keys", handleAiKeys)
	mux.HandleFunc("/api/ai-analyze", handleAiAnalyze)

	// App lifecycle
	mux.HandleFunc("/api/restart", handleRestart)
	mux.HandleFunc("/api/check-update", handleCheckUpdate)
	mux.HandleFunc("/api/apply-update", handleApplyUpdate)

	// Cloud sync / auth (Whop license key)
	mux.HandleFunc("/api/cloud/validate-key", handleCloudValidateKey)
	mux.HandleFunc("/api/cloud/verify", handleCloudVerify)
	mux.HandleFunc("/api/cloud/logout", handleCloudLogout)
	mux.HandleFunc("/api/cloud/status", handleCloudStatus)
	mux.HandleFunc("/api/cloud/results", handleCloudResults)
	mux.HandleFunc("/api/cloud/results/", handleCloudResultSub)

	// PX monitor
	mux.HandleFunc("/api/px/status", handlePXStatus)
	mux.HandleFunc("/api/px/check-now", handlePXCheckNow)
	mux.HandleFunc("/api/px/refresh-config", handlePXRefreshConfig)

	// User preferences (proxied to backend)
	mux.HandleFunc("/api/preferences", handlePreferences)

	// Anonymous suggestions (proxied to backend)
	mux.HandleFunc("/api/suggestions", handleSuggestions)

	// App data export / import (device migration)
	mux.HandleFunc("/api/export", handleExport)
	mux.HandleFunc("/api/import", handleImport)

	// Device transfer (re-bind license key to this machine)
	mux.HandleFunc("/api/cloud/transfer-device", handleCloudTransferDevice)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ── Static file serving ───────────────────────────────────────────────────────

func handleStatic(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	if urlPath == "/" || urlPath == "" {
		urlPath = "/index.html"
	}

	// ── Dev override: serve from disk if the file exists there ───────────────
	fp := filepath.Join(DataDir, urlPath)
	absData, _ := filepath.Abs(DataDir)
	absFP, err := filepath.Abs(fp)
	if err == nil && strings.HasPrefix(absFP, absData+string(filepath.Separator)) {
		if info, err := os.Stat(fp); err == nil && !info.IsDir() {
			http.ServeFile(w, r, fp)
			return
		}
	}

	// ── Release path: serve from embedded FS ─────────────────────────────────
	name := strings.TrimPrefix(urlPath, "/")
	data, err := embeddedFS.ReadFile(name)
	if err != nil {
		// SPA fallback to embedded index.html
		data, err = embeddedFS.ReadFile("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}

	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "text/html; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data) //nolint:errcheck
}

// ── Version ───────────────────────────────────────────────────────────────────

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"version": AppVersion})
}

// ── SSE ───────────────────────────────────────────────────────────────────────

func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	send := func() {
		globalMu.RLock()
		type snap struct {
			JobID       string  `json:"job_id"`
			Status      string  `json:"status"`
			Total       int     `json:"total"`
			Tested      int     `json:"tested"`
			Passed      int     `json:"passed"`
			Failed      int     `json:"failed"`
			ProgressPct float64 `json:"progress_pct"`
			ElapsedSec  float64 `json:"elapsed_sec"`
			EtaSec      *int    `json:"eta_sec,omitempty"`
			ListName    string  `json:"list_name"`
			SessionID   string  `json:"session_id,omitempty"`
		}
		var snaps []snap
		for _, j := range globalJobs {
			j.mu.Lock()
			snaps = append(snaps, snap{
				JobID:       j.JobID,
				Status:      j.Status,
				Total:       j.Total,
				Tested:      j.Tested,
				Passed:      j.Passed,
				Failed:      j.Failed,
				ProgressPct: j.ProgressPct,
				ElapsedSec:  j.ElapsedSec,
				EtaSec:      j.EtaSec,
				ListName:    j.ListName,
				SessionID:   j.SessionID,
			})
			j.mu.Unlock()
		}
		globalMu.RUnlock()
		data, _ := json.Marshal(snaps)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	send()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

// ── Estimate ──────────────────────────────────────────────────────────────────

func handleEstimate(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("proxies"))
	if n < 1 {
		n = 1000
	}

	// In-memory samples
	var samples []int
	globalMu.RLock()
	for _, j := range globalJobs {
		if j.Status == "done" && j.DataUsage != nil && j.DataUsage.AvgBytesPerProxy > 0 {
			samples = append(samples, j.DataUsage.AvgBytesPerProxy)
		}
	}
	globalMu.RUnlock()

	if len(samples) == 0 {
		cal := loadCalibrationData()
		for _, e := range cal {
			samples = append(samples, e.AvgBytesPerProxy)
		}
	}

	if len(samples) == 0 {
		errJSON(w, 400, "No test history yet — run at least one test to calibrate.")
		return
	}

	sum := 0
	for _, s := range samples {
		sum += s
	}
	avg := sum / len(samples)
	// Application-layer byte counts miss TLS handshakes, TCP headers, and
	// response body framing. Empirically, actual OS-level usage is ~4x the
	// measured bytes, so we apply that multiplier here.
	const networkOverhead = 4
	estimated := avg * n * networkOverhead

	writeJSON(w, 200, map[string]any{
		"proxy_count":          n,
		"avg_bytes_per_proxy":  avg,
		"estimated_bytes":      estimated,
		"estimated_kb":         estimated / 1024,
		"estimated_mb":         float64(estimated) / 1048576,
		"calibrated_from":      len(samples),
	})
}

// ── Jobs ──────────────────────────────────────────────────────────────────────

func handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		globalMu.RLock()
		var list []any
		for _, j := range globalJobs {
			j.mu.Lock()
			list = append(list, map[string]any{
				"job_id": j.JobID, "status": j.Status,
				"total": j.Total, "tested": j.Tested, "passed": j.Passed, "failed": j.Failed,
				"progress_pct": j.ProgressPct, "elapsed_sec": j.ElapsedSec, "eta_sec": j.EtaSec,
				"list_name": j.ListName, "session_id": j.SessionID,
				"px_challenge_count": j.pxCount,
				"httpbin_tested": j.httpbinTested, "httpbin_passed": j.httpbinPassed,
				"target_tested": j.targetTested, "target_passed": j.targetPassed,
				"data_usage": j.DataUsage,
			})
			j.mu.Unlock()
		}
		globalMu.RUnlock()
		if list == nil {
			list = []any{}
		}
		writeJSON(w, 200, list)

	case "POST":
		fields, err := parseMultipartForm(r)
		if err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		fileData, ok := fields["file"]
		if !ok {
			errJSON(w, 400, "No file field")
			return
		}
		proxies := ParseProxies(string(fileData))
		if len(proxies) == 0 {
			errJSON(w, 400, "No valid proxies found")
			return
		}

		var parsedTURLs []TargetEndpoint
		if v, ok := fields["target_urls"]; ok {
			json.Unmarshal(v, &parsedTURLs)
		}

		config := &JobConfig{
			TestURL:     fieldStr(fields, "test_url", IpApiURL),
			TargetURL:   strings.TrimSpace(fieldStr(fields, "target_url", "")),
			TargetURLs:  parsedTURLs,
			Concurrency: fieldInt(fields, "concurrency", 150),
			Timeout:     fieldFloat(fields, "timeout", 10),
			Retries:     fieldInt(fields, "retries", 1),
			TopN:        fieldInt(fields, "top_n", 1000),
			SkipHttpbin: fieldStr(fields, "skip_httpbin", "") == "true",
		}

		jobID := generateJobID()
		sid := fieldStr(fields, "session_id", "")
		fname := fieldStr(fields, "filename", "proxies.txt")

		job := &Job{
			JobID:      jobID,
			Status:     "queued",
			SessionID:  sid,
			Total:      len(proxies),
			ListName:   fname,
			Config:     config,
			TopProxies: []ProxyResult{},
		}

		globalMu.Lock()
		globalJobs[jobID] = job
		globalMu.Unlock()

		go RunJob(jobID, proxies, config)

		writeJSON(w, 200, map[string]any{
			"job_id": jobID, "total_proxies": len(proxies), "session_id": sid,
		})

	default:
		w.WriteHeader(405)
	}
}

func handleJobSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/", 2)
	jobID := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = "/" + parts[1]
	}

	globalMu.RLock()
	job := globalJobs[jobID]
	globalMu.RUnlock()
	if job == nil {
		errJSON(w, 404, "Not found")
		return
	}

	switch {
	case sub == "/results" && r.Method == "GET":
		if job.Status != "done" {
			errJSON(w, 400, "Not complete")
			return
		}
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if lim <= 0 {
			lim = 1000
		}
		job.mu.Lock()
		top := job.TopProxies
		ia := job.IpAnalysis
		job.mu.Unlock()
		end := off + lim
		if end > len(top) {
			end = len(top)
		}
		if off > len(top) {
			off = len(top)
		}
		writeJSON(w, 200, map[string]any{
			"total": len(top), "offset": off, "limit": lim,
			"proxies": top[off:end], "ip_analysis": ia,
		})

	case sub == "/analysis" && r.Method == "GET":
		if job.Status != "done" {
			errJSON(w, 400, "Not complete")
			return
		}
		job.mu.Lock()
		ia := job.IpAnalysis
		job.mu.Unlock()
		writeJSON(w, 200, ia)

	case sub == "/copy" && r.Method == "GET":
		if job.Status != "done" {
			errJSON(w, 400, "Not complete")
			return
		}
		excludeDC := r.URL.Query().Get("exclude_dc") != "false"
		provider := strings.ToLower(r.URL.Query().Get("provider"))
		maxPerASN, _ := strconv.Atoi(r.URL.Query().Get("max_per_asn"))
		maxPerCity, _ := strconv.Atoi(r.URL.Query().Get("max_per_city"))

		job.mu.Lock()
		list := make([]ProxyResult, len(job.TopProxies))
		copy(list, job.TopProxies)
		job.mu.Unlock()

		var filtered []ProxyResult
		for _, p := range list {
			if provider != "" {
				pName := ""
				if p.IpInfo != nil {
					pName = strings.ToLower(p.IpInfo.ISP)
				}
				if pName != provider {
					continue
				}
			}
			if excludeDC && (p.IpType == "datacenter" || p.IpType == "private") {
				continue
			}
			filtered = append(filtered, p)
		}
		filtered = ApplyDiversityCaps(filtered, maxPerASN, maxPerCity)

		var lines []string
		for _, p := range filtered {
			lines = append(lines, ProxyLine(&p))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))

	case sub == "/elite" && r.Method == "GET":
		if job.Status != "done" {
			errJSON(w, 400, "Not complete")
			return
		}
		q := r.URL.Query()
		minScore, _ := strconv.Atoi(q.Get("min_score"))
		if minScore == 0 {
			minScore = 60
		}
		maxMs, _ := strconv.Atoi(q.Get("max_ms"))
		if maxMs == 0 {
			maxMs = 800
		}
		excludeDC := q.Get("exclude_dc") != "false"
		excludePX := q.Get("exclude_px") != "false"
		dedupeIP := q.Get("dedupe_ip") != "false"
		excludeSSL := q.Get("exclude_ssl") == "true"
		excludeRot := q.Get("exclude_rotating") == "true"
		maxPerASN, _ := strconv.Atoi(q.Get("max_per_asn"))
		if maxPerASN == 0 {
			maxPerASN = 5
		}
		maxPerCity, _ := strconv.Atoi(q.Get("max_per_city"))
		if maxPerCity == 0 {
			maxPerCity = 3
		}

		job.mu.Lock()
		list := make([]ProxyResult, len(job.TopProxies))
		copy(list, job.TopProxies)
		job.mu.Unlock()

		seenIPs := map[string]bool{}
		var filtered []ProxyResult
		for _, p := range list {
			if p.Score < minScore {
				continue
			}
			if p.AvgMs != nil && *p.AvgMs > maxMs {
				continue
			}
			if excludeDC && (p.IpType == "datacenter" || p.IpType == "private") {
				continue
			}
			if excludePX && p.PxChallenge {
				continue
			}
			if excludeSSL && p.SslInspected {
				continue
			}
			if excludeRot && p.Rotating {
				continue
			}
			if p.TargetPass != nil && !*p.TargetPass {
				continue
			}
			if dedupeIP && p.EgressIP != "" {
				if seenIPs[p.EgressIP] {
					continue
				}
				seenIPs[p.EgressIP] = true
			}
			filtered = append(filtered, p)
		}
		filtered = ApplyDiversityCaps(filtered, maxPerASN, maxPerCity)

		var lines []string
		for _, p := range filtered {
			lines = append(lines, ProxyLine(&p))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))

	case sub == "/export" && r.Method == "GET":
		if job.Status != "done" {
			errJSON(w, 400, "Not complete")
			return
		}
		fp := filepath.Join(resultsDir(), jobID+".json")
		if _, err := os.Stat(fp); os.IsNotExist(err) {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="proxies_`+jobID+`.json"`)
		http.ServeFile(w, r, fp)

	case r.Method == "DELETE":
		globalMu.Lock()
		delete(globalJobs, jobID)
		globalMu.Unlock()
		os.Remove(filepath.Join(resultsDir(), jobID+".json"))
		writeJSON(w, 200, map[string]string{"deleted": jobID})

	default:
		job.mu.Lock()
		writeJSON(w, 200, map[string]any{
			"job_id": job.JobID, "status": job.Status, "session_id": job.SessionID,
			"total": job.Total, "tested": job.Tested, "passed": job.Passed, "failed": job.Failed,
			"progress_pct": job.ProgressPct, "elapsed_sec": job.ElapsedSec, "eta_sec": job.EtaSec,
			"config": job.Config, "list_name": job.ListName,
			"top_proxies_count": len(job.TopProxies),
			"data_usage": job.DataUsage, "ip_analysis": job.IpAnalysis,
		})
		job.mu.Unlock()
	}
}

// ── Sessions ──────────────────────────────────────────────────────────────────

func handleSessionsCompare(w http.ResponseWriter, r *http.Request) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	var list []any
	for _, s := range globalSessions {
		a := s.Analytics
		if a == nil {
			a = computeSessionAnalytics(s)
		}
		avgMs := 0
		if a != nil && len(a.ISPSpeeds) > 0 {
			totMs, totCnt := 0, 0
			for _, sp := range a.ISPSpeeds {
				totMs += sp.AvgMs * sp.Count
				totCnt += sp.Count
			}
			if totCnt > 0 {
				avgMs = totMs / totCnt
			}
		}
		uniqueIPs := 0
		uncrowded := 0
		ispDiv := 0
		resPct := 0.0
		dcPct := 0.0
		if s.IpAnalysis != nil {
			uniqueIPs = s.IpAnalysis.UniqueIPs
			ispDiv = s.IpAnalysis.ISPDiversity
			resPct = s.IpAnalysis.ResidentialPct
			dcPct = s.IpAnalysis.DatacenterPct
		}
		if a != nil {
			uncrowded = a.UncrowdedCount
		}
		list = append(list, map[string]any{
			"session_id": s.SessionID, "name": s.Name, "run_count": s.RunCount,
			"proxy_count": len(s.Proxies), "analyzed_count": len(s.Analyzed),
			"quality_score": func() any {
				if s.IpAnalysis != nil {
					return s.IpAnalysis.QualityScore
				}
				return nil
			}(),
			"avg_ms": avgMs, "unique_ips": uniqueIPs,
			"uncrowded_count": uncrowded, "isp_count": ispDiv,
			"residential_pct": resPct, "datacenter_pct": dcPct,
			"updated_at": s.UpdatedAt,
		})
	}
	if list == nil {
		list = []any{}
	}
	writeJSON(w, 200, list)
}

func handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		globalMu.RLock()
		var list []any
		for _, s := range globalSessions {
			list = append(list, map[string]any{
				"session_id": s.SessionID, "name": s.Name, "status": s.Status,
				"run_count": s.RunCount, "proxy_count": len(s.Proxies),
				"proxy_type": s.ProxyType, "updated_at": s.UpdatedAt,
			})
		}
		globalMu.RUnlock()
		if list == nil {
			list = []any{}
		}
		writeJSON(w, 200, list)

	case "POST":
		fields, err := parseMultipartForm(r)
		if err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		fileData, ok := fields["file"]
		if !ok {
			errJSON(w, 400, "No file field")
			return
		}
		proxies := ParseProxies(string(fileData))
		if len(proxies) == 0 {
			errJSON(w, 400, "No valid proxies found")
			return
		}
		var parsedTURLs []TargetEndpoint
		if v, ok := fields["target_urls"]; ok {
			json.Unmarshal(v, &parsedTURLs)
		}
		config := &SessionConfig{
			TestURL:    fieldStr(fields, "test_url", IpApiURL),
			TargetURL:  strings.TrimSpace(fieldStr(fields, "target_url", "")),
			TargetURLs: parsedTURLs,
			Concurrency: fieldInt(fields, "concurrency", 150),
			Timeout:    fieldFloat(fields, "timeout", 10),
			Retries:    fieldInt(fields, "retries", 1),
			TopN:       fieldInt(fields, "top_n", 1000),
			SkipHttpbin: fieldStr(fields, "skip_httpbin", "") == "true",
		}
		sid := generateID()
		fname := fieldStr(fields, "filename", "proxies.txt")
		name := fieldStr(fields, "session_name", fname)
		sess := &Session{
			SessionID: sid, Name: name, Status: "idle", RunCount: 0,
			ProxyType: fieldStr(fields, "proxy_type", "residential"),
			RunIDs: []string{}, Config: config,
			Proxies: proxies, ProxyHistory: map[string]*ProxyHistoryEntry{},
			Analyzed: []AnalyzedProxy{}, BestProxies: []AnalyzedProxy{},
			CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
		}
		globalMu.Lock()
		globalSessions[sid] = sess
		globalMu.Unlock()
		writeJSON(w, 200, map[string]any{
			"session_id": sid, "proxy_count": len(proxies), "name": name,
		})

	default:
		w.WriteHeader(405)
	}
}

func handleSessionSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if path == "compare" {
		handleSessionsCompare(w, r)
		return
	}
	parts := strings.SplitN(path, "/", 2)
	sid := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = "/" + parts[1]
	}

	globalMu.RLock()
	s := globalSessions[sid]
	globalMu.RUnlock()
	if s == nil {
		errJSON(w, 404, "Session not found")
		return
	}

	switch {
	case sub == "" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{
			"session_id": s.SessionID, "name": s.Name, "status": s.Status,
			"run_count": s.RunCount, "run_ids": s.RunIDs,
			"proxy_count": len(s.Proxies), "config": s.Config,
			"updated_at": s.UpdatedAt, "best_count": len(s.BestProxies),
			"passed_all": len(s.Analyzed), "ip_analysis": s.IpAnalysis,
		})

	case sub == "/run" && r.Method == "POST":
		if s.Status == "running" {
			errJSON(w, 400, "Run already in progress")
			return
		}
		if len(s.Proxies) == 0 {
			errJSON(w, 400, "No proxies in session")
			return
		}
		body, _ := io.ReadAll(r.Body)
		var overrides map[string]any
		json.Unmarshal(body, &overrides)

		config := &JobConfig{
			TestURL:     s.Config.TestURL,
			TargetURL:   s.Config.TargetURL,
			TargetURLs:  s.Config.TargetURLs,
			Concurrency: s.Config.Concurrency,
			Timeout:     s.Config.Timeout,
			Retries:     s.Config.Retries,
			TopN:        s.Config.TopN,
			SkipHttpbin: s.Config.SkipHttpbin,
		}

		jobID := generateJobID()
		s.Status = "running"
		runNum := s.RunCount + 1

		job := &Job{
			JobID:      jobID,
			Status:     "queued",
			SessionID:  sid,
			Total:      len(s.Proxies),
			ListName:   fmt.Sprintf("%s — Run %d", s.Name, runNum),
			Config:     config,
			TopProxies: []ProxyResult{},
		}
		globalMu.Lock()
		globalJobs[jobID] = job
		globalMu.Unlock()

		proxiesCopy := make([]Proxy, len(s.Proxies))
		copy(proxiesCopy, s.Proxies)
		go func() {
			RunJob(jobID, proxiesCopy, config)
			s.Status = "idle"
		}()

		writeJSON(w, 200, map[string]any{
			"job_id": jobID, "run_number": runNum, "total_proxies": len(s.Proxies),
		})

	case sub == "/results" && r.Method == "GET":
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if lim <= 0 {
			lim = 2000
		}
		end := off + lim
		if end > len(s.Analyzed) {
			end = len(s.Analyzed)
		}
		if off > len(s.Analyzed) {
			off = len(s.Analyzed)
		}
		writeJSON(w, 200, map[string]any{
			"session_id": sid, "run_count": s.RunCount,
			"total": len(s.Analyzed), "offset": off, "limit": lim,
			"proxies": s.Analyzed[off:end], "ip_analysis": s.IpAnalysis,
		})

	case sub == "/analysis" && r.Method == "GET":
		writeJSON(w, 200, s.IpAnalysis)

	case sub == "/analytics" && r.Method == "GET":
		a := s.Analytics
		if a == nil {
			a = computeSessionAnalytics(s)
		}
		writeJSON(w, 200, a)

	case sub == "/copy" && r.Method == "GET":
		topN, _ := strconv.Atoi(r.URL.Query().Get("top_n"))
		if topN <= 0 {
			topN = 1000
		}
		minPasses, _ := strconv.Atoi(r.URL.Query().Get("min_passes"))
		if minPasses <= 0 {
			minPasses = s.RunCount
		}
		if minPasses <= 0 {
			minPasses = 1
		}
		excludeDC := r.URL.Query().Get("exclude_dc") != "false"
		provider := strings.ToLower(r.URL.Query().Get("provider"))

		var best []AnalyzedProxy
		for _, a := range s.Analyzed {
			if a.PassCount < minPasses {
				continue
			}
			if excludeDC && (a.IpType == "datacenter" || a.IpType == "private") {
				continue
			}
			if provider != "" {
				pName := ""
				if a.IpInfo != nil {
					pName = strings.ToLower(a.IpInfo.ISP)
				}
				if pName != provider {
					continue
				}
			}
			best = append(best, a)
		}
		if topN > len(best) {
			topN = len(best)
		}
		best = best[:topN]

		var lines []string
		for _, a := range best {
			lines = append(lines, ProxyLine(&a.Proxy))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))

	case sub == "/list" && r.Method == "GET":
		var lines []string
		for _, p := range s.Proxies {
			lines = append(lines, ProxyLineFromProxy(p))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n")))

	case sub == "" && r.Method == "PUT":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		if name, ok := data["name"].(string); ok && strings.TrimSpace(name) != "" {
			s.Name = strings.TrimSpace(name)
		}
		writeJSON(w, 200, map[string]string{"session_id": s.SessionID, "name": s.Name})

	case r.Method == "DELETE":
		globalMu.Lock()
		for _, jid := range s.RunIDs {
			delete(globalJobs, jid)
		}
		delete(globalSessions, sid)
		globalMu.Unlock()
		os.Remove(filepath.Join(resultsDir(), "session_"+sid+".json"))
		writeJSON(w, 200, map[string]string{"deleted": sid})

	default:
		w.WriteHeader(405)
	}
}

// ── Providers ─────────────────────────────────────────────────────────────────

func handleProvidersAutopick(w http.ResponseWriter, r *http.Request) {
	topN, _ := strconv.Atoi(r.URL.Query().Get("top_n"))
	if topN <= 0 {
		topN = 1000
	}
	minScore, _ := strconv.Atoi(r.URL.Query().Get("min_score"))

	byEgress := map[string]CompositeRanked{}

	globalMu.RLock()
	defer globalMu.RUnlock()

	processedSids := map[string]bool{}
	for _, prov := range globalProviders {
		for _, sid := range prov.SessionIDs {
			processedSids[sid] = true
			s := globalSessions[sid]
			if s == nil {
				continue
			}
			a := s.Analytics
			if a == nil {
				a = computeSessionAnalytics(s)
			}
			if a == nil {
				continue
			}
			for _, item := range a.CompositeRanked {
				if item.CompositeScore < minScore {
					continue
				}
				key := item.EgressIP
				if key == "" {
					key = "__" + item.Proxy.Host + ":" + strconv.Itoa(item.Proxy.Port)
				}
				if existing, ok := byEgress[key]; !ok || item.CompositeScore > existing.CompositeScore {
					byEgress[key] = item
				}
			}
		}
	}
	for _, s := range globalSessions {
		if processedSids[s.SessionID] {
			continue
		}
		a := s.Analytics
		if a == nil {
			a = computeSessionAnalytics(s)
		}
		if a == nil {
			continue
		}
		for _, item := range a.CompositeRanked {
			if item.CompositeScore < minScore {
				continue
			}
			key := item.EgressIP
			if key == "" {
				key = "__" + item.Proxy.Host + ":" + strconv.Itoa(item.Proxy.Port)
			}
			if existing, ok := byEgress[key]; !ok || item.CompositeScore > existing.CompositeScore {
				byEgress[key] = item
			}
		}
	}

	var sorted []CompositeRanked
	for _, item := range byEgress {
		sorted = append(sorted, item)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CompositeScore > sorted[j].CompositeScore
	})
	if topN > len(sorted) {
		topN = len(sorted)
	}
	sorted = sorted[:topN]

	var lines []string
	for _, item := range sorted {
		lines = append(lines, ProxyLine(&item.Proxy))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(strings.Join(lines, "\n")))
}

func handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		globalMu.RLock()
		var list []any
		for _, prov := range globalProviders {
			var sess []*Session
			for _, sid := range prov.SessionIDs {
				if s := globalSessions[sid]; s != nil {
					sess = append(sess, s)
				}
			}
			var last *Session
			if len(sess) > 0 {
				last = sess[len(sess)-1]
			}
			var qs, rp any
			var avgMs any
			if last != nil && last.IpAnalysis != nil {
				qs = last.IpAnalysis.QualityScore
				rp = last.IpAnalysis.ResidentialPct
				if last.Analytics != nil && len(last.Analytics.ISPSpeeds) > 0 {
					tot, cnt := 0, 0
					for _, sp := range last.Analytics.ISPSpeeds {
						tot += sp.AvgMs * sp.Count
						cnt += sp.Count
					}
					if cnt > 0 {
						avgMs = tot / cnt
					}
				}
			}
			lrc := 0
			if last != nil {
				lrc = last.RunCount
			}
			list = append(list, map[string]any{
				"id": prov.ID, "name": prov.Name,
				"proxy_count": len(prov.Proxies), "session_count": len(prov.SessionIDs),
				"last_run_count": lrc, "last_quality_score": qs,
				"last_residential_pct": rp, "last_avg_ms": avgMs,
				"created_at": prov.CreatedAt, "updated_at": prov.UpdatedAt,
			})
		}
		globalMu.RUnlock()
		if list == nil {
			list = []any{}
		}
		writeJSON(w, 200, list)

	case "POST":
		fields, err := parseMultipartForm(r)
		if err != nil {
			errJSON(w, 400, err.Error())
			return
		}
		fileData, ok := fields["file"]
		if !ok {
			errJSON(w, 400, "No file field")
			return
		}
		proxies := ParseProxies(string(fileData))
		if len(proxies) == 0 {
			errJSON(w, 400, "No valid proxies found")
			return
		}
		fname := fieldStr(fields, "filename", "")
		name := fieldStr(fields, "provider_name", fname)
		if name == "" {
			name = "Provider"
		}

		globalMu.Lock()
		// Upsert: if a provider with the same name already exists, update it in
		// place so repeated saves of the same list don't create duplicates.
		var existProv *Provider
		for _, p := range globalProviders {
			if p.Name == name {
				existProv = p
				break
			}
		}
		if existProv != nil {
			existProv.InputText = string(fileData)
			existProv.Proxies = proxies
			existProv.ProxyCount = len(proxies)
			existProv.UpdatedAt = time.Now().Format(time.RFC3339)
			saveProviders(globalProviders)
			globalMu.Unlock()
			writeJSON(w, 200, map[string]any{"id": existProv.ID, "name": name, "proxy_count": len(proxies)})
			return
		}
		pid := generateID()
		prov := &Provider{
			ID: pid, Name: name,
			InputText:  string(fileData),
			Proxies:    proxies,
			SessionIDs: []string{},
			ProxyCount: len(proxies),
			CreatedAt:  time.Now().Format(time.RFC3339),
			UpdatedAt:  time.Now().Format(time.RFC3339),
		}
		globalProviders[pid] = prov
		saveProviders(globalProviders)
		globalMu.Unlock()
		writeJSON(w, 200, map[string]any{"id": pid, "name": name, "proxy_count": len(proxies)})

	default:
		w.WriteHeader(405)
	}
}

func handleProviderSub(w http.ResponseWriter, r *http.Request) {
	// Skip autopick (handled separately)
	path := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	if path == "autopick" {
		handleProvidersAutopick(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	pid := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = "/" + parts[1]
	}

	globalMu.RLock()
	prov := globalProviders[pid]
	globalMu.RUnlock()
	if prov == nil {
		errJSON(w, 404, "Provider not found")
		return
	}

	switch {
	case sub == "" && r.Method == "GET":
		globalMu.RLock()
		var sessList []any
		for _, sid := range prov.SessionIDs {
			if s := globalSessions[sid]; s != nil {
				var qs, rp any
				if s.IpAnalysis != nil {
					qs = s.IpAnalysis.QualityScore
					rp = s.IpAnalysis.ResidentialPct
				}
				sessList = append(sessList, map[string]any{
					"session_id": s.SessionID, "name": s.Name, "run_count": s.RunCount,
					"status": s.Status, "updated_at": s.UpdatedAt,
					"analyzed_count": len(s.Analyzed),
					"quality_score": qs, "residential_pct": rp,
				})
			}
		}
		globalMu.RUnlock()
		writeJSON(w, 200, map[string]any{
			"id": prov.ID, "name": prov.Name,
			"proxy_count": len(prov.Proxies), "session_ids": prov.SessionIDs,
			"sessions": sessList,
			"created_at": prov.CreatedAt, "updated_at": prov.UpdatedAt,
		})

	case sub == "" && r.Method == "PUT":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		if name, ok := data["name"].(string); ok && strings.TrimSpace(name) != "" {
			prov.Name = strings.TrimSpace(name)
			prov.UpdatedAt = time.Now().Format(time.RFC3339)
		}
		writeJSON(w, 200, map[string]string{"id": prov.ID, "name": prov.Name})

	case sub == "/run" && r.Method == "POST":
		body, _ := io.ReadAll(r.Body)
		var ov map[string]any
		json.Unmarshal(body, &ov)

		config := &JobConfig{
			TestURL:     strVal(ov, "test_url", IpApiURL),
			TargetURL:   strings.TrimSpace(strVal(ov, "target_url", "")),
			Concurrency: intVal(ov, "concurrency", 150),
			Timeout:     floatVal(ov, "timeout", 10),
			Retries:     intVal(ov, "retries", 1),
			TopN:        intVal(ov, "top_n", 1000),
		}
		if v, ok := ov["target_urls"]; ok {
			if data, err := json.Marshal(v); err == nil {
				json.Unmarshal(data, &config.TargetURLs)
			}
		}

		sid := generateID()
		runNum := len(prov.SessionIDs) + 1
		sess := &Session{
			SessionID: sid, Status: "running",
			Name:      fmt.Sprintf("%s — Test %d", prov.Name, runNum),
			RunCount: 0, RunIDs: []string{},
			Config: &SessionConfig{
				TestURL:     config.TestURL,
				TargetURL:   config.TargetURL,
				TargetURLs:  config.TargetURLs,
				Concurrency: config.Concurrency,
				Timeout:     config.Timeout,
				Retries:     config.Retries,
				TopN:        config.TopN,
			},
			Proxies:      prov.Proxies,
			ProxyHistory: map[string]*ProxyHistoryEntry{},
			Analyzed:     []AnalyzedProxy{},
			BestProxies:  []AnalyzedProxy{},
			CreatedAt:    time.Now().Format(time.RFC3339),
			UpdatedAt:    time.Now().Format(time.RFC3339),
		}
		jobID := generateJobID()
		job := &Job{
			JobID: jobID, Status: "queued", SessionID: sid,
			Total: len(prov.Proxies),
			ListName: sess.Name + " — Run 1",
			Config: config, TopProxies: []ProxyResult{},
		}

		globalMu.Lock()
		globalSessions[sid] = sess
		prov.SessionIDs = append(prov.SessionIDs, sid)
		prov.UpdatedAt = time.Now().Format(time.RFC3339)
		globalJobs[jobID] = job
		globalMu.Unlock()

		proxiesCopy := make([]Proxy, len(prov.Proxies))
		copy(proxiesCopy, prov.Proxies)
		go func() {
			RunJob(jobID, proxiesCopy, config)
			sess.Status = "idle"
		}()

		writeJSON(w, 200, map[string]any{
			"session_id": sid, "job_id": jobID, "proxy_count": len(prov.Proxies),
		})

	case r.Method == "DELETE":
		globalMu.Lock()
		for _, sid := range prov.SessionIDs {
			if s := globalSessions[sid]; s != nil {
				for _, jid := range s.RunIDs {
					delete(globalJobs, jid)
				}
				delete(globalSessions, sid)
			}
		}
		delete(globalProviders, pid)
		saveProviders(globalProviders)
		globalMu.Unlock()
		writeJSON(w, 200, map[string]string{"deleted": pid})

	default:
		w.WriteHeader(405)
	}
}

// ── Schedules ─────────────────────────────────────────────────────────────────

func handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		scheds := loadSchedules()
		var list []any
		for _, s := range scheds {
			s2 := s
			s2.ProxyText = ""
			list = append(list, s2)
		}
		if list == nil {
			list = []any{}
		}
		writeJSON(w, 200, list)

	case "POST":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		proxyText := strVal(data, "proxy_text", "")
		proxies := ParseProxies(proxyText)

		var targetURLs []TargetEndpoint
		if v, ok := data["target_urls"]; ok {
			if d, err := json.Marshal(v); err == nil {
				json.Unmarshal(d, &targetURLs)
			}
		}

		fireAt := strVal(data, "fire_at", time.Now().Format(time.RFC3339))
		sched := Schedule{
			ID:            "sched_" + fmt.Sprintf("%d", time.Now().UnixMilli()),
			Name:          strVal(data, "name", "Scheduled Test"),
			Type:          strVal(data, "type", "once"),
			FireAt:        fireAt,
			IntervalHours: floatVal(data, "interval_hours", 1),
			NextFire:      fireAt,
			ProxyText:     proxyText,
			ProxyCount:    len(proxies),
			ProxyType:     strVal(data, "proxy_type", "residential"),
			TargetURL:     strVal(data, "target_url", ""),
			TargetURLs:    targetURLs,
			Preset:        strVal(data, "preset", "antibot"),
			Concurrency:   intVal(data, "concurrency", 50),
			Timeout:       floatVal(data, "timeout", 10),
			Retries:       intVal(data, "retries", 1),
			TopN:          intVal(data, "top_n", 1000),
			DiscordWebhook: strVal(data, "discord_webhook", ""),
			Status:        "pending",
			CreatedAt:     time.Now().Format(time.RFC3339),
		}

		schedulesMu.Lock()
		loadSchedules()
		schedulesList = append(schedulesList, sched)
		saveSchedules()
		schedulesMu.Unlock()

		s2 := sched
		s2.ProxyText = ""
		writeJSON(w, 201, s2)

	default:
		w.WriteHeader(405)
	}
}

func handleScheduleSub(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/schedules/")

	schedulesMu.Lock()
	defer schedulesMu.Unlock()
	loadSchedules()

	idx := -1
	for i, s := range schedulesList {
		if s.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		errJSON(w, 404, "Schedule not found")
		return
	}

	switch r.Method {
	case "PUT":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		s := &schedulesList[idx]
		if v := strVal(data, "name", ""); v != "" {
			s.Name = v
		}
		if v := strVal(data, "status", ""); v != "" {
			s.Status = v
		}
		if v := strVal(data, "fire_at", ""); v != "" {
			s.FireAt = v
		}
		saveSchedules()
		s2 := *s
		s2.ProxyText = ""
		writeJSON(w, 200, s2)

	case "DELETE":
		schedulesList = append(schedulesList[:idx], schedulesList[idx+1:]...)
		saveSchedules()
		writeJSON(w, 200, map[string]string{"deleted": id})

	default:
		w.WriteHeader(405)
	}
}

// ── Integrations ──────────────────────────────────────────────────────────────

func handleIntegrations(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(204)
	case "GET":
		writeJSON(w, 200, loadIntegrations())
	case "POST":
		var data []Integration
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			errJSON(w, 400, "invalid JSON")
			return
		}
		if data == nil {
			data = []Integration{}
		}
		saveIntegrations(data)
		writeJSON(w, 200, data)
	default:
		errJSON(w, 405, "method not allowed")
	}
}

// ── Saved proxy lists ─────────────────────────────────────────────────────────

func handleLists(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	switch r.Method {
	case "OPTIONS":
		w.WriteHeader(204)
	case "GET":
		writeJSON(w, 200, loadProxyLists())
	case "POST":
		var data map[string]ProxyList
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			errJSON(w, 400, "invalid JSON")
			return
		}
		if data == nil {
			data = map[string]ProxyList{}
		}
		saveProxyLists(data)
		writeJSON(w, 200, data)
	default:
		errJSON(w, 405, "method not allowed")
	}
}

// ── Score config ──────────────────────────────────────────────────────────────

func handleScoreConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		weights := loadScoreWeights()
		writeJSON(w, 200, map[string]any{
			"weights": weights, "defaults": DefaultScoreWeights,
		})
	case "POST":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		weights := loadScoreWeights()
		if wv, ok := data["weights"].(map[string]any); ok {
			if v := intVal(wv, "speed", -1); v >= 0 {
				weights.Speed = v
			}
			if v := intVal(wv, "reliability", -1); v >= 0 {
				weights.Reliability = v
			}
			if v := intVal(wv, "target", -1); v >= 0 {
				weights.Target = v
			}
			if v := intVal(wv, "ip_type", -1); v >= 0 {
				weights.IpType = v
			}
			if v := intVal(wv, "anti_bot", -1); v >= 0 {
				weights.AntiBot = v
			}
		}
		if reset, _ := data["reset"].(bool); reset {
			weights = DefaultScoreWeights
		}
		saveScoreConfig(ScoreConfig{Weights: weights})
		writeJSON(w, 200, map[string]any{
			"ok": true, "weights": weights,
		})
	}
}

// ── Notify config ─────────────────────────────────────────────────────────────

func handleNotifyConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, 200, loadNotifyConfig())
	case "POST":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		cfg := loadNotifyConfig()

		strField := func(key string) (string, bool) {
			v, ok := data[key].(string)
			return v, ok
		}
		boolField := func(key string, def bool) bool {
			if v, ok := data[key].(bool); ok {
				return v
			}
			return def
		}

		if v, ok := strField("discord_webhook"); ok {
			cfg.DiscordWebhook = v
		}
		if v, ok := strField("webhook_px_changes"); ok {
			cfg.WebhookPXChanges = v
		}
		if v, ok := strField("webhook_job_complete"); ok {
			cfg.WebhookJobComplete = v
		}
		if v, ok := strField("webhook_provider_issues"); ok {
			cfg.WebhookProviderIssues = v
		}
		if v, ok := strField("webhook_system_alerts"); ok {
			cfg.WebhookSystemAlerts = v
		}
		if v, ok := strField("webhook_drop_test"); ok {
			cfg.WebhookDropTest = v
		}
		if v, ok := strField("webhook_drop_px"); ok {
			cfg.WebhookDropPX = v
		}
		cfg.EnablePXChanges = boolField("enable_px_changes", cfg.EnablePXChanges)
		cfg.EnableJobComplete = boolField("enable_job_complete", cfg.EnableJobComplete)
		cfg.EnableProviderIssues = boolField("enable_provider_issues", cfg.EnableProviderIssues)
		cfg.EnableSystemAlerts = boolField("enable_system_alerts", cfg.EnableSystemAlerts)
		cfg.EnableDropTest = boolField("enable_drop_test", cfg.EnableDropTest)
		cfg.EnableDropPX = boolField("enable_drop_px", cfg.EnableDropPX)

		saveNotifyConfig(cfg)

		// Test: send to the specified type's webhook (or global fallback)
		if test, _ := data["test"].(bool); test {
			testType, _ := data["test_type"].(string)
			var wh string
			switch testType {
			case "px_changes":
				wh = cfg.WebhookPXChanges
			case "job_complete":
				wh = cfg.WebhookJobComplete
			case "provider_issues":
				wh = cfg.WebhookProviderIssues
			case "system_alerts":
				wh = cfg.WebhookSystemAlerts
			default:
				wh = cfg.DiscordWebhook
			}
			if wh == "" {
				wh = cfg.DiscordWebhook
			}
			if wh != "" {
				go sendDiscordNotification(wh, &Job{Total: 0}, "✅ Webhook test — connected!")
			}
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// ── Analytics config ──────────────────────────────────────────────────────────

func handleAnalyticsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		writeJSON(w, 200, loadAnalyticsConfig())
	case "POST":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)
		cfg := loadAnalyticsConfig()
		if v, ok := data["opt_in"].(bool); ok {
			cfg.OptIn = v
		}
		saveAnalyticsConfig(cfg)
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

// ── Community stats (proxy to CF worker) ─────────────────────────────────────

func handleCommunityStats(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(AnalyticsURL + "/stats")
	if err != nil {
		errJSON(w, 503, "Stats unavailable")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// ── Multipart form parsing ────────────────────────────────────────────────────

func parseMultipartForm(r *http.Request) (map[string][]byte, error) {
	ct := r.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || mt != "multipart/form-data" {
		return nil, fmt.Errorf("bad content-type")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("no boundary")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	mr := multipart.NewReader(strings.NewReader(string(body)), boundary)
	fields := map[string][]byte{}
	fileFields := map[string]string{} // tracks original filenames

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		name := part.FormName()
		if name == "" {
			continue
		}
		data, _ := io.ReadAll(part)
		fields[name] = data
		if fn := part.FileName(); fn != "" {
			fileFields[name] = fn
		}
		part.Close()
	}

	// Store filenames as separate fields
	for name, fn := range fileFields {
		if _, ok := fields["filename"]; !ok && name == "file" {
			fields["filename"] = []byte(fn)
		}
	}

	return fields, nil
}

func fieldStr(fields map[string][]byte, key, def string) string {
	if v, ok := fields[key]; ok {
		s := strings.TrimSpace(string(v))
		if s != "" {
			return s
		}
	}
	return def
}

func fieldInt(fields map[string][]byte, key string, def int) int {
	if v, ok := fields[key]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(string(v))); err == nil {
			return n
		}
	}
	return def
}

func fieldFloat(fields map[string][]byte, key string, def float64) float64 {
	if v, ok := fields[key]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64); err == nil {
			return f
		}
	}
	return def
}

func strVal(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func intVal(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func floatVal(m map[string]any, key string, def float64) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// ── Cloud sync / auth handlers ────────────────────────────────────────────────

// handleCloudValidateKey — POST /api/cloud/validate-key
// Accepts a Whop license key, validates against Whop API, stores session token.
func handleCloudValidateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	var body struct {
		LicenseKey string `json:"license_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" {
		errJSON(w, 400, "license_key required")
		return
	}
	auth, err := CloudValidateKey(body.LicenseKey)
	if err != nil {
		errJSON(w, 401, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":           true,
		"license_hint": auth.LicenseHint,
		"plan":         auth.Plan,
		"user_id":      auth.UserID,
	})
}

// handleCloudVerify — GET /api/cloud/verify
// Validates the stored session token against the live backend.
// Returns valid:true on success or when the backend is unreachable (offline mode,
// trusts the local session). Returns valid:false only when there is no local
// token OR the backend explicitly rejects it (401/403).
func handleCloudVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		errJSON(w, 405, "method not allowed")
		return
	}
	local := GetCloudAuth()
	if local == nil {
		writeJSON(w, 200, map[string]any{"valid": false, "reason": "not_logged_in"})
		return
	}

	// Use a short timeout — we'd rather fall back to offline mode fast than block startup.
	b, status, err := cloudGet("/auth/me", local.Token, 5)
	if err != nil {
		// Backend unreachable/slow — trust local session (offline mode)
		writeJSON(w, 200, map[string]any{
			"valid": true, "offline": true,
			"license_hint": local.LicenseHint, "plan": local.Plan, "user_id": local.UserID,
		})
		return
	}
	if status == 401 || status == 403 {
		// Token explicitly revoked/invalid — clear and force re-auth
		clearCloudAuth()
		writeJSON(w, 200, map[string]any{"valid": false, "reason": "token_invalid"})
		return
	}
	if status != 200 {
		// Backend error — trust local session
		writeJSON(w, 200, map[string]any{
			"valid": true, "offline": true,
			"license_hint": local.LicenseHint, "plan": local.Plan, "user_id": local.UserID,
		})
		return
	}

	// Token confirmed valid — refresh local cache from /auth/me response
	var me struct {
		ID          string `json:"id"`
		LicenseHint string `json:"license_hint"`
		Plan        string `json:"plan"`
	}
	json.Unmarshal(b, &me)
	cloudMu.Lock()
	if cloudAuth != nil {
		cloudAuth.LicenseHint = me.LicenseHint
		cloudAuth.Plan        = me.Plan
	}
	snapshot := cloudAuth
	cloudMu.Unlock()
	if snapshot != nil {
		saveCloudAuth(snapshot)
	}

	writeJSON(w, 200, map[string]any{
		"valid":        true,
		"license_hint": me.LicenseHint,
		"plan":         me.Plan,
		"user_id":      local.UserID,
	})
}

func handleCloudLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	CloudLogout()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleCloudStatus(w http.ResponseWriter, r *http.Request) {
	auth := GetCloudAuth()
	if auth == nil {
		writeJSON(w, 200, map[string]any{"logged_in": false})
		return
	}
	writeJSON(w, 200, map[string]any{
		"logged_in":    true,
		"license_hint": auth.LicenseHint,
		"plan":         auth.Plan,
		"user_id":      auth.UserID,
	})
}

// handleCloudResults proxies GET /api/cloud/results to the CF backend (paginated list).
func handleCloudResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		errJSON(w, 405, "method not allowed")
		return
	}
	auth := GetCloudAuth()
	if auth == nil {
		errJSON(w, 401, "not logged in")
		return
	}
	query := ""
	if q := r.URL.RawQuery; q != "" {
		query = "?" + q
	}
	b, status, err := cloudGet("/results"+query, auth.Token)
	if err != nil {
		errJSON(w, 502, "cloud unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// handleCloudResultSub proxies GET/DELETE /api/cloud/results/:id to the CF backend.
func handleCloudResultSub(w http.ResponseWriter, r *http.Request) {
	auth := GetCloudAuth()
	if auth == nil {
		errJSON(w, 401, "not logged in")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/cloud/results/")
	if id == "" {
		errJSON(w, 400, "missing result id")
		return
	}
	var (
		b      []byte
		status int
		err    error
	)
	switch r.Method {
	case "GET":
		b, status, err = cloudGet("/results/"+id, auth.Token)
	case "DELETE":
		req, _ := http.NewRequest("DELETE", CloudBackendURL+"/results/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+auth.Token)
		client := &http.Client{Timeout: 15 * time.Second}
		resp, rerr := client.Do(req)
		if rerr != nil {
			errJSON(w, 502, "cloud unavailable")
			return
		}
		defer resp.Body.Close()
		b, _ = io.ReadAll(resp.Body)
		status = resp.StatusCode
	default:
		errJSON(w, 405, "method not allowed")
		return
	}
	if err != nil {
		errJSON(w, 502, "cloud unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
}

// ── PX Monitor ────────────────────────────────────────────────────────────────

func handlePXStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		errJSON(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, map[string]any{
		"sites":      GetPXStatuses(),
		"interval_m": int(pxInterval.Minutes()),
		"site_count": len(pxSites),
	})
}

func handlePXCheckNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	var body struct {
		SiteID   string `json:"site_id"`
		ProxyURL string `json:"proxy_url"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	TriggerPXCheck(body.SiteID, body.ProxyURL)
	writeJSON(w, 200, map[string]string{"status": "check triggered"})
}

// handlePXRefreshConfig re-fetches the admin-managed site list from the cloud
// and returns the updated status snapshot. Called from the Monitor tab.
func handlePXRefreshConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	fetchRemotePXConfig()
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"site_count": len(pxSites),
		"sites":      GetPXStatuses(),
		"interval_m": int(pxInterval.Minutes()),
	})
}

// ── User preferences (cloud proxy) ───────────────────────────────────────────

func handlePreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		prefs, err := CloudGetPreferences()
		if err != nil {
			errJSON(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, prefs)
	case "POST":
		var prefs UserPreferences
		if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
			errJSON(w, 400, "invalid JSON")
			return
		}
		if err := CloudSetPreferences(prefs); err != nil {
			errJSON(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		errJSON(w, 405, "method not allowed")
	}
}

// ── Device transfer (cloud proxy) ────────────────────────────────────────────

// handleCloudTransferDevice — POST /api/cloud/transfer-device
// Binds the license key to this device, replacing any previous binding.
func handleCloudTransferDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	var body struct {
		LicenseKey string `json:"license_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LicenseKey == "" {
		errJSON(w, 400, "license_key required")
		return
	}
	auth, err := CloudTransferDevice(body.LicenseKey)
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":           true,
		"license_hint": auth.LicenseHint,
		"plan":         auth.Plan,
		"user_id":      auth.UserID,
	})
}

// ── Anonymous suggestions (cloud proxy) ──────────────────────────────────────

func handleSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		errJSON(w, 405, "method not allowed")
		return
	}
	var body struct {
		Body     string `json:"body"`
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
		errJSON(w, 400, "body required")
		return
	}
	if err := CloudSendSuggestion(body.Body, body.Category); err != nil {
		errJSON(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── Drop Scheduler ────────────────────────────────────────────────────────────

func handleDrops(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		drops := loadDrops()
		var list []any
		for _, d := range drops {
			listsNoText := make([]map[string]any, len(d.ProxyLists))
			for i, l := range d.ProxyLists {
				listsNoText[i] = map[string]any{
					"id": l.ID, "name": l.Name, "proxy_count": l.ProxyCount,
				}
			}
			list = append(list, map[string]any{
				"id": d.ID, "name": d.Name, "status": d.Status,
				"proxy_lists":   listsNoText,
				"recurring_min": d.RecurringMin,
				"jitter_min":    d.JitterMin,
				"stagger_min":   d.StaggerMin,
				"pending_times": d.PendingTimes,
				"next_fire":     d.NextFire,
				"last_fired":    d.LastFired,
				"webhook_on_test": d.WebhookOnTest,
				"webhook_on_px":   d.WebhookOnPX,
				"created_at":    d.CreatedAt,
			})
		}
		if list == nil {
			list = []any{}
		}
		writeJSON(w, 200, list)

	case "POST":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		var proxyLists []DropProxyList
		if v, ok := data["proxy_lists"]; ok {
			if d, err := json.Marshal(v); err == nil {
				json.Unmarshal(d, &proxyLists)
			}
		}
		for i := range proxyLists {
			if proxyLists[i].ID == "" {
				proxyLists[i].ID = generateID()
			}
			proxyLists[i].ProxyCount = len(ParseProxies(proxyLists[i].ProxyText))
		}

		var pendingTimes []string
		if v, ok := data["pending_times"]; ok {
			if d, err := json.Marshal(v); err == nil {
				json.Unmarshal(d, &pendingTimes)
			}
		}

		now := time.Now()
		drop := DropSchedule{
			ID:            "drop_" + fmt.Sprintf("%d", now.UnixMilli()),
			Name:          strVal(data, "name", "Drop Test"),
			ProxyLists:    proxyLists,
			PendingTimes:  pendingTimes,
			RecurringMin:  intVal(data, "recurring_min", 0),
			JitterMin:     intVal(data, "jitter_min", 0),
			StaggerMin:    intVal(data, "stagger_min", 0),
			WebhookOnTest: strVal(data, "webhook_on_test", ""),
			WebhookOnPX:   strVal(data, "webhook_on_px", ""),
			Status:        "active",
			CreatedAt:     now.Format(time.RFC3339),
		}

		dropsMu.Lock()
		loadDrops()
		drop.NextFire = computeDropNextFire(&drop, now)
		dropsList = append(dropsList, drop)
		saveDrops()
		dropsMu.Unlock()

		d2 := drop
		for i := range d2.ProxyLists {
			d2.ProxyLists[i].ProxyText = ""
		}
		writeJSON(w, 201, d2)

	default:
		w.WriteHeader(405)
	}
}

func handleDropSub(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/drops/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = "/" + parts[1]
	}

	dropsMu.Lock()
	loadDrops()
	idx := -1
	for i, d := range dropsList {
		if d.ID == id {
			idx = i
			break
		}
	}
	dropsMu.Unlock()

	if idx == -1 {
		errJSON(w, 404, "Drop not found")
		return
	}

	switch {
	case sub == "/runs" && r.Method == "GET":
		runs := loadDropRuns(id)
		if runs == nil {
			runs = []DropRun{}
		}
		writeJSON(w, 200, runs)

	case sub == "/fire" && r.Method == "POST":
		dropsMu.Lock()
		d := dropsList[idx]
		dropsMu.Unlock()
		go executeDrop(&d)
		writeJSON(w, 200, map[string]string{"status": "fired"})

	case sub == "" && r.Method == "PUT":
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		dropsMu.Lock()
		d := &dropsList[idx]
		if v := strVal(data, "status", ""); v != "" {
			d.Status = v
		}
		if v := strVal(data, "name", ""); v != "" {
			d.Name = v
		}
		if v := strVal(data, "webhook_on_test", ""); v != "" {
			d.WebhookOnTest = v
		}
		if v := strVal(data, "webhook_on_px", ""); v != "" {
			d.WebhookOnPX = v
		}
		if v, ok := data["recurring_min"].(float64); ok {
			d.RecurringMin = int(v)
		}
		if v, ok := data["jitter_min"].(float64); ok {
			d.JitterMin = int(v)
		}
		if v, ok := data["stagger_min"].(float64); ok {
			d.StaggerMin = int(v)
		}
		if v, ok := data["pending_times"]; ok {
			if bytes, err := json.Marshal(v); err == nil {
				var times []string
				json.Unmarshal(bytes, &times)
				d.PendingTimes = times
			}
		}
		d.NextFire = computeDropNextFire(d, time.Now())
		saveDrops()
		d2 := *d
		for i := range d2.ProxyLists {
			d2.ProxyLists[i].ProxyText = ""
		}
		dropsMu.Unlock()
		writeJSON(w, 200, d2)

	case r.Method == "DELETE":
		dropsMu.Lock()
		dropsList = append(dropsList[:idx], dropsList[idx+1:]...)
		saveDrops()
		dropsMu.Unlock()
		writeJSON(w, 200, map[string]string{"deleted": id})

	default:
		w.WriteHeader(405)
	}
}
