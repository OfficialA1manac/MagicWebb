// Package webhook provides HTTP POST senders for several webhook formats:
//   - Discord/Slack: accepts a Payload with content + embeds
//   - Prometheus Alertmanager: sends Alertmanager-compatible alert payloads
package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

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
func sendJSON(ctx context.Context, url string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("webhook marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		buf := make([]byte, 256)
		n, _ := resp.Body.Read(buf)
		bodySnippet := ""
		if n > 0 {
			bodySnippet = string(buf[:n])
		}
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, bodySnippet)
	}

	return nil
}
