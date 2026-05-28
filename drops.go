package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

var (
	dropsMu     sync.Mutex
	dropsList   []DropSchedule
	dropsLoaded bool

	dropRunsMu     sync.Mutex
	dropRunsList   []DropRun
	dropRunsLoaded bool
)

// ── Persistence ───────────────────────────────────────────────────────────────

func loadDrops() []DropSchedule {
	dropsMu.Lock()
	defer dropsMu.Unlock()
	if !dropsLoaded {
		if err := readJSONFile("drops.json", &dropsList); err != nil {
			dropsList = []DropSchedule{}
		}
		dropsLoaded = true
	}
	return dropsList
}

func saveDrops() {
	writeJSONFile("drops.json", dropsList)
}

func loadDropRuns(dropID string) []DropRun {
	dropRunsMu.Lock()
	defer dropRunsMu.Unlock()
	if !dropRunsLoaded {
		if err := readJSONFile("drop_runs.json", &dropRunsList); err != nil {
			dropRunsList = []DropRun{}
		}
		dropRunsLoaded = true
	}
	if dropID == "" {
		out := make([]DropRun, len(dropRunsList))
		copy(out, dropRunsList)
		return out
	}
	var filtered []DropRun
	for _, r := range dropRunsList {
		if r.DropID == dropID {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func appendDropRun(run DropRun) {
	dropRunsMu.Lock()
	defer dropRunsMu.Unlock()
	if !dropRunsLoaded {
		readJSONFile("drop_runs.json", &dropRunsList)
		dropRunsLoaded = true
	}
	dropRunsList = append(dropRunsList, run)
	if len(dropRunsList) > 2000 {
		dropRunsList = dropRunsList[len(dropRunsList)-2000:]
	}
	writeJSONFile("drop_runs.json", dropRunsList)
}

func updateDropRunStatus(runID string, updates DropRun) {
	dropRunsMu.Lock()
	defer dropRunsMu.Unlock()
	for i := range dropRunsList {
		if dropRunsList[i].RunID == runID {
			dropRunsList[i] = updates
			break
		}
	}
	writeJSONFile("drop_runs.json", dropRunsList)
}

// ── Scheduling logic ──────────────────────────────────────────────────────────

// computeDropNextFire picks the earliest pending trigger (specific time or
// recurring interval) and applies a random jitter offset.
func computeDropNextFire(d *DropSchedule, now time.Time) string {
	var candidates []time.Time

	if d.RecurringMin > 0 {
		if d.LastFired != "" {
			last := parseTime(d.LastFired)
			candidates = append(candidates, last.Add(time.Duration(d.RecurringMin)*time.Minute))
		} else {
			candidates = append(candidates, now)
		}
	}

	for _, ts := range d.PendingTimes {
		if t := parseTime(ts); !t.IsZero() {
			candidates = append(candidates, t)
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	earliest := candidates[0]
	for _, c := range candidates[1:] {
		if c.Before(earliest) {
			earliest = c
		}
	}

	if d.JitterMin > 0 {
		rangeSec := d.JitterMin * 2 * 60
		jitterSec := rand.Intn(rangeSec) - d.JitterMin*60
		earliest = earliest.Add(time.Duration(jitterSec) * time.Second)
	}

	return earliest.Format(time.RFC3339)
}

// ── Scheduler goroutine ───────────────────────────────────────────────────────

func startDropScheduler() {
	loadDrops()
	go func() {
		for range time.Tick(30 * time.Second) {
			tickDrops()
		}
	}()
}

func tickDrops() {
	dropsMu.Lock()
	now := time.Now()

	for i := range dropsList {
		d := &dropsList[i]
		if d.Status != "active" {
			continue
		}
		if d.NextFire == "" {
			d.NextFire = computeDropNextFire(d, now)
			saveDrops()
			continue
		}
		nextFire := parseTime(d.NextFire)
		if nextFire.IsZero() || now.Before(nextFire) {
			continue
		}

		// Consume the specific pending time that matched (if any)
		if len(d.PendingTimes) > 0 {
			var remaining []string
			consumed := false
			for _, ts := range d.PendingTimes {
				t := parseTime(ts)
				// Match if this specific time is within 2 min of the computed next fire
				if !consumed && !t.IsZero() && abs64(t.UnixMilli()-nextFire.UnixMilli()) <= 2*60*1000 {
					consumed = true
					continue
				}
				remaining = append(remaining, ts)
			}
			d.PendingTimes = remaining
		}

		d.LastFired = now.Format(time.RFC3339)
		d.NextFire = computeDropNextFire(d, now)
		saveDrops()

		dCopy := *d
		dropsMu.Unlock()
		go executeDrop(&dCopy)
		dropsMu.Lock()
	}

	dropsMu.Unlock()
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// ── Execution ─────────────────────────────────────────────────────────────────

// executeDrop runs all proxy lists in the drop sequentially.
func executeDrop(d *DropSchedule) {
	firedAt := time.Now().Format(time.RFC3339)
	for i := range d.ProxyLists {
		if i > 0 && d.StaggerMin > 0 {
			time.Sleep(time.Duration(d.StaggerMin) * time.Minute)
		}
		runDropList(d, &d.ProxyLists[i], firedAt)
	}
}

var walmartDropEndpoints = []TargetEndpoint{
	{Key: "home",   URL: "https://www.walmart.com/",                Label: "Home"},
	{Key: "search", URL: "https://www.walmart.com/search?q=iphone", Label: "Search"},
	{Key: "login",  URL: "https://www.walmart.com/account/login",   Label: "Login"},
	{Key: "cart",   URL: "https://www.walmart.com/cart",            Label: "Cart"},
}

func collectEndpointStats(j *Job) []DropEndpointStat {
	statMap := map[string]*DropEndpointStat{}
	for _, ep := range walmartDropEndpoints {
		cp := ep
		statMap[ep.Key] = &DropEndpointStat{Label: cp.Label, URL: cp.URL}
	}
	j.mu.Lock()
	for _, p := range j.TopProxies {
		for key, er := range p.TargetResults {
			if s, ok := statMap[key]; ok {
				s.Tested++
				if er.Pass {
					s.Passed++
				}
				if er.Px {
					s.PxHit++
				}
			}
		}
	}
	j.mu.Unlock()
	var result []DropEndpointStat
	for _, ep := range walmartDropEndpoints {
		s := statMap[ep.Key]
		if s.Tested > 0 {
			s.PassPct = float64(s.Passed) / float64(s.Tested) * 100
			s.PxPct = float64(s.PxHit) / float64(s.Tested) * 100
		}
		result = append(result, *s)
	}
	return result
}

func runDropList(d *DropSchedule, list *DropProxyList, firedAt string) {
	proxies := ParseProxies(list.ProxyText)
	if len(proxies) == 0 {
		return
	}

	config := &JobConfig{
		TestURL:     IpApiURL,
		TargetURLs:  walmartDropEndpoints,
		Concurrency: 50,
		Timeout:     10,
		Retries:     1,
		TopN:        len(proxies),
	}
	if config.Concurrency > 150 {
		config.Concurrency = 150
	}

	jobID := generateJobID()
	runID := "dr_" + generateID()

	run := DropRun{
		RunID:        runID,
		DropID:       d.ID,
		ListID:       list.ID,
		ListName:     list.Name,
		JobID:        jobID,
		FiredAt:      firedAt,
		TotalProxies: len(proxies),
		Status:       "running",
	}
	appendDropRun(run)

	job := &Job{
		JobID:      jobID,
		Status:     "queued",
		Total:      len(proxies),
		ListName:   fmt.Sprintf("%s — %s (Drop)", d.Name, list.Name),
		Config:     config,
		TopProxies: []ProxyResult{},
	}
	globalMu.Lock()
	globalJobs[jobID] = job
	globalMu.Unlock()

	RunJob(jobID, proxies, config)

	globalMu.RLock()
	j := globalJobs[jobID]
	globalMu.RUnlock()

	if j != nil {
		j.mu.Lock()
		run.PassCount = j.Passed
		run.PxCount = j.pxCount
		run.TargetPassed = j.targetPassed
		if j.DataUsage != nil && j.DataUsage.TargetTested > 0 {
			avgMs := 0
			cnt := 0
			for _, p := range j.TopProxies {
				if p.AvgMs != nil {
					avgMs += *p.AvgMs
					cnt++
				}
			}
			if cnt > 0 {
				ms := avgMs / cnt
				run.AvgMs = &ms
			}
		}
		j.mu.Unlock()
		if run.TotalProxies > 0 {
			run.PxPassPct = float64(run.PassCount) / float64(run.TotalProxies) * 100
		}
		run.EndpointStats = collectEndpointStats(j)
		run.Status = "complete"
	} else {
		run.Status = "error"
	}
	updateDropRunStatus(runID, run)

	notifyCfg := loadNotifyConfig()
	webhook := d.WebhookOnTest
	if webhook == "" && notifyCfg.EnableDropTest {
		webhook = notifyCfg.WebhookDropTest
		if webhook == "" {
			webhook = notifyCfg.DiscordWebhook
		}
	}
	if webhook != "" {
		msg := fmt.Sprintf(
			"⬡ **DROP TEST COMPLETE** — %s\nList: **%s** | Proxies: %d | Passed: %d | PX Pass: %.0f%%",
			d.Name, list.Name, run.TotalProxies, run.PassCount, run.PxPassPct,
		)
		go sendDiscordMessage(webhook, msg)
	}
}
