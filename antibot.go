package main

import (
	"regexp"
	"strconv"
	"strings"
)

// ── Bot vendor detection from body + headers ──────────────────────────────────

func DetectBotVendor(body, headers string, statusCode int) string {
	bl := strings.ToLower(body)
	hl := strings.ToLower(headers)

	for _, m := range PxMarkers {
		if strings.Contains(bl, m) {
			return "px"
		}
	}
	for _, m := range AkamaiMarkers {
		if strings.Contains(bl, m) || strings.Contains(hl, m) {
			return "akamai"
		}
	}
	for _, m := range CfMarkers {
		if strings.Contains(bl, m) || strings.Contains(hl, m) {
			return "cloudflare"
		}
	}
	for _, m := range DatadomeMarkers {
		if strings.Contains(bl, m) {
			return "datadome"
		}
	}
	for _, m := range ImpervaMarkers {
		if strings.Contains(bl, m) || strings.Contains(hl, m) {
			return "imperva"
		}
	}
	if (statusCode == 403 || statusCode == 429) {
		for _, k := range BlockKeywords {
			if strings.Contains(bl, k) {
				return "block"
			}
		}
		return "block"
	}
	return ""
}

// ── Edge CDN detection ────────────────────────────────────────────────────────

func DetectEdgeCDN(headers string) string {
	hl := strings.ToLower(headers)
	for _, sig := range CDNSignatures {
		for _, h := range sig.Headers {
			if strings.Contains(hl, h) {
				return sig.Name
			}
		}
	}
	return ""
}

// ── Antibot cookie extraction from raw response headers ───────────────────────

func ExtractAntibotCookies(headersRaw string) ([]AntibotCookie, bool) {
	if headersRaw == "" {
		return nil, false
	}
	cookieMap := map[string]AntibotCookie{}
	hasClearance := false

	// Find all Set-Cookie header lines
	lines := strings.Split(headersRaw, "\n")
	for _, line := range lines {
		ll := strings.ToLower(line)
		if !strings.HasPrefix(ll, "set-cookie:") {
			continue
		}
		// Extract the cookie name (first key before =)
		rest := strings.TrimSpace(line[len("set-cookie:"):])
		eqIdx := strings.IndexByte(rest, '=')
		scIdx := strings.IndexByte(rest, ';')
		nameEnd := eqIdx
		if eqIdx == -1 || (scIdx != -1 && scIdx < eqIdx) {
			nameEnd = scIdx
		}
		if nameEnd == -1 {
			nameEnd = len(rest)
		}
		cookieName := strings.TrimSpace(rest[:nameEnd])
		if cookieName == "" {
			continue
		}

		cnl := strings.ToLower(cookieName)
		for _, def := range AntibotCookieDefs {
			if strings.Contains(cnl, strings.ToLower(def.Pattern)) {
				cookieMap[cookieName] = AntibotCookie{
					Name:   cookieName,
					Vendor: def.Vendor,
					Type:   def.Type,
					Label:  def.Label,
				}
				if def.Type == "clearance" {
					hasClearance = true
				}
				break
			}
		}
	}

	cookies := make([]AntibotCookie, 0, len(cookieMap))
	for _, c := range cookieMap {
		cookies = append(cookies, c)
	}
	return cookies, hasClearance
}

// ── PX risk score extraction ──────────────────────────────────────────────────

var pxScoreRe  = regexp.MustCompile(`"score"\s*:\s*(\d+)`)
var pxScore2Re = regexp.MustCompile(`_pxScore\s*[=:]\s*(\d+)`)

func ExtractPxRiskScore(body, botVendor string) *int {
	if botVendor != "px" || body == "" {
		return nil
	}
	if m := pxScoreRe.FindStringSubmatch(body); m != nil {
		v, _ := strconv.Atoi(m[1])
		return &v
	}
	if m := pxScore2Re.FindStringSubmatch(body); m != nil {
		v, _ := strconv.Atoi(m[1])
		return &v
	}
	return nil
}

// ── Anonymity level from httpbin /get response ────────────────────────────────

func DetectAnonymityLevel(body string) string {
	if body == "" {
		return ""
	}
	// Find JSON object
	s := strings.Index(body, "{")
	e := strings.LastIndex(body, "}")
	if s == -1 || e == -1 || e <= s {
		return ""
	}
	jsonStr := body[s : e+1]
	jsonLower := strings.ToLower(jsonStr)

	// Check for forwarded-for or real-ip → transparent
	if strings.Contains(jsonLower, `"x-forwarded-for"`) || strings.Contains(jsonLower, `"x-real-ip"`) {
		return "transparent"
	}
	// via / proxy-connection → anonymous
	if strings.Contains(jsonLower, `"via"`) || strings.Contains(jsonLower, `"proxy-connection"`) || strings.Contains(jsonLower, `"forwarded"`) {
		return "anonymous"
	}
	return "elite"
}

// ── IP classification ─────────────────────────────────────────────────────────

func ExtractASNNum(asStr string) int {
	re := regexp.MustCompile(`AS(\d+)`)
	m := re.FindStringSubmatch(strings.ToUpper(asStr))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func ClassifyIP(info *IpApiResponse) string {
	if info == nil || info.Query == "" {
		return "unknown"
	}
	ip := info.Query

	// RFC1918 / loopback
	if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "127.") || strings.HasPrefix(ip, "169.254.") ||
		strings.HasPrefix(ip, "192.168.") {
		return "private"
	}
	if isRFC1918_172(ip) {
		return "private"
	}

	// ip-api boolean flags
	if info.Hosting {
		return "datacenter"
	}
	if info.Proxy {
		return "flagged_proxy"
	}

	combined := strings.ToLower(info.Org + " " + info.ISP + " " + info.ASName)

	// Check datacenter list first
	for _, dc := range DatacenterOrgs {
		if strings.Contains(combined, dc) {
			return "datacenter"
		}
	}

	// Mobile flag
	if info.Mobile {
		return "mobile"
	}

	// Residential ISPs
	for _, isp := range ResidentialISPs {
		if strings.Contains(combined, isp) {
			return "residential"
		}
	}

	// Residential keywords
	for _, kw := range ResidentialKeywords {
		if strings.Contains(combined, kw) {
			return "residential"
		}
	}

	// ip-api says not hosting
	if !info.Hosting {
		return "residential"
	}

	return "unknown"
}

func isRFC1918_172(ip string) bool {
	// 172.16.0.0/12
	parts := strings.Split(ip, ".")
	if len(parts) < 2 || parts[0] != "172" {
		return false
	}
	second, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return second >= 16 && second <= 31
}

// ── Speed tier score ──────────────────────────────────────────────────────────

func SpeedTierScore(ms int) float64 {
	if ms <= 0 {
		return 0
	}
	if ms <= 150 {
		return 1.00
	}
	if ms <= 300 {
		return 0.80
	}
	if ms <= 500 {
		return 0.55
	}
	if ms <= 800 {
		return 0.30
	}
	if ms <= 1200 {
		return 0.12
	}
	return 0.03
}
