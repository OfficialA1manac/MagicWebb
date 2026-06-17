// Package media normalizes NFT metadata/image URIs and fetches remote content safely.
package media

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
		if !ProxyAllowed(req.URL.String()) {
			return fmt.Errorf("redirect blocked")
		}
		return nil
	},
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
	if !ProxyAllowed(u) {
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
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// ProxyAllowed reports whether a URL may be fetched by the public media proxy.
func ProxyAllowed(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return false
	}
	// Block private/link-local ranges (SSRF guard).
	if strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "169.254.") ||
		host == "0.0.0.0" || host == "::1" {
		return false
	}
	if strings.HasPrefix(host, "172.") {
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			if n := parts[1]; n >= "16" && n <= "31" {
				return false
			}
		}
	}
	return true
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
