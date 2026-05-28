package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ── Proxy parser ──────────────────────────────────────────────────────────────

func ParseProxies(raw string) []Proxy {
	seen := map[string]bool{}
	var out []Proxy

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		protocol := "http"
		if m := regexp.MustCompile(`(?i)^(https?|socks[45])://`).FindString(line); m != "" {
			protocol = strings.ToLower(strings.TrimSuffix(m, "://"))
			line = line[len(m):]
		}

		var host string
		var port int
		var username, password string

		if atIdx := strings.LastIndex(line, "@"); atIdx != -1 {
			creds := line[:atIdx]
			hp := line[atIdx+1:]
			if ciIdx := strings.Index(creds, ":"); ciIdx != -1 {
				username = creds[:ciIdx]
				password = creds[ciIdx+1:]
			} else {
				username = creds
			}
			if hiIdx := strings.LastIndex(hp, ":"); hiIdx != -1 {
				host = hp[:hiIdx]
				port, _ = strconv.Atoi(hp[hiIdx+1:])
			}
		} else {
			parts := strings.Split(line, ":")
			switch len(parts) {
			case 4: // host:port:user:pass
				host = parts[0]
				port, _ = strconv.Atoi(parts[1])
				username = parts[2]
				password = parts[3]
			case 2: // host:port
				host = parts[0]
				port, _ = strconv.Atoi(parts[1])
			default:
				continue
			}
		}

		if host == "" || port == 0 {
			continue
		}
		key := proxyKey(host, port, username, password)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Proxy{Host: host, Port: port, Protocol: protocol, Username: username, Password: password})
	}
	return out
}

func proxyKey(host string, port int, user, pass string) string {
	return user + ":" + pass + "@" + host + ":" + strconv.Itoa(port)
}

func ProxyLine(p *ProxyResult) string {
	if p.Username != "" {
		return fmt.Sprintf("%s:%d:%s:%s", p.Host, p.Port, p.Username, p.Password)
	}
	return fmt.Sprintf("%s:%d", p.Host, p.Port)
}

func ProxyLineFromProxy(p Proxy) string {
	if p.Username != "" {
		return fmt.Sprintf("%s:%d:%s:%s", p.Host, p.Port, p.Username, p.Password)
	}
	return fmt.Sprintf("%s:%d", p.Host, p.Port)
}

// ── Chunked transfer-encoding decoder ────────────────────────────────────────

func decodeChunked(body string) string {
	result := &strings.Builder{}
	pos := 0
	for pos < len(body) {
		lineEnd := strings.Index(body[pos:], "\r\n")
		if lineEnd == -1 {
			break
		}
		size, err := strconv.ParseInt(strings.TrimSpace(body[pos:pos+lineEnd]), 16, 64)
		if err != nil || size == 0 {
			break
		}
		pos += lineEnd + 2
		if int(size) > len(body)-pos {
			break
		}
		result.WriteString(body[pos : pos+int(size)])
		pos += int(size) + 2
	}
	if result.Len() == 0 {
		return body
	}
	return result.String()
}

// ── TCP ping through proxy (CONNECT timing) ───────────────────────────────────

func TcpPingThrough(proxy Proxy, host string, port int, timeout time.Duration) *int {
	start := time.Now()
	addr := fmt.Sprintf("%s:%d", proxy.Host, proxy.Port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	authHdr := ""
	if proxy.Username != "" {
		creds := base64.StdEncoding.EncodeToString([]byte(proxy.Username + ":" + proxy.Password))
		authHdr = "Proxy-Authorization: Basic " + creds + "\r\n"
	}
	req := fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n%s\r\n", host, port, host, port, authHdr)
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil
	}

	buf := make([]byte, 512)
	var resp []byte
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			resp = append(resp, buf[:n]...)
		}
		if bytes.Contains(resp, []byte("\r\n\r\n")) {
			break
		}
		if err != nil {
			break
		}
	}

	if !bytes.HasPrefix(resp, []byte("HTTP/1.1 200")) && !bytes.HasPrefix(resp, []byte("HTTP/1.0 200")) {
		return nil
	}
	ms := int(time.Since(start).Milliseconds())
	return &ms
}

// ── Core proxy test ───────────────────────────────────────────────────────────

func TestProxyOnce(proxy Proxy, targetURL string, timeout time.Duration) ProxyAttempt {
	start := time.Now()
	fail := func() ProxyAttempt {
		return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds())}
	}

	parsed, err := parseURL(targetURL)
	if err != nil {
		return fail()
	}

	isHTTPS := parsed.scheme == "https"
	targetHost := parsed.host
	targetPort := parsed.port
	targetPath := parsed.path

	proxyAddr := fmt.Sprintf("%s:%d", proxy.Host, proxy.Port)

	// For SOCKS proxies, delegate to specialized handler
	if proxy.Protocol == "socks5" || proxy.Protocol == "socks4" {
		return testProxySOCKS(proxy, targetHost, targetPort, targetPath, isHTTPS, timeout, start)
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return fail()
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	bytesSent := 0
	bytesReceived := 0

	authHdr := ""
	if proxy.Username != "" {
		creds := base64.StdEncoding.EncodeToString([]byte(proxy.Username + ":" + proxy.Password))
		authHdr = "Proxy-Authorization: Basic " + creds + "\r\n"
	}

	if isHTTPS {
		// Step 1: CONNECT
		connectReq := fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n%s\r\n",
			targetHost, targetPort, targetHost, targetPort, authHdr)
		conn.Write([]byte(connectReq))
		bytesSent += len(connectReq)

		// Read CONNECT response
		var connectBuf []byte
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				bytesReceived += n
				connectBuf = append(connectBuf, buf[:n]...)
			}
			if bytes.Contains(connectBuf, []byte("\r\n\r\n")) {
				break
			}
			if err != nil {
				return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds()), BytesSent: bytesSent, BytesReceived: bytesReceived}
			}
		}

		connectStr := string(connectBuf)
		if !strings.HasPrefix(connectStr, "HTTP/1.1 200") && !strings.HasPrefix(connectStr, "HTTP/1.0 200") {
			return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds()), BytesSent: bytesSent, BytesReceived: bytesReceived}
		}

		// Step 2: TLS handshake
		tlsCfg := &tls.Config{
			ServerName:         targetHost,
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1", "h2"},
		}
		tlsConn := tls.Client(conn, tlsCfg)
		tlsConn.SetDeadline(time.Now().Add(timeout))
		if err := tlsConn.Handshake(); err != nil {
			return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds()), BytesSent: bytesSent, BytesReceived: bytesReceived}
		}

		h2Support := tlsConn.ConnectionState().NegotiatedProtocol == "h2"

		// SSL inspection check: cert CN should match target domain
		sslInspected := false
		cert := tlsConn.ConnectionState().PeerCertificates
		if len(cert) > 0 {
			cn := cert[0].Subject.CommonName
			cn = strings.TrimPrefix(cn, "*.")
			domainParts := strings.Split(targetHost, ".")
			if len(domainParts) >= 2 {
				domainBase := strings.Join(domainParts[len(domainParts)-2:], ".")
				sslInspected = !strings.HasSuffix(domainBase, cn) && !strings.HasSuffix(cn, domainBase)
			}
		}

		// Step 3: GET request
		getReq := fmt.Sprintf(
			"GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\r\nAccept: text/html,application/xhtml+xml\r\nAccept-Language: en-US,en;q=0.9\r\nConnection: close\r\n\r\n",
			targetPath, targetHost)
		tlsConn.Write([]byte(getReq))
		bytesSent += len(getReq)

		raw, err := io.ReadAll(tlsConn)
		if err != nil && len(raw) == 0 {
			return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds()), BytesSent: bytesSent, BytesReceived: bytesReceived}
		}
		bytesReceived += len(raw)

		return parseHTTPResponse(raw, bytesSent, bytesReceived, h2Support, sslInspected, start)

	} else {
		// Plain HTTP — GET directly through proxy
		getReq := fmt.Sprintf(
			"GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\r\nAccept: text/html,application/xhtml+xml\r\nConnection: close\r\n%s\r\n",
			targetURL, targetHost, authHdr)
		conn.Write([]byte(getReq))
		bytesSent += len(getReq)

		raw, err := io.ReadAll(conn)
		if err != nil && len(raw) == 0 {
			return fail()
		}
		bytesReceived += len(raw)

		return parseHTTPResponse(raw, bytesSent, bytesReceived, false, false, start)
	}
}

// ── SOCKS proxy handler ───────────────────────────────────────────────────────

func testProxySOCKS(proxy Proxy, targetHost string, targetPort int, targetPath string, isHTTPS bool, timeout time.Duration, start time.Time) ProxyAttempt {
	fail := func() ProxyAttempt {
		return ProxyAttempt{OK: false, Ms: int(time.Since(start).Milliseconds())}
	}

	proxyAddr := fmt.Sprintf("%s:%d", proxy.Host, proxy.Port)
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return fail()
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	if proxy.Protocol == "socks5" {
		if err := socks5Handshake(conn, proxy, targetHost, targetPort); err != nil {
			return fail()
		}
	} else {
		if err := socks4Handshake(conn, targetHost, targetPort); err != nil {
			return fail()
		}
	}

	bytesSent := 0
	bytesReceived := 0
	var rawConn io.ReadWriter = conn

	if isHTTPS {
		tlsCfg := &tls.Config{
			ServerName:         targetHost,
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1", "h2"},
		}
		tlsConn := tls.Client(conn, tlsCfg)
		tlsConn.SetDeadline(time.Now().Add(timeout))
		if err := tlsConn.Handshake(); err != nil {
			return fail()
		}
		rawConn = tlsConn
	}

	fullURL := fmt.Sprintf("https://%s%s", targetHost, targetPath)
	if !isHTTPS {
		fullURL = fmt.Sprintf("http://%s%s", targetHost, targetPath)
	}

	getReq := fmt.Sprintf(
		"GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36\r\nAccept: text/html,application/xhtml+xml\r\nConnection: close\r\n\r\n",
		fullURL, targetHost)
	rawConn.Write([]byte(getReq))
	bytesSent += len(getReq)

	raw, _ := io.ReadAll(rawConn)
	bytesReceived += len(raw)

	return parseHTTPResponse(raw, bytesSent, bytesReceived, false, false, start)
}

func socks5Handshake(conn net.Conn, proxy Proxy, host string, port int) error {
	// Auth negotiation
	if proxy.Username != "" {
		conn.Write([]byte{5, 2, 0, 2}) // version 5, 2 methods: no-auth + user/pass
	} else {
		conn.Write([]byte{5, 1, 0}) // version 5, 1 method: no-auth
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 5 {
		return fmt.Errorf("not a SOCKS5 server")
	}
	if resp[1] == 2 && proxy.Username != "" {
		// Username/password auth
		uLen := len(proxy.Username)
		pLen := len(proxy.Password)
		authReq := append([]byte{1, byte(uLen)}, []byte(proxy.Username)...)
		authReq = append(authReq, byte(pLen))
		authReq = append(authReq, []byte(proxy.Password)...)
		conn.Write(authReq)
		authResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, authResp); err != nil {
			return err
		}
		if authResp[1] != 0 {
			return fmt.Errorf("SOCKS5 auth failed")
		}
	}

	// Connect request
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port&0xff))
	conn.Write(req)

	connResp := make([]byte, 10)
	if _, err := io.ReadFull(conn, connResp); err != nil {
		return err
	}
	if connResp[1] != 0 {
		return fmt.Errorf("SOCKS5 connect failed: %d", connResp[1])
	}
	return nil
}

func socks4Handshake(conn net.Conn, host string, port int) error {
	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("DNS lookup failed: %w", err)
	}
	ipParts := strings.Split(ips[0], ".")
	if len(ipParts) != 4 {
		return fmt.Errorf("invalid IP")
	}
	req := []byte{4, 1, byte(port >> 8), byte(port & 0xff)}
	for _, p := range ipParts {
		n, _ := strconv.Atoi(p)
		req = append(req, byte(n))
	}
	req = append(req, 0) // null userid

	conn.Write(req)
	resp := make([]byte, 8)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[1] != 90 {
		return fmt.Errorf("SOCKS4 connect rejected: %d", resp[1])
	}
	return nil
}

// ── HTTP response parser ──────────────────────────────────────────────────────

func parseHTTPResponse(raw []byte, bytesSent, bytesReceived int, h2Support, sslInspected bool, start time.Time) ProxyAttempt {
	ms := int(time.Since(start).Milliseconds())
	rawStr := string(raw)

	headerEnd := strings.Index(rawStr, "\r\n\r\n")
	statusLine := ""
	if idx := strings.Index(rawStr, "\r\n"); idx != -1 {
		statusLine = rawStr[:idx]
	}

	statusCode := 0
	statusRe := regexp.MustCompile(`HTTP/1\.[01] (\d+)`)
	if m := statusRe.FindStringSubmatch(statusLine); m != nil {
		statusCode, _ = strconv.Atoi(m[1])
	}

	headers := ""
	body := ""
	if headerEnd >= 0 {
		headers = rawStr[:headerEnd]
		body = rawStr[headerEnd+4:]
	}

	if regexp.MustCompile(`(?i)transfer-encoding:\s*chunked`).MatchString(headers) {
		body = decodeChunked(body)
	}

	ok := statusCode >= 200 && statusCode < 500

	// Antibot detection
	botVendor := DetectBotVendor(body, headers, statusCode)
	edgeCDN := DetectEdgeCDN(headers)
	cookies, hasClearance := ExtractAntibotCookies(headers)
	pxRisk := ExtractPxRiskScore(body, botVendor)
	bl := strings.ToLower(body)
	pxChallenge := false
	for _, m := range PxMarkers {
		if strings.Contains(bl, m) {
			pxChallenge = true
			break
		}
	}

	return ProxyAttempt{
		OK:                  ok,
		Ms:                  ms,
		Body:                body,
		BytesSent:           bytesSent,
		BytesReceived:       bytesReceived,
		PxChallenge:         pxChallenge,
		HTTPStatus:          statusCode,
		BotVendor:           botVendor,
		EdgeCDN:             edgeCDN,
		AntibotCookies:      cookies,
		AntibotHasClearance: hasClearance,
		PxRiskScore:         pxRisk,
		H2Support:           h2Support,
		SslInspected:        sslInspected,
		ResponseSize:        len(body),
		HeadersRaw:          headers,
	}
}

// ── ip-api response parser ────────────────────────────────────────────────────

func ParseIpApiResponse(body string) *IpApiResponse {
	if body == "" {
		return nil
	}
	s := strings.Index(body, "{")
	e := strings.LastIndex(body, "}")
	if s == -1 || e == -1 || e <= s {
		return nil
	}
	jsonStr := body[s : e+1]

	var data IpApiResponse
	if err := jsonUnmarshal([]byte(jsonStr), &data); err != nil {
		return nil
	}
	if data.Query == "" {
		return nil
	}
	return &data
}

// ── Multi-retry proxy test ────────────────────────────────────────────────────

func TestProxy(proxy Proxy, testURL string, timeout time.Duration, retries int, targetURLs []TargetEndpoint, skipHttpbin bool) ProxyResult {
	var ipapiAttempts []ProxyAttempt
	var httpbinAttempts []ProxyAttempt
	allTargetAttempts := map[string][]ProxyAttempt{}
	for _, t := range targetURLs {
		allTargetAttempts[t.Key] = nil
	}
	totalBytesSent := 0
	totalBytesRecv := 0

	for i := 0; i < retries; i++ {
		type taskDef struct {
			key string
			url string
		}

		// Phase 1: ipapi + httpbin in parallel (different domains, no rate-limit risk)
		phase1 := []taskDef{{key: "ipapi", url: testURL}}
		if !skipHttpbin {
			phase1 = append(phase1, taskDef{key: "httpbin", url: HttpbinURL})
		}
		p1results := make([]ProxyAttempt, len(phase1))
		p1done := make(chan int, len(phase1))
		for idx, td := range phase1 {
			go func(i int, u string) {
				p1results[i] = TestProxyOnce(proxy, u, timeout)
				p1done <- i
			}(idx, td.url)
		}
		for range phase1 {
			<-p1done
		}

		byKey := map[string]ProxyAttempt{}
		for idx, td := range phase1 {
			byKey[td.key] = p1results[idx]
			totalBytesSent += p1results[idx].BytesSent
			totalBytesRecv += p1results[idx].BytesReceived
		}

		// Phase 2: target endpoints sequentially with per-endpoint jitter
		// Reduces concurrent connections to target sites from (concurrency × endpoints)
		// down to just concurrency, drastically cutting rate-limit hits.
		for j, t := range targetURLs {
			if j > 0 {
				// 100–350 ms jitter between endpoints for the same proxy
				time.Sleep(time.Duration(100+rand.Intn(250)) * time.Millisecond)
			}
			a := TestProxyOnce(proxy, t.URL, timeout)
			byKey["t_"+t.Key] = a
			totalBytesSent += a.BytesSent
			totalBytesRecv += a.BytesReceived
		}

		ipapiAttempts = append(ipapiAttempts, byKey["ipapi"])
		if a, ok := byKey["httpbin"]; ok {
			httpbinAttempts = append(httpbinAttempts, a)
		}
		for _, t := range targetURLs {
			if a, ok := byKey["t_"+t.Key]; ok {
				allTargetAttempts[t.Key] = append(allTargetAttempts[t.Key], a)
			}
		}
	}

	// Good ipapi attempts
	var goodIpapi []ProxyAttempt
	for _, a := range ipapiAttempts {
		if a.OK {
			goodIpapi = append(goodIpapi, a)
		}
	}
	var goodHttpbin []ProxyAttempt
	for _, a := range httpbinAttempts {
		if a.OK {
			goodHttpbin = append(goodHttpbin, a)
		}
	}

	// Per-endpoint result map
	targetResults := map[string]EndpointResult{}
	for _, t := range targetURLs {
		attempts := allTargetAttempts[t.Key]
		var good []ProxyAttempt
		for _, a := range attempts {
			if a.OK {
				good = append(good, a)
			}
		}
		var lastRef *ProxyAttempt
		if len(good) > 0 {
			lastRef = &good[len(good)-1]
		} else if len(attempts) > 0 {
			lastRef = &attempts[len(attempts)-1]
		}

		pxHit := false
		for _, a := range attempts {
			if a.PxChallenge {
				pxHit = true
			}
		}

		var avgMs *int
		if len(good) > 0 {
			sum := 0
			for _, a := range good {
				sum += a.Ms
			}
			v := sum / len(good)
			avgMs = &v
		}

		ep := EndpointResult{
			Label:          t.Label,
			URL:            t.URL,
			Pass:           len(good) > 0,
			Px:             pxHit,
			Ms:             avgMs,
			AntibotCookies: []AntibotCookie{},
		}
		if lastRef != nil {
			if lastRef.HTTPStatus != 0 {
				v := lastRef.HTTPStatus
				ep.HTTPStatus = &v
			}
			ep.BotVendor = lastRef.BotVendor
			ep.AntibotCookies = lastRef.AntibotCookies
			ep.EdgeCDN = lastRef.EdgeCDN
		}
		targetResults[t.Key] = ep
	}

	// Aggregate backwards-compat fields
	allEpResults := make([]EndpointResult, 0, len(targetResults))
	for _, ep := range targetResults {
		allEpResults = append(allEpResults, ep)
	}

	var allEpAttempts []ProxyAttempt
	for _, attempts := range allTargetAttempts {
		allEpAttempts = append(allEpAttempts, attempts...)
	}

	targetTested := len(targetURLs) > 0
	var targetPass *bool
	if targetTested {
		tp := false
		for _, ep := range allEpResults {
			if ep.Pass {
				tp = true
			}
		}
		targetPass = &tp
	}

	pxChallenge := false
	for _, a := range append(ipapiAttempts, append(httpbinAttempts, allEpAttempts...)...) {
		if a.PxChallenge {
			pxChallenge = true
		}
	}
	targetPx := false
	for _, a := range allEpAttempts {
		if a.PxChallenge {
			targetPx = true
		}
	}

	var passingMs []int
	for _, ep := range allEpResults {
		if ep.Pass && ep.Ms != nil {
			passingMs = append(passingMs, *ep.Ms)
		}
	}
	var targetMs *int
	if len(passingMs) > 0 {
		sum := 0
		for _, m := range passingMs {
			sum += m
		}
		v := sum / len(passingMs)
		targetMs = &v
	}

	// Best representative attempt
	var bestRef *ProxyAttempt
	if len(allEpAttempts) > 0 {
		var goodAll []ProxyAttempt
		for _, a := range allEpAttempts {
			if a.OK {
				goodAll = append(goodAll, a)
			}
		}
		if len(goodAll) > 0 {
			ref := goodAll[len(goodAll)-1]
			bestRef = &ref
		} else {
			ref := allEpAttempts[len(allEpAttempts)-1]
			bestRef = &ref
		}
	}

	var targetBotVendor string
	var targetStatus *int
	var targetResponseSize *int
	var edgeCDN string
	var pxRiskScore *int
	h2Support := false
	sslInspected := false

	if bestRef != nil {
		targetBotVendor = bestRef.BotVendor
		if bestRef.HTTPStatus != 0 {
			v := bestRef.HTTPStatus
			targetStatus = &v
		}
		if bestRef.ResponseSize > 0 {
			v := bestRef.ResponseSize
			targetResponseSize = &v
		}
		edgeCDN = bestRef.EdgeCDN
		pxRiskScore = bestRef.PxRiskScore
	}

	// Merge antibot cookies from all endpoints
	cookieMap := map[string]AntibotCookie{}
	for _, ep := range allEpResults {
		for _, c := range ep.AntibotCookies {
			cookieMap[c.Name] = c
			if ep.EdgeCDN != "" && edgeCDN == "" {
				edgeCDN = ep.EdgeCDN
			}
		}
	}
	antibotCookies := make([]AntibotCookie, 0, len(cookieMap))
	for _, c := range cookieMap {
		antibotCookies = append(antibotCookies, c)
	}
	antibotHasClearance := false
	for _, c := range antibotCookies {
		if c.Type == "clearance" {
			antibotHasClearance = true
		}
	}

	// Edge CDN fallback
	if edgeCDN == "" {
		for _, a := range allEpAttempts {
			if a.EdgeCDN != "" {
				edgeCDN = a.EdgeCDN
				break
			}
		}
	}

	all := append(ipapiAttempts, append(httpbinAttempts, allEpAttempts...)...)
	for _, a := range all {
		if a.H2Support {
			h2Support = true
		}
		if a.SslInspected {
			sslInspected = true
		}
	}

	// Anonymity level
	anonLevel := ""
	if len(goodHttpbin) > 0 {
		anonLevel = DetectAnonymityLevel(goodHttpbin[len(goodHttpbin)-1].Body)
	}

	var httpbinPass *bool
	if !skipHttpbin {
		hp := len(goodHttpbin) > 0
		httpbinPass = &hp
	}

	// Fail path
	if len(goodIpapi) == 0 {
		return ProxyResult{
			Host: proxy.Host, Port: proxy.Port, Protocol: proxy.Protocol,
			Username: proxy.Username, Password: proxy.Password,
			Status: "fail", IpType: "unknown", Score: 0,
			HttpbinPass: httpbinPass, TargetPass: targetPass, TargetMs: targetMs, TargetPx: targetPx,
			BytesSent: totalBytesSent, BytesReceived: totalBytesRecv,
			PxChallenge: pxChallenge, Rotating: false, SslInspected: false,
			TargetBotVendor: targetBotVendor, TargetStatus: targetStatus,
			TargetResponseSize: targetResponseSize,
			EdgeCDN: edgeCDN, AnonLevel: anonLevel,
			AntibotCookies: antibotCookies, AntibotHasClearance: antibotHasClearance,
			PxRiskScore: pxRiskScore, H2Support: h2Support,
			TargetResults: targetResults,
		}
	}

	// Latency stats from good ipapi attempts
	lats := make([]int, len(goodIpapi))
	for i, a := range goodIpapi {
		lats[i] = a.Ms
	}
	avgMs := 0
	for _, l := range lats {
		avgMs += l
	}
	avgMs /= len(lats)
	minMs := lats[0]
	for _, l := range lats {
		if l < minMs {
			minMs = l
		}
	}
	successRate := float64(len(goodIpapi)) / float64(retries)

	// Parse ip info
	var ipInfo *IpApiResponse
	for i := len(goodIpapi) - 1; i >= 0; i-- {
		if info := ParseIpApiResponse(goodIpapi[i].Body); info != nil {
			ipInfo = info
			break
		}
	}
	ipType := ClassifyIP(ipInfo)
	asnNum := 0
	if ipInfo != nil {
		asnNum = ExtractASNNum(ipInfo.AS)
	}
	isDcAsn := asnNum != 0 && DatacenterASNs[asnNum]

	// Edge RTT
	var edgeRtt *int
	if len(targetURLs) > 0 {
		firstTarget, _ := parseURL(targetURLs[0].URL)
		if firstTarget != nil {
			edgeRtt = TcpPingThrough(proxy, firstTarget.host, firstTarget.port, minDuration(timeout, 5*time.Second))
		}
	}

	// Rotating detection
	var egressIPs []string
	for _, a := range goodIpapi {
		if info := ParseIpApiResponse(a.Body); info != nil {
			egressIPs = append(egressIPs, info.Query)
		}
	}
	uniqueEgress := uniqueStrings(egressIPs)
	rotating := len(egressIPs) >= 2 && len(uniqueEgress) > 1

	// Score calculation
	scoreWeights := loadScoreWeights()
	w := scoreWeights
	wTotal := w.Speed + w.Reliability + w.Target + w.IpType + w.AntiBot
	if wTotal <= 0 {
		wTotal = 100
	}

	speedScore := float64(avgMs)
	if avgMs == 0 {
		speedScore = 50
	} else {
		speedScore = maxF(0, minF(100, 100-float64(avgMs-200)/18.0))
	}
	reliabilityScore := successRate * 100
	targetScore := 50.0
	if targetTested {
		if targetPass != nil && *targetPass {
			targetScore = 100
		} else {
			targetScore = 0
		}
	}

	ipTypeScores := map[string]float64{
		"residential": 100, "mobile": 85, "unknown": 40,
		"flagged_proxy": 10, "datacenter": 5, "private": 0,
	}
	ipTypeScore := 40.0
	if v, ok := ipTypeScores[ipType]; ok {
		ipTypeScore = v
	}
	if isDcAsn && ipTypeScore > 10 {
		ipTypeScore = 10
	}

	antiBotScore := 100.0
	if antibotHasClearance {
		antiBotScore = 90
	} else if pxChallenge || targetBotVendor != "" {
		if targetPass != nil && *targetPass {
			antiBotScore = 35
		} else {
			antiBotScore = 0
		}
	}

	score := int(
		(float64(w.Speed)*speedScore + float64(w.Reliability)*reliabilityScore +
			float64(w.Target)*targetScore + float64(w.IpType)*ipTypeScore +
			float64(w.AntiBot)*antiBotScore) / float64(wTotal),
	)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	// Build IpInfo
	var ipInfoOut *IpInfo
	var egressIP string
	if ipInfo != nil {
		egressIP = ipInfo.Query
		ipInfoOut = &IpInfo{
			ISP: ipInfo.ISP, Org: ipInfo.Org, AS: ipInfo.AS, ASName: ipInfo.ASName,
			City: ipInfo.City, Region: ipInfo.RegionName,
			Country: ipInfo.Country, CountryCode: ipInfo.CountryCode,
			Hosting: ipInfo.Hosting, Proxy: ipInfo.Proxy, Mobile: ipInfo.Mobile,
		}
	}

	return ProxyResult{
		Host: proxy.Host, Port: proxy.Port, Protocol: proxy.Protocol,
		Username: proxy.Username, Password: proxy.Password,
		Status:      "pass",
		AvgMs:       &avgMs,
		MinMs:       &minMs,
		SuccessRate: successRate,
		Score:       score,
		EgressIP:    egressIP,
		IpInfo:      ipInfoOut,
		IpType:      ipType,
		HttpbinPass: httpbinPass,
		TargetPass:  targetPass, TargetMs: targetMs, TargetPx: targetPx,
		BytesSent: totalBytesSent, BytesReceived: totalBytesRecv,
		PxChallenge: pxChallenge, EdgeRtt: edgeRtt,
		Rotating: rotating, SslInspected: sslInspected,
		TargetBotVendor: targetBotVendor, TargetStatus: targetStatus,
		TargetResponseSize: targetResponseSize,
		EdgeCDN: edgeCDN, AnonLevel: anonLevel,
		AntibotCookies: antibotCookies, AntibotHasClearance: antibotHasClearance,
		PxRiskScore: pxRiskScore, H2Support: h2Support,
		TargetResults: targetResults,
	}
}

// ── URL parser helper ─────────────────────────────────────────────────────────

type parsedURL struct {
	scheme string
	host   string
	port   int
	path   string
}

func parseURL(rawURL string) (*parsedURL, error) {
	isHTTPS := strings.HasPrefix(rawURL, "https://")
	isHTTP := strings.HasPrefix(rawURL, "http://")
	if !isHTTPS && !isHTTP {
		return nil, fmt.Errorf("unsupported scheme: %s", rawURL)
	}

	scheme := "http"
	rest := rawURL[7:]
	defaultPort := 80
	if isHTTPS {
		scheme = "https"
		rest = rawURL[8:]
		defaultPort = 443
	}

	// Split host from path
	slashIdx := strings.IndexByte(rest, '/')
	hostPart := rest
	pathPart := "/"
	if slashIdx != -1 {
		hostPart = rest[:slashIdx]
		pathPart = rest[slashIdx:]
	}

	// Query string
	if qIdx := strings.IndexByte(pathPart, '?'); qIdx != -1 {
		pathPart = pathPart // keep it
	}

	// Port
	port := defaultPort
	if colonIdx := strings.LastIndexByte(hostPart, ':'); colonIdx != -1 {
		p, err := strconv.Atoi(hostPart[colonIdx+1:])
		if err == nil {
			port = p
			hostPart = hostPart[:colonIdx]
		}
	}

	return &parsedURL{scheme: scheme, host: hostPart, port: port, path: pathPart}, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func uniqueStrings(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// ── Buffered reader (used internally) ────────────────────────────────────────

func readLines(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
