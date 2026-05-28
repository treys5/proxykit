package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const AnalyticsURL = "https://proxykit-analytics.proxykit.workers.dev"

// ── Analytics reporting ───────────────────────────────────────────────────────

func reportAnalytics(job *Job, results []ProxyResult) {
	cfg := loadAnalyticsConfig()
	if !cfg.OptIn {
		return
	}

	// Use the Whop user_id as the stable identifier when logged in so
	// analytics events can be correlated with a real membership in the
	// admin portal. Fall back to the local random UUID when offline.
	clientID := getOrCreateClientID()
	if auth := GetCloudAuth(); auth != nil && auth.UserID != "" {
		clientID = auth.UserID
	}

	var passing []ProxyResult
	for _, r := range results {
		if r.Status == "pass" {
			passing = append(passing, r)
		}
	}

	avgMs := 0
	if len(passing) > 0 {
		sum := 0
		for _, r := range passing {
			if r.AvgMs != nil {
				sum += *r.AvgMs
			}
		}
		avgMs = sum / len(passing)
	}

	typeCounts := map[string]int{"residential": 0, "mobile": 0, "datacenter": 0, "unknown": 0}
	for _, r := range results {
		t := r.IpType
		if _, ok := typeCounts[t]; ok {
			typeCounts[t]++
		}
	}

	ispMap := map[string]int{}
	for _, r := range results {
		if r.IpInfo != nil {
			isp := r.IpInfo.ISP
			if isp == "" {
				isp = r.IpInfo.Org
			}
			if isp != "" && len(isp) <= 80 {
				ispMap[isp]++
			}
		}
	}
	type ispEntry struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var topISPs []ispEntry
	for name, count := range ispMap {
		topISPs = append(topISPs, ispEntry{Name: name, Count: count})
	}
	if len(topISPs) > 10 {
		topISPs = topISPs[:10]
	}

	countryMap := map[string]int{}
	for _, r := range results {
		if r.IpInfo != nil && r.IpInfo.CountryCode != "" {
			countryMap[r.IpInfo.CountryCode]++
		}
	}

	job.mu.Lock()
	du := job.DataUsage
	tested := job.Tested
	passed := job.Passed
	job.mu.Unlock()

	hasTarget := false
	var targetPassPct *float64
	if du != nil {
		hasTarget = du.TargetTested > 0
		targetPassPct = du.TargetPassPct
	}

	payload := map[string]any{
		"client_id":           clientID,
		"app_version":         AppVersion,
		"proxies_tested":      tested,
		"proxies_passed":      passed,
		"avg_ms":              avgMs,
		"has_target":          hasTarget,
		"target_pass_rate":    targetPassPct,
		"ip_type_residential": typeCounts["residential"],
		"ip_type_mobile":      typeCounts["mobile"],
		"ip_type_datacenter":  typeCounts["datacenter"],
		"ip_type_unknown":     typeCounts["unknown"],
		"top_isps":            topISPs,
		"vendor_counts":       map[string]int{},
		"status_counts":       map[string]int{},
		"country_counts":      countryMap,
	}
	if du != nil {
		payload["vendor_counts"] = du.VendorCounts
		payload["status_counts"] = du.StatusCounts
	}

	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", AnalyticsURL+"/ingest", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// ── Discord notification ───────────────────────────────────────────────────────

func sendDiscordNotification(webhookURL string, job *Job, name string) {
	if webhookURL == "" || !strings.HasPrefix(webhookURL, "https://discord.com/api/webhooks/") {
		return
	}

	job.mu.Lock()
	total := job.Total
	passed := job.Passed
	du := job.DataUsage
	topProxies := job.TopProxies
	job.mu.Unlock()

	passRate := 0
	if total > 0 {
		passRate = passed * 100 / total
	}

	var pxPct *float64
	var tgtPct *float64
	vendorCounts := map[string]int{}
	if du != nil {
		if du.PxChallengePct > 0 {
			pxPct = &du.PxChallengePct
		}
		tgtPct = du.TargetPassPct
		vendorCounts = du.VendorCounts
	}

	avgMs := 0
	if len(topProxies) > 0 {
		n := len(topProxies)
		if n > 20 {
			n = 20
		}
		sum := 0
		count := 0
		for i := 0; i < n; i++ {
			if topProxies[i].AvgMs != nil {
				sum += *topProxies[i].AvgMs
				count++
			}
		}
		if count > 0 {
			avgMs = sum / count
		}
	}

	color := 0xff5c5c
	verdict := "WEAK"
	if passRate >= 70 {
		color = 0x818cf8
		verdict = "ELITE"
	} else if passRate >= 50 {
		color = 0xff9f43
		verdict = "GOOD"
	} else if passRate >= 30 {
		color = 0xff9f43
		verdict = "WORKABLE"
	}

	type field struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}
	fields := []field{
		{Name: "Pass Rate", Value: fmt.Sprintf("%d%%", passRate), Inline: true},
		{Name: "Proxies", Value: fmt.Sprintf("%d", total), Inline: true},
		{Name: "Verdict", Value: verdict, Inline: true},
	}
	if pxPct != nil {
		fields = append(fields, field{Name: "PX Hit %", Value: fmt.Sprintf("%.1f%%", *pxPct), Inline: true})
	}
	if tgtPct != nil {
		fields = append(fields, field{Name: "Target Pass %", Value: fmt.Sprintf("%.1f%%", *tgtPct), Inline: true})
	}
	if avgMs > 0 {
		fields = append(fields, field{Name: "Avg Speed", Value: fmt.Sprintf("%dms", avgMs), Inline: true})
	}
	if len(vendorCounts) > 0 {
		var parts []string
		for k, v := range vendorCounts {
			parts = append(parts, strings.ToUpper(k)+":"+fmt.Sprintf("%d", v))
		}
		fields = append(fields, field{Name: "Bot Vendors Hit", Value: strings.Join(parts, " · "), Inline: false})
	}

	embed := map[string]any{
		"title":     fmt.Sprintf("📊 %s — Complete", name),
		"color":     color,
		"fields":    fields,
		"footer":    map[string]string{"text": "PROXY TESTER v" + AppVersion},
		"timestamp": time.Now().Format(time.RFC3339),
	}
	payload := map[string]any{"embeds": []any{embed}}
	data, _ := json.Marshal(payload)

	parsed, err := url.Parse(webhookURL)
	if err != nil {
		return
	}
	_ = parsed

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("DiscordBot (proxy-tester, %s)", AppVersion))
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// sendDiscordMessage posts a simple plain-text message to a Discord webhook.
func sendDiscordMessage(webhookURL, content string) {
	if webhookURL == "" || !strings.HasPrefix(webhookURL, "https://discord.com/api/webhooks/") {
		return
	}
	payload := map[string]any{"content": content}
	data, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", webhookURL, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", fmt.Sprintf("DiscordBot (proxy-tester, %s)", AppVersion))
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
