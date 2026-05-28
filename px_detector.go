package main

// ── In-app Bot Protection Monitor ────────────────────────────────────────────
// Checks major retail / sneaker sites for changes in bot protection level.
// Runs on a background goroutine; when status changes it reports to the backend
// which forwards Discord webhook notifications to opted-in users.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ── Site definitions ──────────────────────────────────────────────────────────

type PXSite struct {
	ID           string
	Name         string
	URL          string
	Protection   string   // human-readable protection name
	HardCodes    []int    // HTTP status codes = hard block
	BodyKeywords []string // body substrings signalling an active challenge
	HeaderKeys   []string // response header names that reveal the protection stack
}

// pxSites is the monitored site list.  Add / remove entries freely.
var pxSites = []PXSite{
	{
		ID: "nike", Name: "Nike / SNKRS", Protection: "Akamai BM",
		URL:          "https://www.nike.com/launch",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck", "ak_bmsc", "AkamaiGuard", "bm_sz"},
		HeaderKeys:   []string{"x-akamai-request-id", "x-check-cacheable"},
	},
	{
		ID: "adidas", Name: "Adidas", Protection: "Akamai BM",
		URL:          "https://www.adidas.com/us",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck", "ak_bmsc"},
		HeaderKeys:   []string{"x-akamai-request-id"},
	},
	{
		ID: "supreme", Name: "Supreme", Protection: "Shape/F5",
		URL:          "https://www.supremenewyork.com/shop/all",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"shape_utmb", "f5-scrutinizer", "_utmb"},
	},
	{
		ID: "footlocker", Name: "Foot Locker", Protection: "PerimeterX",
		URL:          "https://www.footlocker.com/",
		HardCodes:    []int{403},
		BodyKeywords: []string{"_pxhd", "_pxvid", "_px2", "perimeterx"},
		HeaderKeys:   []string{"x-px-version"},
	},
	{
		ID: "jdsports", Name: "JD Sports", Protection: "DataDome",
		URL:          "https://www.jdsports.com/",
		HardCodes:    []int{403},
		BodyKeywords: []string{"datadome", "_dd_s"},
		HeaderKeys:   []string{"x-datadome"},
	},
	{
		ID: "finishline", Name: "Finish Line", Protection: "Akamai BM",
		URL:          "https://www.finishline.com/",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck", "ak_bmsc"},
	},
	{
		ID: "yeezysupply", Name: "Yeezy Supply", Protection: "Akamai BM",
		URL:          "https://www.yeezysupply.com/",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck"},
	},
	{
		ID: "jordan", Name: "Jordan Brand", Protection: "Akamai BM",
		URL:          "https://www.jordan.com/",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck"},
	},
	{
		ID: "shopify", Name: "Shopify Checkout", Protection: "Cloudflare",
		URL:          "https://www.shopify.com/checkout",
		HardCodes:    []int{403, 503},
		BodyKeywords: []string{"cf-challenge", "__cf_bm", "cloudflare"},
	},
	{
		ID: "walmart", Name: "Walmart", Protection: "Akamai BM",
		URL:          "https://www.walmart.com/",
		HardCodes:    []int{403, 429},
		BodyKeywords: []string{"_abck", "ak_bmsc"},
	},
}

// ── Status model ──────────────────────────────────────────────────────────────

// PXStatus holds the last-known protection status for one site.
type PXStatus struct {
	SiteID      string `json:"site_id"`
	SiteName    string `json:"site_name"`
	Protection  string `json:"protection"`
	// "clean"   — site reachable, no active challenge detected
	// "soft"    — challenge script present but not blocking yet
	// "hard"    — site returned a block/ban status code
	// "unknown" — could not reach the site
	Status      string `json:"status"`
	LastChecked int64  `json:"last_checked"` // unix timestamp
	LastChanged int64  `json:"last_changed"` // unix; 0 = never changed
	Detail      string `json:"detail,omitempty"`
}

var (
	pxMu       sync.RWMutex
	pxStatuses = map[string]*PXStatus{}
	pxInterval = 10 * time.Minute
)

// ── Persistence ───────────────────────────────────────────────────────────────

func loadPXStatuses() {
	var saved map[string]*PXStatus
	if err := readJSONFile("px_status.json", &saved); err == nil && saved != nil {
		pxMu.Lock()
		pxStatuses = saved
		pxMu.Unlock()
	}
}

func savePXStatuses() {
	pxMu.RLock()
	snapshot := make(map[string]*PXStatus, len(pxStatuses))
	for k, v := range pxStatuses {
		cp := *v
		snapshot[k] = &cp
	}
	pxMu.RUnlock()
	writeJSONFile("px_status.json", snapshot)
}

// ── Public API ────────────────────────────────────────────────────────────────

// GetPXStatuses returns a snapshot of every monitored site's current status.
// Sites not yet checked get Status="unknown".
func GetPXStatuses() []PXStatus {
	pxMu.RLock()
	defer pxMu.RUnlock()
	out := make([]PXStatus, 0, len(pxSites))
	for _, site := range pxSites {
		if st, ok := pxStatuses[site.ID]; ok {
			out = append(out, *st)
		} else {
			out = append(out, PXStatus{
				SiteID:     site.ID,
				SiteName:   site.Name,
				Protection: site.Protection,
				Status:     "unknown",
			})
		}
	}
	return out
}

// TriggerPXCheck triggers an immediate check — either one site (by ID) or all sites ("").
// proxyURL is optional; pass "" for a direct connection (used by background checks).
func TriggerPXCheck(siteID, proxyURL string) {
	go func() {
		if siteID == "" {
			runPXChecks()
			return
		}
		for _, site := range pxSites {
			if site.ID == siteID {
				result := probeSite(site, proxyURL)
				pxMu.Lock()
				prev := pxStatuses[site.ID]
				result.LastChanged = inheritLastChanged(prev, result.Status)
				if prev != nil && prev.Status != result.Status {
					result.LastChanged = result.LastChecked
					go reportPXChange(result, prev.Status)
				}
				pxStatuses[site.ID] = &result
				pxMu.Unlock()
				savePXStatuses()
				break
			}
		}
	}()
}

// ── Background monitor ────────────────────────────────────────────────────────

// fetchRemotePXConfig pulls site definitions + interval from the backend and
// overrides the local defaults.  Called synchronously before the background
// goroutine starts, so no mutex is needed for pxSites.
func fetchRemotePXConfig() {
	cfg, err := CloudFetchPXConfig()
	if err != nil || cfg == nil || len(cfg.Sites) == 0 {
		return // keep hardcoded defaults on any error
	}
	newSites := make([]PXSite, 0, len(cfg.Sites))
	for _, s := range cfg.Sites {
		// Worker already queries WHERE enabled=1; skip only if explicitly disabled
		if s.ID == "" || s.URL == "" {
			continue
		}
		newSites = append(newSites, PXSite{
			ID:           s.ID,
			Name:         s.Name,
			URL:          s.URL,
			Protection:   s.Protection,
			HardCodes:    s.HardCodes,
			BodyKeywords: s.BodyKW,
			HeaderKeys:   s.HeaderKeys,
		})
	}
	if len(newSites) > 0 {
		pxSites = newSites
		if cfg.IntervalM > 0 {
			pxInterval = time.Duration(cfg.IntervalM) * time.Minute
		}
	}
}

func startPXMonitor() {
	fetchRemotePXConfig() // pull admin-managed config; keep defaults on error
	loadPXStatuses()
	go pxMonitorLoop()
}

func pxMonitorLoop() {
	// First check 45 s after startup so the app feels snappy on launch.
	time.Sleep(45 * time.Second)
	runPXChecks()

	ticker := time.NewTicker(pxInterval)
	defer ticker.Stop()
	for range ticker.C {
		runPXChecks()
	}
}

func runPXChecks() {
	for _, site := range pxSites {
		result := probeSite(site, "") // background checks always use direct connection

		pxMu.Lock()
		prev := pxStatuses[site.ID]
		changed := prev != nil && prev.Status != result.Status
		result.LastChanged = inheritLastChanged(prev, result.Status)
		if changed {
			result.LastChanged = result.LastChecked
		}
		pxStatuses[site.ID] = &result
		pxMu.Unlock()

		if changed {
			prevStatus := prev.Status // capture before goroutine
			go reportPXChange(result, prevStatus)
		}

		// Small gap between site probes to avoid burst traffic.
		time.Sleep(2 * time.Second)
	}
	savePXStatuses()
}

func inheritLastChanged(prev *PXStatus, newStatus string) int64 {
	if prev == nil {
		return 0
	}
	if prev.Status != newStatus {
		return 0 // will be overridden to LastChecked by caller
	}
	return prev.LastChanged
}

// ── Single-site probe ─────────────────────────────────────────────────────────

// probeSite probes a site and classifies its bot protection status.
// proxyURL is optional (e.g. "http://user:pass@host:port"); pass "" for direct.
func probeSite(site PXSite, proxyURL string) PXStatus {
	transport := &http.Transport{}
	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(pu)
		}
	}
	client := &http.Client{
		Timeout:   12 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", site.URL, nil)
	if err != nil {
		return unknownStatus(site, "request build error")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return unknownStatus(site, "network error")
	}
	defer resp.Body.Close()

	// Read up to 64 KB for pattern matching.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	body := strings.ToLower(string(bodyBytes))

	now := time.Now().Unix()
	status := "clean"
	detail := fmt.Sprintf("HTTP %d", resp.StatusCode)

	// Hard block: status code is a definitive ban/block.
	for _, code := range site.HardCodes {
		if resp.StatusCode == code {
			status = "hard"
			detail = fmt.Sprintf("HTTP %d — hard block", resp.StatusCode)
			goto done
		}
	}

	// Soft block: challenge script present in body.
	for _, kw := range site.BodyKeywords {
		if strings.Contains(body, strings.ToLower(kw)) {
			status = "soft"
			detail = fmt.Sprintf("HTTP %d — challenge detected (%s)", resp.StatusCode, kw)
			goto done
		}
	}

	// Protection-stack headers present (informational, still "clean").
	for _, hk := range site.HeaderKeys {
		if resp.Header.Get(hk) != "" {
			detail = fmt.Sprintf("HTTP %d — %s header present", resp.StatusCode, hk)
			break
		}
	}

done:
	return PXStatus{
		SiteID:      site.ID,
		SiteName:    site.Name,
		Protection:  site.Protection,
		Status:      status,
		LastChecked: now,
		Detail:      detail,
	}
}

func unknownStatus(site PXSite, reason string) PXStatus {
	return PXStatus{
		SiteID:      site.ID,
		SiteName:    site.Name,
		Protection:  site.Protection,
		Status:      "unknown",
		LastChecked: time.Now().Unix(),
		Detail:      reason,
	}
}

// ── Backend reporting ─────────────────────────────────────────────────────────

// reportPXChange notifies the backend of a status change and fires the local
// per-type Discord webhook if one is configured.
func reportPXChange(current PXStatus, prevStatus string) {
	// 1. Fire local webhook (PX-changes type, fallback to global)
	notifyCfg := loadNotifyConfig()
	if notifyCfg.EnablePXChanges {
		wh := notifyCfg.WebhookPXChanges
		if wh == "" {
			wh = notifyCfg.DiscordWebhook
		}
		if wh != "" {
			statusEmoji := map[string]string{
				"clean":   "✅",
				"soft":    "⚠️",
				"hard":    "🚫",
				"unknown": "❓",
			}
			emoji := statusEmoji[current.Status]
			if emoji == "" {
				emoji = "🔔"
			}
			msg := fmt.Sprintf("%s **PX STATUS CHANGE** — %s\n%s → **%s**\n%s",
				emoji, current.SiteName,
				prevStatus, current.Status, current.Detail)
			go sendDiscordMessage(wh, msg)
		}
	}

	// 2. Report to backend (fans out to all opted-in users)
	auth := GetCloudAuth()
	if auth == nil {
		return // not signed in — silently skip
	}
	payload := map[string]any{
		"site_id":    current.SiteID,
		"site_name":  current.SiteName,
		"protection": current.Protection,
		"old_status": prevStatus,
		"new_status": current.Status,
		"detail":     current.Detail,
		"changed_at": current.LastChecked,
	}
	cloudPost("/px/change", payload, auth.Token)
}
