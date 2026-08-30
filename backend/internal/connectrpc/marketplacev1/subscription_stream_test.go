package marketplacev1

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// SubscribeNotifications filters events by unmarshalling the marshalled
// sse.NotificationEvent and comparing one field against the subscriber's
// address. That couples this file to a JSON tag in another package, and the
// failure mode is SILENT: name the wrong field and the comparison is always
// ""-vs-address, so the stream delivers nothing at all while looking healthy.
//
// This test pins the coupling. It mirrors the struct used by the filter.
func TestNotificationFilterFieldMatchesEventPayload(t *testing.T) {
	const want = "0x00000000000000000000000000000000000000AA"

	payload, err := json.Marshal(&sse.NotificationEvent{
		User:     want,
		UserAddr: want,
		Kind:     "refund",
		Title:    "Action needed",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Keep this shape identical to the one in SubscribeNotifications.
	var meta struct {
		UserAddr string `json:"user_addr"`
	}
	if err := json.Unmarshal(payload, &meta); err != nil {
		t.Fatal(err)
	}

	if meta.UserAddr == "" {
		t.Fatalf("filter field is empty for payload %s — the notification stream would drop EVERY event", payload)
	}
	if !strings.EqualFold(meta.UserAddr, want) {
		t.Fatalf("filter field = %q, want %q", meta.UserAddr, want)
	}
}

// The filter must not match an event belonging to a different wallet.
func TestNotificationFilterRejectsOtherWallets(t *testing.T) {
	payload, err := json.Marshal(&sse.NotificationEvent{
		UserAddr: "0x00000000000000000000000000000000000000bb",
	})
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		UserAddr string `json:"user_addr"`
	}
	if err := json.Unmarshal(payload, &meta); err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(meta.UserAddr, "0x00000000000000000000000000000000000000aa") {
		t.Fatal("filter matched a notification addressed to a different wallet")
	}
}
