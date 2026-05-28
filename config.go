package main

// ── App constants ─────────────────────────────────────────────────────────────

const AppVersion = "1.10.0"
const HttpbinURL = "http://httpbin.org/get"
const IpApiURL   = "http://ip-api.com/json?fields=status,message,query,country,countryCode,regionName,city,isp,org,as,asname,mobile,proxy,hosting"

// ── Default score weights ─────────────────────────────────────────────────────

var DefaultScoreWeights = ScoreWeights{Speed: 20, Reliability: 15, Target: 35, IpType: 20, AntiBot: 10}

// ── Site endpoint definitions (mirrors index.html SITE_ENDPOINTS) ─────────────

var SiteEndpoints = map[string][]TargetEndpoint{
	"walmart": {
		{Key: "home",   URL: "https://www.walmart.com/",                Label: "Home"},
		{Key: "search", URL: "https://www.walmart.com/search?q=iphone", Label: "Search"},
		{Key: "login",  URL: "https://www.walmart.com/account/login",   Label: "Login"},
		{Key: "cart",   URL: "https://www.walmart.com/cart",            Label: "Cart"},
	},
	"bestbuy": {
		{Key: "home",   URL: "https://www.bestbuy.com/",                                 Label: "Home"},
		{Key: "search", URL: "https://www.bestbuy.com/site/searchpage.jsp?st=rtx+4090", Label: "Search"},
		{Key: "login",  URL: "https://www.bestbuy.com/identity/signin",                 Label: "Login"},
	},
	"homedepot": {
		{Key: "home",   URL: "https://www.homedepot.com/",             Label: "Home"},
		{Key: "search", URL: "https://www.homedepot.com/s/rtx%204090", Label: "Search"},
	},
	"target": {
		{Key: "home",   URL: "https://www.target.com/",                 Label: "Home"},
		{Key: "search", URL: "https://www.target.com/s?searchTerm=ps5", Label: "Search"},
	},
	"ticketmaster": {
		{Key: "home",   URL: "https://www.ticketmaster.com/",                  Label: "Home"},
		{Key: "search", URL: "https://www.ticketmaster.com/search?q=concert",  Label: "Search"},
	},
}

// ── IP classification lists ───────────────────────────────────────────────────

var DatacenterOrgs = []string{
	"amazon", "aws", "azure", "microsoft azure", "google cloud", "google llc",
	"digitalocean", "linode", "vultr", "hetzner", "ovh", "leaseweb", "rackspace",
	"akamai", "cloudflare", "fastly", "edgecast", "stackpath", "cdn77",
	"keycdn", "bunnycdn", "g-core", "gcore", "incapsula", "imperva",
	"choopa", "choopa llc", "constant", "colocrossing", "psychz", "fdcservers",
	"quadranet", "sharktech", "tzulo", "krypt", "multacom", "wowrack", "nocix",
	"servermania", "serverbeach", "singlehop", "nexcess", "mochahost", "performive",
	"reliablesite", "wholesale internet", "limestone networks",
	"cogent", "level 3", "lumen", "zayo", "telia company", "internap",
	"xo communications", "tata communications", "ntt communications",
	"zenlayer", "he.net", "hurricane electric",
	"equinix", "voxility", "path network", "ddos-guard", "serverius", "combahton",
	"hosthatch", "racknerd", "buyvm", "frantech", "dedicated server",
	"colocation", "data center", "datacenter", "server hosting",
	"vps hosting", "cloud hosting", "web hosting",
}

var ResidentialISPs = []string{
	// US
	"comcast", "xfinity", "spectrum", "charter communications", "at&t", "verizon",
	"cox communications", "optimum", "cablevision", "frontier communications",
	"windstream", "mediacom", "wow!", "altice", "suddenlink", "astound",
	"t-mobile", "tmobile", "cricket wireless", "boost mobile", "metro pcs",
	"sbcglobal", "bellsouth", "hughes network", "viasat", "earthlink", "brightspeed",
	// Canada
	"bell canada", "rogers communications", "telus", "shaw communications",
	"cogeco", "videotron", "sasktel", "teksavvy", "fido solutions",
	"freedom mobile", "koodo", "public mobile", "virgin plus",
	// UK
	"bt group", "british telecom", "virgin media", "sky broadband", "talktalk",
	"plusnet", "ee limited", "three uk", "vodafone uk", "o2 uk", "now broadband",
	"hyperoptic", "community fibre",
	// Germany
	"deutsche telekom", "telekom deutschland", "vodafone germany", "unitymedia",
	"o2 germany", "1&1", "freenet", "congstar",
	// France
	"orange france", "sfr", "bouygues telecom", "free sas", "iliad",
	// Spain
	"telefonica de espana", "movistar", "vodafone spain", "orange spain",
	"masmovil", "yoigo",
	// Italy
	"telecom italia", "tim ", "vodafone italy", "fastweb", "wind tre",
	// Netherlands
	"kpn", "ziggo", "t-mobile netherlands",
	// Nordics
	"telia", "telenor", "elisa", "dna plc", "tdc", "yousee",
	// Eastern Europe
	"rostelecom", "pjsc mts", "mts russia", "vimpelcom", "beeline",
	"megafon", "tele2 russia", "er-telecom", "dom.ru",
	"kyivstar", "lifecell", "ukrtelecom",
	// AU/NZ
	"telstra", "optus", "tpg telecom", "aussie broadband", "internode",
	"iinet", "dodo", "superloop",
	"spark new zealand", "vodafone new zealand", "2degrees",
	// Japan
	"ntt docomo", "softbank", "kddi", "au by kddi", "iij",
	"biglobe", "so-net", "ocn", "jcom",
	// Korea
	"kt corporation", "sk broadband", "lg uplus",
	// China
	"china telecom", "china unicom", "china mobile",
	// India
	"airtel", "reliance jio", "bsnl", "mtnl", "vodafone india",
	"idea cellular", "hathway", "act fibernet",
	// SEA
	"singtel", "starhub", "m1 limited",
	"maxis", "celcom", "tm net", "unifi",
	"globe telecom", "pldt", "smart communications",
	// LatAm
	"vivo", "claro brasil", "tim brasil", "oi internet",
	"telmex", "izzi", "totalplay", "telcel",
	// Middle East
	"bezeq", "hot telecom", "partner communications",
	"du telecom", "etisalat", "mobily", "stc",
	"turkcell", "turk telekom", "vodafone turkey",
	// Africa
	"safaricom", "vodacom", "mtn", "airtel africa",
	"telkom south africa", "cell c", "rain",
}

var ResidentialKeywords = []string{
	"telecom", "telekom", "telefon", "telefonica", "telstra", "telus",
	"cablevision", "cable tv", "cable network",
	"broadband", "fiber internet", "fibre internet",
	"dsl ", "adsl", "vdsl",
	"mobile network", "mobile operator", "mobile service",
	"cellular", "gsm network", "lte network",
	"internet service provider",
}

var DatacenterASNs = map[int]bool{
	13335: true, 15169: true, 16509: true, 14618: true,
	8075: true, 36351: true, 63949: true, 14061: true,
	20473: true, 24940: true, 16276: true, 12876: true,
	3223: true, 9009: true, 396982: true, 15133: true,
	20940: true, 54113: true, 32934: true, 2906: true,
	46489: true, 55256: true,
}

// ── Anti-bot marker lists ─────────────────────────────────────────────────────

var PxMarkers = []string{
	"_pxhd", "px-captcha", "press & hold", "_pxde", "pxchallenge",
	"px.init", "perimeterx", "_px2", "pxi/", "human challenge",
	"g.perimeterx.net", "human.js", "_pxff", "challenge.js",
	`"_pxvid"`, `"_px3"`, "pxde=", "pxff=",
}

var AkamaiMarkers = []string{
	"_abck", "akamai_pixel", "/akam/", "ak_bmsc", "sensor_data", "bm_sz", "akamaibox",
	"bmak.js", "akam_nob", "bm_sv", "bm_mi", "akavpau_",
	"detect.js", "sb_detect", "/_akam/",
}

var CfMarkers = []string{
	"cf-ray", "__cf_bm", "cloudflare", "cf_clearance", "ray id", "cf-mitigated",
}

var DatadomeMarkers = []string{
	"datadome", "dd_referrer", "dd_cookie", "ddos-guard", "_dd_s", "datadome.co",
}

var ImpervaMarkers = []string{
	"incapsula", "incap_ses", "visid_incap", "reese84", "x-iinfo", "_imperva",
}

var BlockKeywords = []string{
	"access denied", "bot detected", "unusual traffic", "automated queries",
	"please verify", "security check", "captcha", "blocked",
}

// ── Antibot cookie definitions ────────────────────────────────────────────────

type AntibotCookieDef struct {
	Pattern string
	Vendor  string
	Type    string
	Label   string
}

var AntibotCookieDefs = []AntibotCookieDef{
	// PerimeterX
	{Pattern: "_pxhd",  Vendor: "px",         Type: "device",    Label: "PX device ID"},
	{Pattern: "_px",    Vendor: "px",         Type: "tracking",  Label: "PX risk session"},
	{Pattern: "_pxvid", Vendor: "px",         Type: "tracking",  Label: "PX visitor ID"},
	{Pattern: "_pxde",  Vendor: "px",         Type: "device",    Label: "PX device extra"},
	{Pattern: "_pxff",  Vendor: "px",         Type: "device",    Label: "PX browser flags"},
	// Akamai Bot Manager
	{Pattern: "_abck",  Vendor: "akamai",     Type: "tracking",  Label: "Akamai Bot Manager"},
	{Pattern: "bm_sz",  Vendor: "akamai",     Type: "tracking",  Label: "Akamai sensor data"},
	{Pattern: "bm_sv",  Vendor: "akamai",     Type: "tracking",  Label: "Akamai sensor value"},
	{Pattern: "bm_mi",  Vendor: "akamai",     Type: "tracking",  Label: "Akamai metrics"},
	{Pattern: "ak_bmsc",Vendor: "akamai",     Type: "device",    Label: "Akamai BM session"},
	{Pattern: "akavpau",Vendor: "akamai",     Type: "device",    Label: "Akamai viewport auth"},
	// Cloudflare
	{Pattern: "cf_clearance", Vendor: "cloudflare", Type: "clearance", Label: "CF challenge passed"},
	{Pattern: "__cf_bm",      Vendor: "cloudflare", Type: "tracking",  Label: "CF Bot Management"},
	// DataDome
	{Pattern: "datadome", Vendor: "datadome", Type: "tracking",  Label: "DataDome session"},
	{Pattern: "_dd_s",    Vendor: "datadome", Type: "tracking",  Label: "DataDome session flag"},
	// Imperva
	{Pattern: "incap_ses",  Vendor: "imperva", Type: "tracking", Label: "Imperva session"},
	{Pattern: "visid_incap",Vendor: "imperva", Type: "device",   Label: "Imperva visitor ID"},
	{Pattern: "reese84",    Vendor: "imperva", Type: "device",   Label: "Imperva Reese token"},
}

// ── CDN signatures ────────────────────────────────────────────────────────────

type CDNSig struct {
	Name    string
	Headers []string // substrings to look for in raw headers
}

var CDNSignatures = []CDNSig{
	{Name: "cloudflare",  Headers: []string{"cf-ray:", "server: cloudflare"}},
	{Name: "akamai",      Headers: []string{"server: akamai", "x-akamai-", "x-check-cacheable:", "akamaighost"}},
	{Name: "cloudfront",  Headers: []string{"x-amz-cf-id:", "via: cloudfront"}},
	{Name: "fastly",      Headers: []string{"x-served-by: cache-", "x-fastly", "x-timer: s"}},
	{Name: "azure",       Headers: []string{"x-azure-ref:", "x-msedge-ref:", "x-ec-custom-error:"}},
	{Name: "imperva",     Headers: []string{"x-iinfo:", "x-cdn: imperva"}},
	{Name: "sucuri",      Headers: []string{"x-sucuri-id:"}},
	{Name: "vercel",      Headers: []string{"x-vercel-id:", "server: vercel"}},
	{Name: "bunnycdn",    Headers: []string{"cdn-requestid:", "server: bunny"}},
	{Name: "varnish",     Headers: []string{"x-varnish:", "via: varnish"}},
}
