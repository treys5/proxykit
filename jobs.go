package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Job runner ────────────────────────────────────────────────────────────────

func RunJob(jobID string, proxies []Proxy, config *JobConfig) {
	globalMu.Lock()
	job := globalJobs[jobID]
	globalMu.Unlock()
	if job == nil {
		return
	}

	job.mu.Lock()
	job.Status = "running"
	job.startTime = time.Now().UnixMilli()
	job.vendorCounts = map[string]int{}
	job.statusCounts = map[string]int{}
	job.mu.Unlock()

	total := len(proxies)
	results := make([]ProxyResult, total)

	// Normalize target URLs
	targetURLs := config.TargetURLs
	if len(targetURLs) == 0 && config.TargetURL != "" {
		targetURLs = []TargetEndpoint{{Key: "target", URL: config.TargetURL, Label: "Target"}}
	}

	conc := config.Concurrency
	if conc > total {
		conc = total
	}
	if conc < 1 {
		conc = 1
	}

	// Channel-based work queue
	queue := make(chan int, total)
	for i := 0; i < total; i++ {
		queue <- i
	}
	close(queue)

	timeout := time.Duration(config.Timeout * float64(time.Second))
	retries := config.Retries
	if retries < 1 {
		retries = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range queue {
				r := TestProxy(proxies[idx], config.TestURL, timeout, retries, targetURLs, config.SkipHttpbin)
				results[idx] = r

				job.mu.Lock()
				job.Tested++
				completed := job.Tested
				if r.Status == "pass" {
					job.Passed++
				} else {
					job.Failed++
				}
				if r.PxChallenge {
					job.pxCount++
				}
				if r.TargetBotVendor != "" {
					job.vendorCounts[r.TargetBotVendor]++
				}
				if r.TargetStatus != nil {
					sc := statusBucket(*r.TargetStatus)
					job.statusCounts[sc]++
				}
				if r.HttpbinPass != nil {
					job.httpbinTested++
					if *r.HttpbinPass {
						job.httpbinPassed++
					}
				}
				if r.TargetPass != nil {
					job.targetTested++
					if *r.TargetPass {
						job.targetPassed++
					}
				}
				job.bytesSent += r.BytesSent
				job.bytesReceived += r.BytesReceived

				elapsed := float64(time.Now().UnixMilli()-job.startTime) / 1000.0
				job.ElapsedSec = math.Round(elapsed*10) / 10
				job.ProgressPct = math.Round(float64(completed)/float64(total)*1000) / 10
				if completed > 0 && elapsed > 0 {
					eta := int(float64(total-completed) / (float64(completed) / elapsed))
					job.EtaSec = &eta
				}
				job.mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Post-processing
	job.mu.Lock()

	totalBytes := job.bytesSent + job.bytesReceived
	avgBytesPerProxy := 0
	if total > 0 {
		avgBytesPerProxy = totalBytes / total
	}

	var httpbinPassPct *float64
	if job.httpbinTested > 0 {
		v := math.Round(float64(job.httpbinPassed)/float64(job.httpbinTested)*1000) / 10
		httpbinPassPct = &v
	}
	var targetPassPct *float64
	if job.targetTested > 0 {
		v := math.Round(float64(job.targetPassed)/float64(job.targetTested)*1000) / 10
		targetPassPct = &v
	}

	pxPct := 0.0
	if total > 0 {
		pxPct = math.Round(float64(job.pxCount)/float64(total)*1000) / 10
	}

	job.DataUsage = &DataUsage{
		BytesSent:        job.bytesSent,
		BytesReceived:    job.bytesReceived,
		TotalBytes:       totalBytes,
		AvgBytesPerProxy: avgBytesPerProxy,
		PxChallengeCount: job.pxCount,
		PxChallengePct:   pxPct,
		HttpbinTested:    job.httpbinTested,
		HttpbinPassed:    job.httpbinPassed,
		HttpbinPassPct:   httpbinPassPct,
		TargetTested:     job.targetTested,
		TargetPassed:     job.targetPassed,
		TargetPassPct:    targetPassPct,
		VendorCounts:     job.vendorCounts,
		StatusCounts:     job.statusCounts,
	}

	var passed []ProxyResult
	for _, r := range results {
		if r.Status == "pass" {
			passed = append(passed, r)
		}
	}

	// Sort: residential first, then by speed
	typeOrder := map[string]int{
		"residential": 0, "mobile": 1, "unknown": 2,
		"flagged_proxy": 3, "datacenter": 4, "private": 5,
	}
	sort.Slice(passed, func(i, j int) bool {
		oi, ok1 := typeOrder[passed[i].IpType]
		if !ok1 {
			oi = 2
		}
		oj, ok2 := typeOrder[passed[j].IpType]
		if !ok2 {
			oj = 2
		}
		if oi != oj {
			return oi < oj
		}
		mi := 9999
		if passed[i].AvgMs != nil {
			mi = *passed[i].AvgMs
		}
		mj := 9999
		if passed[j].AvgMs != nil {
			mj = *passed[j].AvgMs
		}
		return mi < mj
	})

	// Store up to 5000 passed proxies so the frontend filter (isTopProxy) has
	// enough material to select 1000 quality results even after stricter cuts.
	topN := config.TopN
	if topN <= 0 {
		topN = 5000
	}
	if topN > len(passed) {
		topN = len(passed)
	}
	job.TopProxies = passed[:topN]
	job.IpAnalysis = AnalyzeEgressIPs(passed)
	job.Status = "done"
	job.ProgressPct = 100
	zero := 0
	job.EtaSec = &zero

	job.mu.Unlock()

	// Persist calibration
	if avgBytesPerProxy > 0 {
		appendCalibrationData(CalibrationEntry{
			Date:             time.Now().Format(time.RFC3339),
			AvgBytesPerProxy: avgBytesPerProxy,
			ProxyCount:       total,
		})
	}

	// Persist result file
	type resultFile struct {
		JobID       string      `json:"job_id"`
		Config      *JobConfig  `json:"config"`
		CompletedAt string      `json:"completed_at"`
		Stats       any         `json:"stats"`
		DataUsage   *DataUsage  `json:"data_usage"`
		IpAnalysis  *IpAnalysis `json:"ip_analysis"`
		TopProxies  []ProxyResult `json:"top_proxies"`
	}
	writeResultFile(jobID+".json", resultFile{
		JobID:       jobID,
		Config:      config,
		CompletedAt: time.Now().Format(time.RFC3339),
		Stats:       map[string]int{"total": total, "passed": job.Passed, "failed": job.Failed},
		DataUsage:   job.DataUsage,
		IpAnalysis:  job.IpAnalysis,
		TopProxies:  job.TopProxies,
	})

	// Session merge
	if job.SessionID != "" {
		globalMu.Lock()
		sess := globalSessions[job.SessionID]
		globalMu.Unlock()
		if sess != nil {
			MergeRunIntoSession(sess, jobID, results)
		}
	}

	// Analytics
	go reportAnalytics(job, results)

	// Cloud sync (no-op if not logged in)
	go SyncJobResult(job, results)
}

func statusBucket(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code == 403:
		return "403"
	case code == 429:
		return "429"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}

// ── Egress IP analysis ────────────────────────────────────────────────────────

func AnalyzeEgressIPs(passed []ProxyResult) *IpAnalysis {
	ipCounts := map[string]int{}
	asnCounts := map[string]int{}
	ispCounts := map[string]int{}
	typeCounts := map[string]int{}

	var totalWithIP, datacenter, private_, flagged, residential, mobile, unknown int
	var rotating, sslInspected int

	for _, r := range passed {
		t := r.IpType
		if t == "" {
			t = "unknown"
		}
		typeCounts[t]++
		switch t {
		case "datacenter":
			datacenter++
		case "private":
			private_++
		case "flagged_proxy":
			flagged++
		case "residential":
			residential++
		case "mobile":
			mobile++
		default:
			unknown++
		}
		if r.EgressIP != "" {
			ipCounts[r.EgressIP]++
			totalWithIP++
		}
		if r.IpInfo != nil {
			if r.IpInfo.AS != "" {
				asnCounts[r.IpInfo.AS]++
			}
			isp := r.IpInfo.ISP
			if isp == "" {
				isp = r.IpInfo.Org
			}
			if isp != "" {
				ispCounts[isp]++
			}
		}
		if r.Rotating {
			rotating++
		}
		if r.SslInspected {
			sslInspected++
		}
	}

	// Crowded subnets (/24)
	subnetCounts := map[string]int{}
	for ip := range ipCounts {
		parts := strings.Split(ip, ".")
		if len(parts) >= 3 {
			subnet := strings.Join(parts[:3], ".") + ".0/24"
			subnetCounts[subnet]++
		}
	}
	var crowdedSubnets []CrowdedSubnet
	for sn, count := range subnetCounts {
		if count > 5 {
			crowdedSubnets = append(crowdedSubnets, CrowdedSubnet{Subnet: sn, Count: count})
		}
	}
	sort.Slice(crowdedSubnets, func(i, j int) bool {
		return crowdedSubnets[i].Count > crowdedSubnets[j].Count
	})
	if len(crowdedSubnets) > 20 {
		crowdedSubnets = crowdedSubnets[:20]
	}

	// Reused IPs
	var reusedIPs []ReusedIP
	for ip, count := range ipCounts {
		if count > 1 {
			reusedIPs = append(reusedIPs, ReusedIP{IP: ip, Count: count})
		}
	}
	sort.Slice(reusedIPs, func(i, j int) bool {
		return reusedIPs[i].Count > reusedIPs[j].Count
	})
	if len(reusedIPs) > 20 {
		reusedIPs = reusedIPs[:20]
	}

	// Top ASNs
	var topASNs []TopASN
	for asn, count := range asnCounts {
		topASNs = append(topASNs, TopASN{ASN: asn, Count: count})
	}
	sort.Slice(topASNs, func(i, j int) bool {
		return topASNs[i].Count > topASNs[j].Count
	})
	if len(topASNs) > 10 {
		topASNs = topASNs[:10]
	}

	// Top ISPs
	var topISPs []TopISP
	for isp, count := range ispCounts {
		topISPs = append(topISPs, TopISP{ISP: isp, Count: count})
	}
	sort.Slice(topISPs, func(i, j int) bool {
		return topISPs[i].Count > topISPs[j].Count
	})
	if len(topISPs) > 10 {
		topISPs = topISPs[:10]
	}

	uniqueIPs := len(ipCounts)
	reuseRate := 0.0
	if totalWithIP > 0 {
		reuseRate = math.Round((1-float64(uniqueIPs)/float64(totalWithIP))*1000) / 10
	}
	total := len(passed)
	resRate, dcRate := 0.0, 0.0
	if total > 0 {
		resRate = math.Round(float64(residential)/float64(total)*1000) / 10
		dcRate = math.Round(float64(datacenter)/float64(total)*1000) / 10
	}

	qualityScore := int(
		resRate*0.4 +
			math.Max(0, 100-reuseRate)*0.3 +
			math.Max(0, 100-dcRate*5)*0.2 +
			math.Min(10, float64(len(topASNs))*2),
	)

	return &IpAnalysis{
		TotalPassed:       total,
		TotalWithIP:       totalWithIP,
		UniqueIPs:         uniqueIPs,
		ISPDiversity:      len(ispCounts),
		ReuseRatePct:      reuseRate,
		ResidentialCount:  residential,
		ResidentialPct:    resRate,
		DatacenterCount:   datacenter,
		DatacenterPct:     dcRate,
		MobileCount:       mobile,
		FlaggedCount:      flagged,
		PrivateCount:      private_,
		UnknownCount:      unknown,
		QualityScore:      qualityScore,
		TopReusedIPs:      reusedIPs,
		TopASNs:           topASNs,
		TopISPs:           topISPs,
		CrowdedSubnets:    crowdedSubnets,
		RotatingCount:     rotating,
		SslInspectedCount: sslInspected,
	}
}

// ── Diversity caps ────────────────────────────────────────────────────────────

func ApplyDiversityCaps(list []ProxyResult, maxPerASN, maxPerCity int) []ProxyResult {
	if maxPerASN == 0 && maxPerCity == 0 {
		return list
	}
	asnCounts := map[string]int{}
	cityCounts := map[string]int{}
	var out []ProxyResult
	for _, p := range list {
		asn := "__uk"
		city := "__uk"
		if p.IpInfo != nil {
			if p.IpInfo.AS != "" {
				asn = p.IpInfo.AS
			}
			if p.IpInfo.City != "" && p.IpInfo.Country != "" {
				city = p.IpInfo.City + "," + p.IpInfo.Country
			}
		}
		if maxPerASN > 0 {
			asnCounts[asn]++
			if asnCounts[asn] > maxPerASN {
				continue
			}
		}
		if maxPerCity > 0 {
			cityCounts[city]++
			if cityCounts[city] > maxPerCity {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func generateID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())[:8]
}

func generateJobID() string {
	return "j" + fmt.Sprintf("%d", time.Now().UnixMilli())
}
