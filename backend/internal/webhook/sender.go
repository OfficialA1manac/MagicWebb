// Package webhook provides HTTP POST senders for several webhook formats:
//   - Discord/Slack: accepts a Payload with content + embeds
//   - Prometheus Alertmanager: sends Alertmanager-compatible alert payloads
//
// Retry policy: senders automatically retry on transient errors (5xx, network
// timeout, DNS resolution failure) with exponential backoff: 3 attempts,
// delays 5s → 30s → 120s. Non-retryable errors (4xx client errors) are
// returned immediately. The webhook HMAC secret, when configured, signs every
// outgoing payload with HMAC-SHA256 so receivers can verify the origin.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// DefaultRetryConfig is the standard exponential backoff for webhook sends.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	Delays:      []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second},
}

// RetryConfig controls automatic retry behavior for HTTP webhook sends.
type RetryConfig struct {
	MaxAttempts int
	Delays      []time.Duration // must have len >= MaxAttempts-1
}

// HMACSecret, when non-empty, signs every JSON payload with HMAC-SHA256.
// Receivers can verify the X-Webhook-Signature header to authenticate origin.
var HMACSecret string

// ── Discord/Slack format ────────────────────────────────────────────────

// Payload is a Discord-compatible webhook body. The Content field is rendered
// as the message text (both Discord and Slack); Embeds provide richer cards
// on Discord (ignored by Slack).
type Payload struct {
	Content string  `json:"content"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

// Embed is a Discord-style rich embed card.
type Embed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"` // hex colour as decimal (e.g. 0xE74C3C = 15158332)
}

// SendDiscord POSTs a Discord/Slack-compatible payload to the webhook URL.
func SendDiscord(ctx context.Context, url string, p Payload) error {
	return sendJSON(ctx, url, p)
}

// SendDiscordAlert is a convenience wrapper that constructs a red-coloured
// embed alert with the given title and description fields.
func SendDiscordAlert(ctx context.Context, url, title, description string) error {
	return SendDiscord(ctx, url, Payload{
		Content: fmt.Sprintf("🚨 **%s**", title),
		Embeds: []Embed{{
			Title:       title,
			Description: description,
			Color:       0xE74C3C, // red
		}},
	})
}

// ── Prometheus Alertmanager format ───────────────────────────────────────

// PromAlert is one alert within an Alertmanager webhook payload.
type PromAlert struct {
	Status       string            `json:"status"`       // "firing" | "resolved"
	Labels       map[string]string `json:"labels"`       // e.g. alertname, severity
	Annotations  map[string]string `json:"annotations"`  // summary, description
	StartsAt     string            `json:"startsAt"`     // RFC3339
	EndsAt       string            `json:"endsAt"`       // RFC3339 (zero = still firing)
	GeneratorURL string            `json:"generatorURL"` // link back to source
	Fingerprint  string            `json:"fingerprint,omitempty"`
}

// PromPayload is the top-level Alertmanager webhook JSON body.
type PromPayload struct {
	Version           string            `json:"version"`           // "4"
	GroupKey          string            `json:"groupKey"`          // grouping key for dedup
	TruncatedAlerts   int               `json:"truncatedAlerts"`   // 0 = all alerts included
	Status            string            `json:"status"`            // "firing" | "resolved"
	Receiver          string            `json:"receiver"`          // receiver name
	GroupLabels       map[string]string `json:"groupLabels"`
	CommonLabels      map[string]string `json:"commonLabels"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	ExternalURL       string            `json:"externalURL"`       // Alertmanager UI URL
	Alerts            []PromAlert       `json:"alerts"`
}

// SendPrometheus POSTs an Alertmanager-compatible payload to the webhook URL.
// The payload follows the default Alertmanager webhook schema at
// https://pkg.go.dev/github.com/prometheus/alertmanager/template .
func SendPrometheus(ctx context.Context, url string, p PromPayload) error {
	return sendJSON(ctx, url, p)
}

// SendPrometheusAlert constructs and sends an Alertmanager-compatible alert
// with the given title as alertname, the description as annotation, and the
// given severity label. This is a convenience wrapper so callers don't need
// to build the full PromPayload struct manually.
func SendPrometheusAlert(ctx context.Context, url, alertName, description, severity string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload := PromPayload{
		Version:         "4",
		GroupKey:        fmt.Sprintf("{}:{alertname=\"%s\"}", alertName),
		TruncatedAlerts: 0,
		Status:          "firing",
		Receiver:        "gas-alert",
		GroupLabels: map[string]string{
			"alertname": alertName,
		},
		CommonLabels: map[string]string{
			"alertname": alertName,
			"severity":  severity,
		},
		CommonAnnotations: map[string]string{
			"summary":     fmt.Sprintf("Keeper gas cost alert: %s", alertName),
			"description": description,
		},
		ExternalURL: "",
		Alerts: []PromAlert{{
			Status: "firing",
			Labels: map[string]string{
				"alertname": alertName,
				"severity":  severity,
			},
			Annotations: map[string]string{
				"summary":     fmt.Sprintf("Keeper gas cost alert: %s", alertName),
				"description": description,
			},
			StartsAt: now,
			EndsAt:   "0001-01-01T00:00:00Z", // zero time = still firing
		}},
	}
	return SendPrometheus(ctx, url, payload)
}

// ── Resolved notification helpers (green / "resolved" status) ─────────────────

// SendDiscordResolvedAlert sends a green-coloured embed indicating the gas cost
// has dropped back below the threshold (resolved status).
func SendDiscordResolvedAlert(ctx context.Context, url, title, description string) error {
	return SendDiscord(ctx, url, Payload{
		Content: fmt.Sprintf("✅ **%s**", title),
		Embeds: []Embed{{
			Title:       title,
			Description: description,
			Color:       0x2ECC71, // green
		}},
	})
}

// SendPrometheusResolvedAlert sends an Alertmanager-compatible alert with
// status "resolved" and an endsAt timestamp set to now. This tells
// Alertmanager the alert is no longer firing and can be auto-resolved.
// The Labels/CommonLabels severity MUST match the firing alert's severity
// so Alertmanager links them as the same alert (same fingerprint); a
// different severity creates a separate alert group.
func SendPrometheusResolvedAlert(ctx context.Context, url, alertName, description string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload := PromPayload{
		Version:         "4",
		GroupKey:        fmt.Sprintf("{}:{alertname=\"%s\"}", alertName),
		TruncatedAlerts: 0,
		Status:          "resolved",
		Receiver:        "gas-alert",
		GroupLabels: map[string]string{
			"alertname": alertName,
		},
		CommonLabels: map[string]string{
			"alertname": alertName,
			"severity":  "warning", // MUST match firing alert severity for Alertmanager fingerprint linking
		},
		CommonAnnotations: map[string]string{
			"summary":     fmt.Sprintf("Keeper gas cost resolved: %s", alertName),
			"description": description,
		},
		ExternalURL: "",
		Alerts: []PromAlert{{
			Status: "resolved",
			Labels: map[string]string{
				"alertname": alertName,
				"severity":  "warning", // MUST match firing alert severity
			},
			Annotations: map[string]string{
				"summary":     fmt.Sprintf("Keeper gas cost resolved: %s", alertName),
				"description": description,
			},
			StartsAt: now,
			EndsAt:   now, // resolved immediately
		}},
	}
	return SendPrometheus(ctx, url, payload)
}

// BuildResolvedAlertEmailBody returns a green-themed HTML email body for a
// gas cost resolved notification.
func BuildResolvedAlertEmailBody(title, description, thresholdWei, costWei, thresholdFLR, costFLR, currency string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#0a0a0f;color:#fafafa;margin:0;padding:2rem;}
.container{max-width:600px;margin:0 auto;background:#16161f;border-radius:12px;padding:2rem;border:1px solid rgba(34,197,94,0.3);}
h1{color:#22c55e;font-size:1.25rem;margin:0 0 1rem;}
.stat{display:flex;justify-content:space-between;padding:0.75rem 0;border-bottom:1px solid rgba(255,255,255,0.08);}
.stat:last-child{border-bottom:none;}
.label{color:rgba(255,255,255,0.5);font-size:0.875rem;}
.value{color:#fafafa;font-family:'JetBrains Mono','SF Mono',monospace;font-weight:700;}
.threshold{color:#fbbf24;}
.current{color:#22c55e;}
.footer{margin-top:1.5rem;font-size:0.75rem;color:rgba(255,255,255,0.35);text-align:center;}
</style></head>
<body>
<div class=container>
<h1>✅ %s</h1>
<p style="color:rgba(255,255,255,0.7);margin:0 0 1.5rem;">%s</p>
<div class=stat><span class=label>Threshold</span><span class="value threshold">%s wei (%s %s)</span></div>
<div class=stat><span class=label>Current (24h)</span><span class="value current">%s wei (%s %s)</span></div>
<div class=stat><span class=label>Currency</span><span class=value>%s</span></div>
<div class=footer>MagicWebb Keeper Gas Alert · <a href="https://magicwebb.fly.dev/metrics/gas" style="color:#818cf8;">Gas Dashboard</a></div>
</div>
</body>
</html>`,
		title, description,
		thresholdWei, thresholdFLR, currency,
		costWei, costFLR, currency,
		currency,
	)
}

// ── SMTP Email ──────────────────────────────────────────────────────────

// SendEmail sends an HTML email via SMTP. Requires SMTP host/port, credentials,
// and from/to addresses. Returns an error if SMTP is not configured or the
// send fails.
func SendEmail(ctx context.Context, host string, port int, user, pass, from, to, subject, body string) error {
	if host == "" || user == "" || pass == "" || from == "" || to == "" {
		return fmt.Errorf("smtp: incomplete configuration")
	}

	// Default to 587 (STARTTLS). 465 uses direct SSL — we handle both.
	if port <= 0 {
		port = 587
	}

	auth := smtp.PlainAuth("", user, pass, host)

	// Build MIME message with both plain-text and HTML parts.
	plainBody := stripHTML(body)
	msg := fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: multipart/alternative; boundary=\"boundary123\"\r\n"+
		"\r\n"+
		"--boundary123\r\n"+
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n"+
		"--boundary123\r\n"+
		"Content-Type: text/html; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n"+
		"--boundary123--\r\n",
		from, to, subject, plainBody, body)

	// Choose connection type based on port.
	addr := fmt.Sprintf("%s:%d", host, port)

	// Use a cancellable dial context.
	dialer := &net.Dialer{}
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)

	go func() {
		c, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			errCh <- err
			return
		}
		connCh <- c
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return fmt.Errorf("smtp dial: %w", err)
	case c := <-connCh:
		client, err := smtp.NewClient(c, host)
		if err != nil {
			_ = c.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
		defer client.Close()

		// STARTTLS for port 587; direct SSL for 465 is handled differently.
		if port != 465 {
			tlsConfig := &tls.Config{ServerName: host}
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}

		// Authenticate.
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}

		// Set sender and recipient.
		if err := client.Mail(from); err != nil {
			return fmt.Errorf("smtp mail: %w", err)
		}
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("smtp rcpt: %w", err)
		}

		// Send message body.
		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("smtp data: %w", err)
		}
		if _, err := fmt.Fprint(w, msg); err != nil {
			return fmt.Errorf("smtp write: %w", err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("smtp close: %w", err)
		}

		if err := client.Quit(); err != nil {
			return fmt.Errorf("smtp quit: %w", err)
		}
	}

	return nil
}

// buildAlertEmailBody returns an HTML email body for a gas cost alert.
func BuildAlertEmailBody(title, description, thresholdWei, costWei, thresholdFLR, costFLR, currency string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#0a0a0f;color:#fafafa;margin:0;padding:2rem;}
.container{max-width:600px;margin:0 auto;background:#16161f;border-radius:12px;padding:2rem;border:1px solid rgba(239,68,68,0.3);}
h1{color:#ef4444;font-size:1.25rem;margin:0 0 1rem;}
.stat{display:flex;justify-content:space-between;padding:0.75rem 0;border-bottom:1px solid rgba(255,255,255,0.08);}
.stat:last-child{border-bottom:none;}
.label{color:rgba(255,255,255,0.5);font-size:0.875rem;}
.value{color:#fafafa;font-family:'JetBrains Mono','SF Mono',monospace;font-weight:700;}
.threshold{color:#fbbf24;}
.current{color:#ef4444;}
.footer{margin-top:1.5rem;font-size:0.75rem;color:rgba(255,255,255,0.35);text-align:center;}
</style></head>
<body>
<div class=container>
<h1>🚨 %s</h1>
<p style="color:rgba(255,255,255,0.7);margin:0 0 1.5rem;">%s</p>
<div class=stat><span class=label>Threshold</span><span class="value threshold">%s wei (%s %s)</span></div>
<div class=stat><span class=label>Current (24h)</span><span class="value current">%s wei (%s %s)</span></div>
<div class=stat><span class=label>Currency</span><span class=value>%s</span></div>
<div class=footer>MagicWebb Keeper Gas Alert · <a href="https://magicwebb.fly.dev/metrics/gas" style="color:#818cf8;">Gas Dashboard</a></div>
</div>
</body>
</html>`,
		title, description,
		thresholdWei, thresholdFLR, currency,
		costWei, costFLR, currency,
		currency,
	)
}

// stripHTML removes HTML tags for plain-text fallback.
func stripHTML(html string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			buf.WriteRune(r)
		}
	}
	// Collapse multiple whitespace.
	result := strings.TrimSpace(buf.String())
	parts := strings.Fields(result)
	return strings.Join(parts, " ")
}

// ── Generic JSON POST ───────────────────────────────────────────────────

// sendJSON POSTs any JSON-marshalable value to a URL with a 10-second timeout.
// Non-2xx responses are returned as errors with the response body truncated
// to 256 characters for safe logging.
//
// When DefaultRetryConfig has retries configured and HMACSecret is set, the
// payload is signed and retried on transient failures with exponential backoff.
func sendJSON(ctx context.Context, url string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("webhook marshal: %w", err)
	}

	// Sign the payload with HMAC-SHA256 when a secret is configured.
	// Receivers verify X-Webhook-Signature against the same secret.
	var signature string
	if HMACSecret != "" {
		mac := hmac.New(sha256.New, []byte(HMACSecret))
		mac.Write(body)
		signature = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	// Retry loop with exponential backoff.
	var lastErr error
	for attempt := 0; attempt < DefaultRetryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			// Apply backoff delay between retries.
			delayIdx := attempt - 1
			if delayIdx >= len(DefaultRetryConfig.Delays) {
				delayIdx = len(DefaultRetryConfig.Delays) - 1
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(DefaultRetryConfig.Delays[delayIdx]):
			}
		}

		lastErr = sendSingle(ctx, url, body, signature)
		if lastErr == nil {
			return nil
		}

		// Only retry on transient errors (5xx, network issues).
		// 4xx errors are permanent — retrying won't help.
		if !isRetryable(lastErr) {
			return lastErr
		}
	}

	return fmt.Errorf("webhook: all %d attempts failed: %w", DefaultRetryConfig.MaxAttempts, lastErr)
}

// sendSingle performs a single HTTP POST without retries.
func sendSingle(ctx context.Context, url string, body []byte, signature string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-Webhook-Signature", signature)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &retryableError{err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := make([]byte, 256)
		n, _ := resp.Body.Read(buf)
		bodySnippet := ""
		if n > 0 {
			bodySnippet = string(buf[:n])
		}
		err := fmt.Errorf("webhook returned %d: %s", resp.StatusCode, bodySnippet)
		// 5xx = retryable, 4xx = not
		if resp.StatusCode >= 500 {
			return &retryableError{err}
		}
		return err
	}

	return nil
}

// retryableError wraps an error that should be retried.
type retryableError struct{ error }

func isRetryable(err error) bool {
	_, ok := err.(*retryableError)
	return ok
}
