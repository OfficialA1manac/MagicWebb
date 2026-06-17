// Package media normalizes NFT metadata/image URIs and fetches remote content safely.
package media

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
var ipfsGateways = []string{
	"https://cloudflare-ipfs.com/ipfs/",
	"https://dweb.link/ipfs/",
	"https://ipfs.io/ipfs/",
	"https://gateway.pinata.cloud/ipfs/",
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
var dialResolver Resolver = net.DefaultResolver

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

	first := ips[0]
	// 5s dial ceiling — below fetchClient's 10s timeout so the dialer
	// cannot outlast the outer HTTP request deadline.
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 10 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(first.String(), port))
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
	return []byte(payload), nil
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

func publicAddrAllowed(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() &&
		!addr.IsLoopback() &&
		!addr.IsPrivate() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified()
}

// ProxyURL returns a same-origin proxy path for external media, or the original URI for data:/relative.
func ProxyURL(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" || strings.HasPrefix(uri, "data:") || strings.HasPrefix(uri, "/") {
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
