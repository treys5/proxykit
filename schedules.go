package main

import (
	"math"
	"sync"
	"time"
)

var (
	schedulesMu     sync.Mutex
	schedulesLoaded bool
	schedulesList   []Schedule
)

func loadSchedules() []Schedule {
	schedulesMu.Lock()
	defer schedulesMu.Unlock()
	if schedulesLoaded {
		return schedulesList
	}
	if err := readJSONFile("schedules.json", &schedulesList); err != nil {
		schedulesList = []Schedule{}
	}
	schedulesLoaded = true
	return schedulesList
}

func saveSchedules() {
	writeJSONFile("schedules.json", schedulesList)
}

func nextFireMs(s Schedule) int64 {
	if s.Status == "paused" || s.Status == "done" {
		return math.MaxInt64 / 2
	}
	baseMs := parseTime(s.FireAt).UnixMilli()
	if s.Type == "once" {
		return baseMs
	}
	intervalMs := int64(s.IntervalHours * 3600000)
	if s.LastFired == "" {
		return baseMs
	}
	last := parseTime(s.LastFired).UnixMilli()
	return last + intervalMs
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, _ = time.Parse("2006-01-02T15:04:05.000Z", s)
	}
	return t
}

func fireScheduledJob(sched *Schedule) {
	proxies := ParseProxies(sched.ProxyText)
	if len(proxies) == 0 {
		return
	}
	jobID := generateJobID()
	config := &JobConfig{
		TestURL:     IpApiURL,
		TargetURL:   sched.TargetURL,
		TargetURLs:  sched.TargetURLs,
		Concurrency: sched.Concurrency,
		Timeout:     sched.Timeout,
		Retries:     sched.Retries,
		TopN:        sched.TopN,
	}
	if config.Concurrency == 0 {
		config.Concurrency = 50
	}
	if config.Timeout == 0 {
		config.Timeout = 10
	}
	if config.Retries == 0 {
		config.Retries = 1
	}
	if config.TopN == 0 {
		config.TopN = 1000
	}
	if config.Concurrency > 150 {
		config.Concurrency = 150
	}

	job := &Job{
		JobID:       jobID,
		Status:      "queued",
		Total:       len(proxies),
		ListName:    sched.Name + " (scheduled)",
		Config:      config,
		ScheduledID: sched.ID,
		TopProxies:  []ProxyResult{},
	}
	globalMu.Lock()
	globalJobs[jobID] = job
	globalMu.Unlock()

	go func() {
		RunJob(jobID, proxies, config)

		// Discord notification — use per-schedule webhook, then job-complete type, then global
		notifyCfg := loadNotifyConfig()
		webhook := sched.DiscordWebhook
		if webhook == "" && notifyCfg.EnableJobComplete {
			webhook = notifyCfg.WebhookJobComplete
		}
		if webhook == "" {
			webhook = notifyCfg.DiscordWebhook
		}
		if webhook != "" {
			globalMu.RLock()
			j := globalJobs[jobID]
			globalMu.RUnlock()
			if j != nil {
				sendDiscordNotification(webhook, j, sched.Name)
			}
		}

		schedulesMu.Lock()
		for i := range schedulesList {
			if schedulesList[i].ID == sched.ID {
				schedulesList[i].LastJobID = jobID
				if schedulesList[i].Type == "once" {
					schedulesList[i].Status = "done"
				} else {
					schedulesList[i].Status = "pending"
					next := time.UnixMilli(nextFireMs(schedulesList[i])).Format(time.RFC3339)
					schedulesList[i].NextFire = next
				}
				break
			}
		}
		saveSchedules()
		schedulesMu.Unlock()
	}()
}

// ── Scheduler goroutine — ticks every 30 seconds ──────────────────────────────

func startScheduler() {
	go func() {
		for range time.Tick(30 * time.Second) {
			schedulesMu.Lock()
			now := time.Now().UnixMilli()
			for i := range schedulesList {
				s := &schedulesList[i]
				if s.Status == "paused" || s.Status == "done" || s.Status == "running" {
					continue
				}
				if now < nextFireMs(*s) {
					continue
				}
				s.Status = "running"
				s.LastFired = time.Now().Format(time.RFC3339)
				saveSchedules()
				sCopy := *s
				schedulesMu.Unlock()
				fireScheduledJob(&sCopy)
				schedulesMu.Lock()
			}
			schedulesMu.Unlock()
		}
	}()
}
