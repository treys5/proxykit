package main

import (
	"math"
	"sort"
	"time"
)

// ── Merge a completed run into a session ──────────────────────────────────────

func MergeRunIntoSession(s *Session, jobID string, results []ProxyResult) {
	s.RunIDs = append(s.RunIDs, jobID)
	s.RunCount = len(s.RunIDs)

	if s.ProxyHistory == nil {
		s.ProxyHistory = map[string]*ProxyHistoryEntry{}
	}

	for _, r := range results {
		k := proxyKey(r.Host, r.Port, r.Username, r.Password)
		if s.ProxyHistory[k] == nil {
			s.ProxyHistory[k] = &ProxyHistoryEntry{Proxy: r}
		}
		s.ProxyHistory[k].Runs = append(s.ProxyHistory[k].Runs, ProxyRunEntry{
			RunNum:      s.RunCount,
			JobID:       jobID,
			Status:      r.Status,
			AvgMs:       r.AvgMs,
			MinMs:       r.MinMs,
			SuccessRate: r.SuccessRate,
			Score:       r.Score,
			EgressIP:    r.EgressIP,
			IpType:      r.IpType,
			IpInfo:      r.IpInfo,
		})
	}

	// Analyze proxies that passed in all (or configured minimum) runs
	var analyzed []AnalyzedProxy
	minPassRuns := s.RunCount
	if s.Config != nil && s.Config.MinPassRuns > 0 {
		minPassRuns = s.Config.MinPassRuns
	}

	for _, entry := range s.ProxyHistory {
		if len(entry.Runs) < s.RunCount {
			continue
		}
		var passRuns []ProxyRunEntry
		for _, run := range entry.Runs {
			if run.Status == "pass" {
				passRuns = append(passRuns, run)
			}
		}
		if len(passRuns) < minPassRuns {
			continue
		}

		var lats []int
		var scores []float64
		for _, r := range passRuns {
			if r.AvgMs != nil {
				lats = append(lats, *r.AvgMs)
			}
			scores = append(scores, float64(r.Score))
		}

		meanLat := 0
		if len(lats) > 0 {
			sum := 0
			for _, l := range lats {
				sum += l
			}
			meanLat = sum / len(lats)
		}

		var minLat *int
		for _, r := range passRuns {
			if r.MinMs != nil && (minLat == nil || *r.MinMs < *minLat) {
				v := *r.MinMs
				minLat = &v
			}
		}

		meanScore := 0.0
		if len(scores) > 0 {
			sum := 0.0
			for _, s := range scores {
				sum += s
			}
			meanScore = sum / float64(len(scores))
		}

		// Dominant IP type
		typeCounts := map[string]int{}
		for _, r := range passRuns {
			t := r.IpType
			if t == "" {
				t = "unknown"
			}
			typeCounts[t]++
		}
		ipType := "unknown"
		best := 0
		for t, cnt := range typeCounts {
			if cnt > best {
				best = cnt
				ipType = t
			}
		}

		var latHistory []*int
		var scoreHistory []float64
		for _, r := range entry.Runs {
			if r.AvgMs != nil {
				v := *r.AvgMs
				latHistory = append(latHistory, &v)
			} else {
				latHistory = append(latHistory, nil)
			}
			scoreHistory = append(scoreHistory, float64(r.Score))
		}

		lastPass := passRuns[len(passRuns)-1]
		analyzed = append(analyzed, AnalyzedProxy{
			Proxy:        entry.Proxy,
			RunCount:     s.RunCount,
			PassCount:    len(passRuns),
			PassPct:      int(math.Round(float64(len(passRuns)) / float64(s.RunCount) * 100)),
			MeanLat:      meanLat,
			MinLat:       minLat,
			MeanScore:    meanScore,
			IpType:       ipType,
			EgressIP:     lastPass.EgressIP,
			IpInfo:       lastPass.IpInfo,
			LatHistory:   latHistory,
			ScoreHistory: scoreHistory,
		})
	}

	// Sort
	typeOrder := map[string]int{
		"residential": 0, "mobile": 1, "unknown": 2,
		"flagged_proxy": 3, "datacenter": 4, "private": 5,
	}
	sort.Slice(analyzed, func(i, j int) bool {
		oi := typeOrder[analyzed[i].IpType]
		oj := typeOrder[analyzed[j].IpType]
		if oi != oj {
			return oi < oj
		}
		if analyzed[j].PassCount != analyzed[i].PassCount {
			return analyzed[i].PassCount > analyzed[j].PassCount
		}
		return analyzed[i].MeanLat < analyzed[j].MeanLat
	})

	s.Analyzed = analyzed
	topN := 1000
	if s.Config != nil && s.Config.TopN > 0 {
		topN = s.Config.TopN
	}
	if topN > len(analyzed) {
		topN = len(analyzed)
	}
	s.BestProxies = analyzed[:topN]
	s.Status = "idle"
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	s.Analytics = computeSessionAnalytics(s)
	if len(analyzed) > 0 {
		mapped := make([]ProxyResult, len(analyzed))
		for i, a := range analyzed {
			mapped[i] = ProxyResult{
				IpType:   a.IpType,
				EgressIP: a.EgressIP,
				IpInfo:   a.IpInfo,
				AvgMs:    &a.MeanLat,
			}
		}
		s.IpAnalysis = AnalyzeEgressIPs(mapped)
	}

	// Persist session file
	type sessionFile struct {
		SessionID   string          `json:"session_id"`
		Config      *SessionConfig  `json:"config"`
		RunCount    int             `json:"run_count"`
		RunIDs      []string        `json:"run_ids"`
		UpdatedAt   string          `json:"updated_at"`
		IpAnalysis  *IpAnalysis     `json:"ip_analysis"`
		BestProxies []AnalyzedProxy `json:"best_proxies"`
	}
	writeResultFile("session_"+s.SessionID+".json", sessionFile{
		SessionID:   s.SessionID,
		Config:      s.Config,
		RunCount:    s.RunCount,
		RunIDs:      s.RunIDs,
		UpdatedAt:   s.UpdatedAt,
		IpAnalysis:  s.IpAnalysis,
		BestProxies: s.BestProxies,
	})
}

// ── Session analytics ─────────────────────────────────────────────────────────

func computeSessionAnalytics(s *Session) *SessionAnalytics {
	history := s.ProxyHistory
	analyzed := s.Analyzed
	runCount := s.RunCount
	if runCount < 1 {
		runCount = 1
	}

	// Build egress IP → credential count
	ipToCredCount := map[string]int{}
	for _, entry := range history {
		for _, r := range entry.Runs {
			if r.Status == "pass" && r.EgressIP != "" {
				ipToCredCount[r.EgressIP]++
			}
		}
	}

	// ISP speed profile
	type ispData struct {
		count      int
		totalMs    int
		passTotal  int
		typeCounts map[string]int
	}
	ispMap := map[string]*ispData{}
	for _, a := range analyzed {
		isp := "Unknown"
		if a.IpInfo != nil && a.IpInfo.ISP != "" {
			isp = a.IpInfo.ISP
		}
		if ispMap[isp] == nil {
			ispMap[isp] = &ispData{typeCounts: map[string]int{}}
		}
		ispMap[isp].count++
		ispMap[isp].totalMs += a.MeanLat
		ispMap[isp].passTotal += a.PassCount
		t := a.IpType
		if t == "" {
			t = "unknown"
		}
		ispMap[isp].typeCounts[t]++
	}

	total := len(analyzed)
	if total < 1 {
		total = 1
	}
	var ispSpeeds []ISPSpeed
	for isp, d := range ispMap {
		domType := "unknown"
		best := 0
		for t, cnt := range d.typeCounts {
			if cnt > best {
				best = cnt
				domType = t
			}
		}
		ispSpeeds = append(ispSpeeds, ISPSpeed{
			ISP:            isp,
			Count:          d.count,
			PoolPct:        int(math.Round(float64(d.count) / float64(total) * 100)),
			AvgMs:          d.totalMs / d.count,
			ConsistencyPct: int(math.Round(float64(d.passTotal) / float64(d.count*runCount) * 100)),
			DominantType:   domType,
		})
	}
	sort.Slice(ispSpeeds, func(i, j int) bool {
		return ispSpeeds[i].AvgMs < ispSpeeds[j].AvgMs
	})
	if len(ispSpeeds) > 25 {
		ispSpeeds = ispSpeeds[:25]
	}

	// Composite ranking
	typeScore := map[string]float64{
		"residential": 1.0, "unknown": 0.5, "flagged_proxy": 0.1, "datacenter": 0, "private": 0,
	}
	var composite []CompositeRanked
	for _, a := range analyzed {
		if a.IpType == "datacenter" || a.IpType == "private" || a.IpType == "mobile" || a.IpType == "flagged_proxy" {
			continue
		}
		consistency := float64(a.PassCount) / math.Max(float64(a.RunCount), 1)
		spd := SpeedTierScore(a.MeanLat)
		crowding := 1
		if a.EgressIP != "" {
			if c, ok := ipToCredCount[a.EgressIP]; ok {
				crowding = c
			}
		}
		uniqueScore := 1.0
		switch {
		case crowding == 1:
			uniqueScore = 1.0
		case crowding <= 2:
			uniqueScore = 0.7
		case crowding <= 5:
			uniqueScore = 0.4
		default:
			uniqueScore = 0.1
		}
		ts := 0.5
		if v, ok := typeScore[a.IpType]; ok {
			ts = v
		}
		cs := int(math.Round(math.Pow(consistency, 2.2)*30 + spd*40 + uniqueScore*20 + ts*10))

		isp := ""
		asn := ""
		city := ""
		country := ""
		if a.IpInfo != nil {
			isp = a.IpInfo.ISP
			asn = a.IpInfo.AS
			city = a.IpInfo.City
			country = a.IpInfo.Country
		}
		composite = append(composite, CompositeRanked{
			Proxy:          a.Proxy,
			EgressIP:       a.EgressIP,
			IpType:         a.IpType,
			ISP:            isp,
			ASN:            asn,
			City:           city,
			Country:        country,
			MeanLat:        a.MeanLat,
			PassCount:      a.PassCount,
			RunCount:       a.RunCount,
			ConsistencyPct: int(math.Round(consistency * 100)),
			SharedBy:       crowding,
			CompositeScore: cs,
		})
	}
	sort.Slice(composite, func(i, j int) bool {
		return composite[i].CompositeScore > composite[j].CompositeScore
	})
	if len(composite) > 1000 {
		composite = composite[:1000]
	}

	// Crowded IPs
	var crowded []CrowdedIPEntry
	for ip, count := range ipToCredCount {
		if count > 1 {
			var match *AnalyzedProxy
			for i := range analyzed {
				if analyzed[i].EgressIP == ip {
					match = &analyzed[i]
					break
				}
			}
			entry := CrowdedIPEntry{IP: ip, SharedBy: count}
			if match != nil {
				if match.IpInfo != nil {
					entry.ISP = match.IpInfo.ISP
				}
				entry.AvgMs = &match.MeanLat
				entry.IpType = match.IpType
			}
			crowded = append(crowded, entry)
		}
	}
	sort.Slice(crowded, func(i, j int) bool {
		return crowded[i].SharedBy > crowded[j].SharedBy
	})
	if len(crowded) > 40 {
		crowded = crowded[:40]
	}

	uniqueCount := 0
	for _, c := range ipToCredCount {
		if c == 1 {
			uniqueCount++
		}
	}

	return &SessionAnalytics{
		ComputedAt:      time.Now().Format(time.RFC3339),
		TotalAnalyzed:   len(analyzed),
		UniqueEgressIPs: len(ipToCredCount),
		UncrowdedCount:  uniqueCount,
		CrowdedCount:    len(crowded),
		ISPSpeeds:       ispSpeeds,
		CrowdedIPs:      crowded,
		CompositeRanked: composite,
	}
}
