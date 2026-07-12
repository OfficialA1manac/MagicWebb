// Package media normalizes NFT metadata/image URIs and fetches remote content safely.
package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxFetchBytes = 8 << 20

// IPFS gateways tried in order when resolving ipfs:// or bare CIDs.
// No paid gateways (Pinata removed). Free public IPFS gateways only;
// assets are self-hosted into the local imagestore on first fetch
// so the frontend never depends on any gateway at render time.
var ipfsGateways = []string{
	"https://ipfs.io/ipfs/",
	"https://dweb.link/ipfs/",
	"https://w3s.link/ipfs/",
	"https://nftstorage.link/ipfs/",
}

var fetchClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if !ProxyAllowedContext(req.Context(), req.URL.String()) {
			return fmt.Errorf("redirect blocked")
		}
		return nil
	},
	// Custom Transport binds the connection to the IP that ProxyAllowedContext
	// already vetted. Without this, the default transport re-resolves DNS at
	// dial time, opening a TOCTOU/dns-rebinding window (attacker domain flips
	// its A record between the pre-check and the dial).
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// Resolver is the subset of net.Resolver that safeDialContext uses. It is
// declared as an interface so tests can swap in a fake and verify the
// re-validation step without touching real DNS.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// dialResolver is replaceable from tests so safeDialContext can be exercised
// against deterministic fake DNS results without touching the real network.
//
// IMPORTANT: tests that touch dialResolver / dialTCP must NOT call
// t.Parallel() — both are package-level mutable vars and concurrent stubs
// would race.
var dialResolver Resolver = net.DefaultResolver

// dialFunc is the shape of the TCP dial step. Wrapped behind a package var so
// tests can swap in a stub to assert happy-eyeballs fallback behavior without
// touching the OS dialer. (Same non-parallel rule as dialResolver.)
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var dialTCP dialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 10 * time.Second}).DialContext(ctx, network, addr)
}

func parseIPLiteral(host string) (netip.Addr, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap(), true
	}
	return netip.Addr{}, false
}

// safeDialContext re-resolves the target at dial time and refuses any private,
// loopback, link-local, multicast or unspecified address. It then dials the
// first validated IP directly so Go's HTTP transport cannot perform a second
// (untrusted) DNS lookup between this check and the actual TCP connect. TLS
// SNI / cert verification are unaffected — the http.Transport wraps the
// connection with tls.Config.ServerName=req.URL.Host, regardless of the IP.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// Defense-in-depth: reject percent-encoded hosts (e.g. IPv6 zone-id
	// tricks like `fe80::1%25en0`) before either branch decides.
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("dial blocked: percent-encoded host %s", host)
	}

	var ips []netip.Addr
	if ip, ok := parseIPLiteral(host); ok {
		if !publicAddrAllowed(ip) {
			return nil, fmt.Errorf("dial blocked: disallowed IP %s", host)
		}
		ips = []netip.Addr{ip}
	} else {
		if !proxyHostAllowed(host) {
			return nil, fmt.Errorf("dial blocked: disallowed host %s", host)
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resolved, err := dialResolver.LookupNetIP(lookupCtx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(resolved) == 0 {
			return nil, fmt.Errorf("no addresses for %s", host)
		}
		for _, ip := range resolved {
			if !publicAddrAllowed(ip) {
				return nil, fmt.Errorf("dial blocked: %s resolves to disallowed address", host)
			}
		}
		ips = resolved
	}

	// Iterate over every vetted A/AAAA record and dial each in turn. This
	// recreates Happy Eyeballs manually, which is otherwise bypassed when
	// the transport is called with a pre-resolved IP. If only one record
	// exists, the loop runs once. SSRF posture is unchanged — every IP in
	// `ips` has already passed publicAddrAllowed above. We join every
	// per-IP error so operators can see *which* IPs failed when diagnose
	// Fly.io edge routing issues from logs.
	errs := make([]error, 0, len(ips))
	for _, ip := range ips {
		conn, err := dialTCP(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("dial %s: %w", ip.String(), err))
	}
	return nil, errors.Join(errs...)
}

func isBareIPFSCID(uri string) bool {
	if strings.HasPrefix(uri, "Qm") && len(uri) >= 44 {
		return true
	}
	if strings.HasPrefix(uri, "baf") && len(uri) >= 59 {
		return true
	}
	return false
}

// ResolveURI normalizes ipfs://, bare CIDs, ERC-1155 {id} placeholders, and data: URIs.
func ResolveURI(uri, tokenID string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	if strings.Contains(uri, "{id}") {
		if id, ok := new(big.Int).SetString(tokenID, 10); ok {
			padded := make([]byte, 32)
			id.FillBytes(padded)
			uri = strings.ReplaceAll(uri, "{id}", hex.EncodeToString(padded))
		}
	}
	switch {
	case strings.HasPrefix(uri, "data:"):
		return uri
	case strings.HasPrefix(uri, "ipfs://ipfs/"):
		return ipfsGateways[0] + strings.TrimPrefix(uri, "ipfs://ipfs/")
	case strings.HasPrefix(uri, "ipfs://"):
		return ipfsGateways[0] + strings.TrimPrefix(uri, "ipfs://")
	case isBareIPFSCID(uri):
		return ipfsGateways[0] + uri
	case strings.HasPrefix(uri, "ar://"):
		return "https://arweave.net/" + strings.TrimPrefix(uri, "ar://")
	}
	return uri
}

// ResolveCandidates returns ordered URLs to try for a metadata or image URI.
func ResolveCandidates(uri, tokenID string) []string {
	primary := ResolveURI(uri, tokenID)
	if primary == "" {
		return nil
	}
	cid := extractIPFSCID(uri)
	if cid == "" {
		return []string{primary}
	}
	seen := map[string]bool{primary: true}
	out := []string{primary}
	for _, gw := range ipfsGateways {
		u := gw + cid
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func extractIPFSCID(uri string) string {
	uri = strings.TrimSpace(uri)
	switch {
	case strings.HasPrefix(uri, "ipfs://ipfs/"):
		return strings.TrimPrefix(uri, "ipfs://ipfs/")
	case strings.HasPrefix(uri, "ipfs://"):
		return strings.TrimPrefix(uri, "ipfs://")
	case isBareIPFSCID(uri):
		return uri
	}
	if u, err := url.Parse(uri); err == nil {
		if strings.Contains(u.Host, "ipfs") || strings.Contains(u.Host, "dweb.link") {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 && parts[0] == "ipfs" {
				return parts[1]
			}
			if len(parts) >= 1 && isBareIPFSCID(parts[len(parts)-1]) {
				return parts[len(parts)-1]
			}
		}
	}
	return ""
}

// FetchBytes downloads a URI, trying IPFS gateway fallbacks when applicable.
func FetchBytes(ctx context.Context, uri, tokenID string) ([]byte, error) {
	if strings.HasPrefix(uri, "data:") {
		return decodeDataURI(uri)
	}
	var lastErr error
	for _, u := range ResolveCandidates(uri, tokenID) {
		if !strings.HasPrefix(u, "http") {
			lastErr = fmt.Errorf("unsupported uri scheme")
			continue
		}
		body, err := httpGet(ctx, u)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no fetch candidates")
	}
	return nil, lastErr
}

func decodeDataURI(raw string) ([]byte, error) {
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return nil, fmt.Errorf("malformed data uri")
	}
	meta, payload := raw[:comma], raw[comma+1:]
	if strings.Contains(meta, ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	// Many contracts emit percent-encoded data: URIs (e.g.
	// data:image/svg+xml,%3Csvg%20...). url.PathUnescape handles both
	// %HH escapes and + → space (the latter is technically query-string
	// encoding, but is harmless for SVG/XML payloads).
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("data uri unescape: %w", err)
	}
	return []byte(decoded), nil
}

func httpGet(ctx context.Context, u string) ([]byte, error) {
	if !ProxyAllowedContext(ctx, u) {
		return nil, fmt.Errorf("url not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, image/*, */*")
	req.Header.Set("User-Agent", "MagicWebb/1.0")
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFetchBytes {
		return nil, fmt.Errorf("response too large")
	}
	return body, nil
}

// ProxyAllowed reports whether a URL has an allowed scheme and host syntax.
func ProxyAllowed(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return proxyHostAllowed(u.Hostname())
}

// ProxyAllowedContext additionally resolves DNS and rejects hosts that resolve
// to private/link-local/reserved IPs. It is used immediately before every fetch,
// including redirects, to avoid SSRF through DNS rebinding.
func ProxyAllowedContext(ctx context.Context, raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !proxyHostAllowed(host) {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		if !publicAddrAllowed(addr) {
			return false
		}
	}
	return true
}

func proxyHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return false
	}
	if strings.Contains(host, "%") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return publicAddrAllowed(addr)
	}
	if !strings.Contains(host, ".") {
		return false
	}
	return true
}

// cgnat10 is the 100.64.0.0/10 CGNAT prefix (RFC 6598). Carrier-grade NAT
// ranges are not globally routable and should not be proxied-to.
var cgnat10 = netip.MustParsePrefix("100.64.0.0/10")

func publicAddrAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() &&
		!addr.IsLoopback() &&
		!addr.IsPrivate() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified() &&
		// Reject CGNAT (RFC 6598 100.64.0.0/10) used by mobile carriers
		// and some ISPs — these are not globally unique and proxying to
		// them creates an SSRF vector into carrier-internal networks.
		!cgnat10.Contains(addr) &&
		// Reject documentation ranges (TEST-NET-[123]: 192.0.2.0/24,
		// 198.51.100.0/24, 203.0.113.0/24) and the benchmarking range
		// (198.18.0.0/15). These have no legitimate NFT content and
		// proxying to them would serve no purpose.
		!isTestNet(addr)
}

// isTestNet reports whether addr falls within any of the IANA-specified
// documentation / test-net prefixes (RFC 5735, RFC 2544).
func isTestNet(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	switch {
	case netip.MustParsePrefix("192.0.2.0/24").Contains(addr):   // TEST-NET-1
		return true
	case netip.MustParsePrefix("198.51.100.0/24").Contains(addr): // TEST-NET-2
		return true
	case netip.MustParsePrefix("203.0.113.0/24").Contains(addr):  // TEST-NET-3
		return true
	case netip.MustParsePrefix("198.18.0.0/15").Contains(addr):   // Benchmarking (RFC 2544)
		return true
	default:
		return false
	}
}

// SniffImage validates that body begins with a recognised image magic signature
// and returns the corresponding Content-Type header value. It returns ok=false
// for anything that is not image/png, image/jpeg, image/gif, image/webp,
// image/avif, or image/svg+xml. Both the ingest path (indexer writes must be
// valid images) and the serve path (mediaProxy must send a safe Content-Type)
// share this rule so we never advertise a non-image MIME for an image/served
// endpoint.
//
// When built with `-tags zigmedia`, the detection is delegated to the
// Zig-accelerated sniffer (zignsniff_zigmedia.go) which processes the blob
// in a single pass with zero heap allocation. The default build uses the
// Go-native magic-byte chain below.
//
// Kept in the media package so the indexer worker can call it without
// importing api (which would be a dependency-direction violation).
func SniffImage(body []byte) (mime string, ok bool) {
	// When the zigmedia tag is active, sniffer() is the Zig-accelerated
	// version; otherwise it delegates to the Go fallback below.
	if mime, ok := sniffer(body); ok {
		return mime, true
	}
	switch {
	case len(body) >= 8 &&
		body[0] == 0x89 && body[1] == 'P' && body[2] == 'N' && body[3] == 'G' &&
		body[4] == '\r' && body[5] == '\n' && body[6] == 0x1a && body[7] == '\n':
		return "image/png", true
	case len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff:
		return "image/jpeg", true
	case len(body) >= 6 && (string(body[:6]) == "GIF87a" || string(body[:6]) == "GIF89a"):
		return "image/gif", true
	case len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP":
		return "image/webp", true
	case len(body) >= 12 && string(body[4:8]) == "ftyp" &&
		(strings.HasPrefix(string(body[8:12]), "avif") || strings.HasPrefix(string(body[8:12]), "avis")):
		return "image/avif", true
	case isSVG(body):
		return "image/svg+xml", true
	}
	return "", false
}

// isSVG detects SVG documents by looking for the <svg opening tag after
// optional BOM, XML declaration, and/or whitespace. Many on-chain generative
// NFT collections render as SVG, so this is critical for marketplace display.
func isSVG(body []byte) bool {
	s := skipXMLPreamble(body)
	if len(s) < 5 || !strings.EqualFold(string(s[:4]), "<svg") {
		return false
	}
	// Fifth byte must be whitespace, >, or / to reject spurious prefix matches
	// like "<svgTHISISNOTVALID".
	switch s[4] {
	case ' ', '\t', '\n', '\r', '>', '/', '?':
		return true
	}
	return false
}

// skipXMLPreamble advances past an optional UTF-8 BOM (EF BB BF), an optional
// XML declaration (<?xml ... ?>), and leading whitespace so that a bare <svg>
// or <SVG> tag is detected regardless of preamble.
func skipXMLPreamble(body []byte) []byte {
	// Skip UTF-8 BOM
	if len(body) >= 3 && body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF {
		body = body[3:]
	}
	// Skip optional <?xml ... ?> declaration
	if len(body) >= 2 && body[0] == '<' && body[1] == '?' {
		if end := bytes.Index(body, []byte("?>")); end >= 0 {
			body = body[end+2:]
		}
	}
	// Skip leading whitespace
	for len(body) > 0 && (body[0] == ' ' || body[0] == '\t' || body[0] == '\n' || body[0] == '\r') {
		body = body[1:]
	}
	return body
}

// ProxyURL returns a same-origin proxy path for external media, or the original
// URI for data:/relative. Inputs that already name a self-hosted blob
// (`/api/v1/img/<sha>`) pass through verbatim so the frontend never round-
// trips through `/api/v1/media?url=...` with a long encoded query string.
func ProxyURL(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" || strings.HasPrefix(uri, "data:") {
		return uri
	}
	// Self-hosted blob path — serve directly, never round-trip through proxy.
	if strings.HasPrefix(uri, "/api/v1/img/") {
		return uri
	}
	// Other absolute paths (relative to origin) pass through as-is.
	if strings.HasPrefix(uri, "/") {
		return uri
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		return "/api/v1/media?url=" + url.QueryEscape(uri)
	}
	// ipfs and bare CIDs — resolve then proxy the gateway URL.
	resolved := ResolveURI(uri, "")
	if resolved != "" && strings.HasPrefix(resolved, "http") {
		return "/api/v1/media?url=" + url.QueryEscape(resolved)
	}
	return uri
}
