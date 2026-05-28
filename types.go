package main

import "sync"

// ── Proxy ─────────────────────────────────────────────────────────────────────

type Proxy struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // http, https, socks4, socks5
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// ── Target endpoint (mirrors SITE_ENDPOINTS entries) ─────────────────────────

type TargetEndpoint struct {
	Key   string `json:"key"`
	URL   string `json:"url"`
	Label string `json:"label"`
}

// ── Per-attempt result from a single testProxyOnce call ──────────────────────

type ProxyAttempt struct {
	OK                  bool   `json:"ok"`
	Ms                  int    `json:"ms"`
	Body                string `json:"-"`
	BytesSent           int    `json:"bytes_sent"`
	BytesReceived       int    `json:"bytes_received"`
	PxChallenge         bool   `json:"px_challenge"`
	HTTPStatus          int    `json:"http_status"`
	BotVendor           string `json:"bot_vendor,omitempty"`
	EdgeCDN             string `json:"edge_cdn,omitempty"`
	AntibotCookies      []AntibotCookie `json:"antibot_cookies"`
	AntibotHasClearance bool   `json:"antibot_has_clearance"`
	PxRiskScore         *int   `json:"px_risk_score,omitempty"`
	H2Support           bool   `json:"h2_support"`
	SslInspected        bool   `json:"ssl_inspected"`
	ResponseSize        int    `json:"response_size"`
	HeadersRaw          string `json:"-"`
}

// ── Per-endpoint result in a testProxy call ───────────────────────────────────

type EndpointResult struct {
	Label          string          `json:"label"`
	URL            string          `json:"url"`
	Pass           bool            `json:"pass"`
	Px             bool            `json:"px"`
	Ms             *int            `json:"ms,omitempty"`
	HTTPStatus     *int            `json:"http_status,omitempty"`
	BotVendor      string          `json:"bot_vendor,omitempty"`
	AntibotCookies []AntibotCookie `json:"antibot_cookies"`
	EdgeCDN        string          `json:"edge_cdn,omitempty"`
}

// ── Antibot cookie ────────────────────────────────────────────────────────────

type AntibotCookie struct {
	Name   string `json:"name"`
	Vendor string `json:"vendor"`
	Type   string `json:"type"`
	Label  string `json:"label"`
}

// ── IP info from ip-api.com ───────────────────────────────────────────────────

type IpApiResponse struct {
	Status      string  `json:"status"`
	Query       string  `json:"query"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	AS          string  `json:"as"`
	ASName      string  `json:"asname"`
	Mobile      bool    `json:"mobile"`
	Proxy       bool    `json:"proxy"`
	Hosting     bool    `json:"hosting"`
}

type IpInfo struct {
	ISP         string `json:"isp,omitempty"`
	Org         string `json:"org,omitempty"`
	AS          string `json:"as,omitempty"`
	ASName      string `json:"asname,omitempty"`
	City        string `json:"city,omitempty"`
	Region      string `json:"region,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
	Hosting     bool   `json:"hosting"`
	Proxy       bool   `json:"proxy"`
	Mobile      bool   `json:"mobile"`
}

// ── Final proxy test result ───────────────────────────────────────────────────

type ProxyResult struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	Status      string   `json:"status"` // pass | fail
	AvgMs       *int     `json:"avg_ms"`
	MinMs       *int     `json:"min_ms"`
	SuccessRate float64  `json:"success_rate"`
	Score       int      `json:"score"`

	EgressIP  string   `json:"egress_ip,omitempty"`
	IpInfo    *IpInfo  `json:"ip_info,omitempty"`
	IpType    string   `json:"ip_type"` // residential | mobile | datacenter | unknown | flagged_proxy | private

	HttpbinPass *bool   `json:"httpbin_pass,omitempty"`
	TargetPass  *bool   `json:"target_pass,omitempty"`
	TargetMs    *int    `json:"target_ms,omitempty"`
	TargetPx    bool    `json:"target_px"`

	BytesSent     int `json:"bytes_sent"`
	BytesReceived int `json:"bytes_received"`

	PxChallenge         bool   `json:"px_challenge"`
	EdgeRtt             *int   `json:"edge_rtt,omitempty"`
	Rotating            bool   `json:"rotating"`
	SslInspected        bool   `json:"ssl_inspected"`

	TargetBotVendor      string `json:"target_bot_vendor,omitempty"`
	TargetStatus         *int   `json:"target_status,omitempty"`
	TargetResponseSize   *int   `json:"target_response_size,omitempty"`
	EdgeCDN              string `json:"edge_cdn,omitempty"`
	AnonLevel            string `json:"anon_level,omitempty"`

	AntibotCookies      []AntibotCookie `json:"antibot_cookies"`
	AntibotHasClearance bool            `json:"antibot_has_clearance"`
	PxRiskScore         *int            `json:"px_risk_score,omitempty"`
	H2Support           bool            `json:"h2_support"`

	TargetResults map[string]EndpointResult `json:"target_results,omitempty"`
}

// ── Job ───────────────────────────────────────────────────────────────────────

type JobConfig struct {
	TestURL      string           `json:"test_url"`
	TargetURL    string           `json:"target_url,omitempty"`
	TargetURLs   []TargetEndpoint `json:"target_urls,omitempty"`
	Concurrency  int              `json:"concurrency"`
	Timeout      float64          `json:"timeout"`
	Retries      int              `json:"retries"`
	TopN         int              `json:"top_n"`
	SkipHttpbin  bool             `json:"skip_httpbin"`
}

type DataUsage struct {
	BytesSent          int      `json:"bytes_sent"`
	BytesReceived      int      `json:"bytes_received"`
	TotalBytes         int      `json:"total_bytes"`
	AvgBytesPerProxy   int      `json:"avg_bytes_per_proxy"`
	PxChallengeCount   int      `json:"px_challenge_count"`
	PxChallengePct     float64  `json:"px_challenge_pct"`
	HttpbinTested      int      `json:"httpbin_tested"`
	HttpbinPassed      int      `json:"httpbin_passed"`
	HttpbinPassPct     *float64 `json:"httpbin_pass_pct,omitempty"`
	TargetTested       int      `json:"target_tested"`
	TargetPassed       int      `json:"target_passed"`
	TargetPassPct      *float64 `json:"target_pass_pct,omitempty"`
	VendorCounts       map[string]int `json:"vendor_counts"`
	StatusCounts       map[string]int `json:"status_counts"`
}

type Job struct {
	JobID           string       `json:"job_id"`
	Status          string       `json:"status"` // queued | running | done | error
	SessionID       string       `json:"session_id,omitempty"`
	ScheduledID     string       `json:"scheduled_id,omitempty"`
	ListName        string       `json:"list_name"`
	Total           int          `json:"total"`
	Tested          int          `json:"tested"`
	Passed          int          `json:"passed"`
	Failed          int          `json:"failed"`
	ProgressPct     float64      `json:"progress_pct"`
	ElapsedSec      float64      `json:"elapsed_sec"`
	EtaSec          *int         `json:"eta_sec,omitempty"`
	Config          *JobConfig   `json:"config,omitempty"`
	DataUsage       *DataUsage   `json:"data_usage,omitempty"`
	IpAnalysis      *IpAnalysis  `json:"ip_analysis,omitempty"`
	TopProxies      []ProxyResult `json:"top_proxies,omitempty"`

	// Runtime counters (not serialized to JSON directly)
	mu              sync.Mutex `json:"-"`
	startTime       int64      // unix ms
	pxCount         int
	httpbinTested   int
	httpbinPassed   int
	targetTested    int
	targetPassed    int
	bytesSent       int
	bytesReceived   int
	vendorCounts    map[string]int
	statusCounts    map[string]int
}

// ── IP Analysis ───────────────────────────────────────────────────────────────

type ReusedIP struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
}

type TopASN struct {
	ASN   string `json:"asn"`
	Count int    `json:"count"`
}

type TopISP struct {
	ISP   string `json:"isp"`
	Count int    `json:"count"`
}

type CrowdedSubnet struct {
	Subnet string `json:"subnet"`
	Count  int    `json:"count"`
}

type IpAnalysis struct {
	TotalPassed       int      `json:"total_passed"`
	TotalWithIP       int      `json:"total_with_ip"`
	UniqueIPs         int      `json:"unique_ips"`
	ISPDiversity      int      `json:"isp_diversity"`
	ReuseRatePct      float64  `json:"reuse_rate_pct"`
	ResidentialCount  int      `json:"residential_count"`
	ResidentialPct    float64  `json:"residential_pct"`
	DatacenterCount   int      `json:"datacenter_count"`
	DatacenterPct     float64  `json:"datacenter_pct"`
	MobileCount       int      `json:"mobile_count"`
	FlaggedCount      int      `json:"flagged_count"`
	PrivateCount      int      `json:"private_count"`
	UnknownCount      int      `json:"unknown_count"`
	QualityScore      int      `json:"quality_score"`
	TopReusedIPs      []ReusedIP      `json:"top_reused_ips"`
	TopASNs           []TopASN        `json:"top_asns"`
	TopISPs           []TopISP        `json:"top_isps"`
	CrowdedSubnets    []CrowdedSubnet `json:"crowded_subnets"`
	RotatingCount     int      `json:"rotating_count"`
	SslInspectedCount int      `json:"ssl_inspected_count"`
}

// ── Session ───────────────────────────────────────────────────────────────────

type ProxyRunEntry struct {
	RunNum      int     `json:"run_num"`
	JobID       string  `json:"job_id"`
	Status      string  `json:"status"`
	AvgMs       *int    `json:"avg_ms"`
	MinMs       *int    `json:"min_ms"`
	SuccessRate float64 `json:"success_rate"`
	Score       int     `json:"score"`
	EgressIP    string  `json:"egress_ip,omitempty"`
	IpType      string  `json:"ip_type"`
	IpInfo      *IpInfo `json:"ip_info,omitempty"`
}

type ProxyHistoryEntry struct {
	Proxy ProxyResult     `json:"proxy"`
	Runs  []ProxyRunEntry `json:"runs"`
}

type AnalyzedProxy struct {
	Proxy        ProxyResult    `json:"proxy"`
	RunCount     int            `json:"run_count"`
	PassCount    int            `json:"pass_count"`
	PassPct      int            `json:"pass_pct"`
	MeanLat      int            `json:"mean_lat"`
	MinLat       *int           `json:"min_lat,omitempty"`
	MeanScore    float64        `json:"mean_score"`
	IpType       string         `json:"ip_type"`
	EgressIP     string         `json:"egress_ip,omitempty"`
	IpInfo       *IpInfo        `json:"ip_info,omitempty"`
	LatHistory   []*int         `json:"lat_history"`
	ScoreHistory []float64      `json:"score_history"`
}

type SessionConfig struct {
	TestURL     string           `json:"test_url"`
	TargetURL   string           `json:"target_url,omitempty"`
	TargetURLs  []TargetEndpoint `json:"target_urls,omitempty"`
	Concurrency int              `json:"concurrency"`
	Timeout     float64          `json:"timeout"`
	Retries     int              `json:"retries"`
	TopN        int              `json:"top_n"`
	SkipHttpbin bool             `json:"skip_httpbin"`
	MinPassRuns int              `json:"min_pass_runs,omitempty"`
}

type Session struct {
	SessionID    string                       `json:"session_id"`
	Name         string                       `json:"name"`
	Status       string                       `json:"status"` // idle | running
	ProxyType    string                       `json:"proxy_type,omitempty"`
	RunCount     int                          `json:"run_count"`
	RunIDs       []string                     `json:"run_ids"`
	Config       *SessionConfig               `json:"config"`
	IpAnalysis   *IpAnalysis                  `json:"ip_analysis,omitempty"`
	Analytics    *SessionAnalytics            `json:"analytics,omitempty"`
	CreatedAt    string                       `json:"created_at"`
	UpdatedAt    string                       `json:"updated_at"`

	// Runtime only (not serialized in list views)
	Proxies      []Proxy                      `json:"proxies,omitempty"`
	ProxyHistory map[string]*ProxyHistoryEntry `json:"proxy_history,omitempty"`
	Analyzed     []AnalyzedProxy              `json:"analyzed,omitempty"`
	BestProxies  []AnalyzedProxy              `json:"best_proxies,omitempty"`
}

// ── Session Analytics ─────────────────────────────────────────────────────────

type ISPSpeed struct {
	ISP             string  `json:"isp"`
	Count           int     `json:"count"`
	PoolPct         int     `json:"pool_pct"`
	AvgMs           int     `json:"avg_ms"`
	ConsistencyPct  int     `json:"consistency_pct"`
	DominantType    string  `json:"dominant_type"`
}

type CompositeRanked struct {
	Proxy           ProxyResult `json:"proxy"`
	EgressIP        string  `json:"egress_ip,omitempty"`
	IpType          string  `json:"ip_type"`
	ISP             string  `json:"isp,omitempty"`
	ASN             string  `json:"asn,omitempty"`
	City            string  `json:"city,omitempty"`
	Country         string  `json:"country,omitempty"`
	MeanLat         int     `json:"mean_lat"`
	PassCount       int     `json:"pass_count"`
	RunCount        int     `json:"run_count"`
	ConsistencyPct  int     `json:"consistency_pct"`
	SharedBy        int     `json:"shared_by"`
	CompositeScore  int     `json:"composite_score"`
}

type CrowdedIPEntry struct {
	IP       string  `json:"ip"`
	SharedBy int     `json:"shared_by"`
	ISP      string  `json:"isp,omitempty"`
	AvgMs    *int    `json:"avg_ms,omitempty"`
	IpType   string  `json:"ip_type,omitempty"`
}

type SessionAnalytics struct {
	ComputedAt        string            `json:"computed_at"`
	TotalAnalyzed     int               `json:"total_analyzed"`
	UniqueEgressIPs   int               `json:"unique_egress_ips"`
	UncrowdedCount    int               `json:"uncrowded_count"`
	CrowdedCount      int               `json:"crowded_count"`
	ISPSpeeds         []ISPSpeed        `json:"isp_speeds"`
	CrowdedIPs        []CrowdedIPEntry  `json:"crowded_ips"`
	CompositeRanked   []CompositeRanked `json:"composite_ranked"`
}

// ── Provider ──────────────────────────────────────────────────────────────────

type Provider struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	InputText   string   `json:"input_text,omitempty"`
	Proxies     []Proxy  `json:"proxies,omitempty"`
	SessionIDs  []string `json:"session_ids"`
	ProxyCount  int      `json:"proxy_count"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// ── Schedule ──────────────────────────────────────────────────────────────────

type Schedule struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"` // once | recurring
	FireAt         string           `json:"fire_at"`
	IntervalHours  float64          `json:"interval_hours"`
	NextFire       string           `json:"next_fire,omitempty"`
	ProxyText      string           `json:"proxy_text,omitempty"`
	ProxyCount     int              `json:"proxy_count"`
	ProxyType      string           `json:"proxy_type,omitempty"`
	TargetURL      string           `json:"target_url,omitempty"`
	TargetURLs     []TargetEndpoint `json:"target_urls,omitempty"`
	Preset         string           `json:"preset,omitempty"`
	Concurrency    int              `json:"concurrency"`
	Timeout        float64          `json:"timeout"`
	Retries        int              `json:"retries"`
	TopN           int              `json:"top_n"`
	DiscordWebhook string           `json:"discord_webhook,omitempty"`
	Status         string           `json:"status"` // pending | running | done | paused
	LastFired      string           `json:"last_fired,omitempty"`
	LastJobID      string           `json:"last_job_id,omitempty"`
	CreatedAt      string           `json:"created_at"`
}

// ── Score config ──────────────────────────────────────────────────────────────

type ScoreWeights struct {
	Speed       int `json:"speed"`
	Reliability int `json:"reliability"`
	Target      int `json:"target"`
	IpType      int `json:"ip_type"`
	AntiBot     int `json:"anti_bot"`
}

type ScoreConfig struct {
	Weights ScoreWeights `json:"weights"`
}

// ── Analytics / notify config ─────────────────────────────────────────────────

type AnalyticsConfig struct {
	OptIn    bool   `json:"opt_in"`
	ClientID string `json:"client_id,omitempty"`
}

type NotifyConfig struct {
	// Global fallback webhook (used when a type-specific one is blank)
	DiscordWebhook string `json:"discord_webhook"`

	// Per-notification-type webhook URLs (optional — falls back to DiscordWebhook)
	WebhookPXChanges      string `json:"webhook_px_changes,omitempty"`
	WebhookJobComplete    string `json:"webhook_job_complete,omitempty"`
	WebhookProviderIssues string `json:"webhook_provider_issues,omitempty"`
	WebhookSystemAlerts   string `json:"webhook_system_alerts,omitempty"`
	WebhookDropTest       string `json:"webhook_drop_test,omitempty"`
	WebhookDropPX         string `json:"webhook_drop_px,omitempty"`

	// Enable/disable each notification type
	EnablePXChanges      bool `json:"enable_px_changes"`
	EnableJobComplete    bool `json:"enable_job_complete"`
	EnableProviderIssues bool `json:"enable_provider_issues"`
	EnableSystemAlerts   bool `json:"enable_system_alerts"`
	EnableDropTest       bool `json:"enable_drop_test"`
	EnableDropPX         bool `json:"enable_drop_px"`
}

// ── Drop Scheduler ────────────────────────────────────────────────────────────

type DropProxyList struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProxyText  string `json:"proxy_text,omitempty"`
	ProxyCount int    `json:"proxy_count"`
}

type DropRun struct {
	RunID         string             `json:"run_id"`
	DropID        string             `json:"drop_id"`
	ListID        string             `json:"list_id"`
	ListName      string             `json:"list_name"`
	JobID         string             `json:"job_id"`
	FiredAt       string             `json:"fired_at"`
	TotalProxies  int                `json:"total_proxies"`
	PassCount     int                `json:"pass_count"`
	TargetPassed  int                `json:"target_passed"`
	PxCount       int                `json:"px_count"`
	PxPassPct     float64            `json:"px_pass_pct"`
	AvgMs         *int               `json:"avg_ms,omitempty"`
	Status        string             `json:"status"` // running | complete | error
	EndpointStats []DropEndpointStat `json:"endpoint_stats,omitempty"`
}

type DropEndpointStat struct {
	Label   string  `json:"label"`
	URL     string  `json:"url"`
	Tested  int     `json:"tested"`
	Passed  int     `json:"passed"`
	PxHit   int     `json:"px_hit"`
	PassPct float64 `json:"pass_pct"`
	PxPct   float64 `json:"px_pct"`
}

type DropSchedule struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ProxyLists    []DropProxyList `json:"proxy_lists"`
	PendingTimes  []string        `json:"pending_times"`  // one-off RFC3339 times not yet fired
	RecurringMin  int             `json:"recurring_min"`  // 0=off; 15/30/60/120/240/360/720/1440
	JitterMin     int             `json:"jitter_min"`     // random ±N min offset on each fire
	StaggerMin    int             `json:"stagger_min"`    // minutes between each list execution
	NextFire      string          `json:"next_fire,omitempty"`
	WebhookOnTest string          `json:"webhook_on_test,omitempty"`
	WebhookOnPX   string          `json:"webhook_on_px,omitempty"`
	Status        string          `json:"status"` // active | paused
	LastFired     string          `json:"last_fired,omitempty"`
	CreatedAt     string          `json:"created_at"`
}

// ── Saved Proxy List ──────────────────────────────────────────────────────────

type ProxyList struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	Count     int    `json:"count"`
	Saved     string `json:"saved,omitempty"`
	ProxyType string `json:"proxy_type,omitempty"`
}

// ── API Integration ───────────────────────────────────────────────────────────

type Integration struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Key        string `json:"key,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	URL        string `json:"url,omitempty"`
	ServiceID  string `json:"service_id,omitempty"`
	APIType    string `json:"api_type,omitempty"`
	Created    string `json:"created,omitempty"`
}

// ── Global state ──────────────────────────────────────────────────────────────

var (
	globalMu        sync.RWMutex
	globalJobs      = map[string]*Job{}
	globalSessions  = map[string]*Session{}
	globalProviders = map[string]*Provider{}
)
