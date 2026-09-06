package indexer

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// ── Governance events (v3.6 wave 1) ──────────────────────────────────────────
//
// The manager's admin/keeper lifecycle and every core's upgrade queue used to
// be invisible: the watcher filtered only the trading cores and none of these
// topics had a handler. Now each one lands in governance_events (043), bumps a
// Prometheus counter, publishes a "governance" SSE event (which the webhook
// dispatcher maps to the "governance" webhook type), and — for the events
// that change who controls the contracts — notifies every signed-in wallet
// that has not opted out (profiles.security_alerts).

// GovernanceEvent is the SSE payload for Type "governance". Untyped in the
// proto oneof on purpose: the bridge falls back to the JSON payload, and the
// only consumers are the webhook dispatcher and /status polling.
type GovernanceEvent struct {
	Event     string `json:"event"`
	Contract  string `json:"contract"`
	Actor     string `json:"actor,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Extra     string `json:"extra,omitempty"`
	TxHash    string `json:"tx_hash"`
	Block     uint64 `json:"block"`
	BlockTime int64  `json:"block_time"`
}

// governanceCounts backs magicwebb_governance_events_total{event}.
var (
	governanceMu     sync.Mutex
	governanceCounts = map[string]int64{}
)

func bumpGovernance(event string) {
	governanceMu.Lock()
	governanceCounts[event]++
	governanceMu.Unlock()
}

// GovernanceCounts returns a stable (sorted) snapshot for the metrics route.
func GovernanceCounts() []struct {
	Event string
	Count int64
} {
	governanceMu.Lock()
	defer governanceMu.Unlock()
	out := make([]struct {
		Event string
		Count int64
	}, 0, len(governanceCounts))
	for k, v := range governanceCounts {
		out = append(out, struct {
			Event string
			Count int64
		}{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Event < out[j].Event })
	return out
}

// securityAlertEvents are the governance events that change who controls the
// contracts; only these fan out as user notifications. Cancels, AuditLog and
// the routine Upgraded-at-construction do not.
var securityAlertEvents = map[string]bool{
	"UpgradeQueued":        true,
	"Upgraded":             true,
	"KeeperSet":            true,
	"AdminTransferStarted": true,
	"AdminTransferred":     true,
	"AdminRenounced":       true,
}

// securityAlertCopy is the plain-language notification per event.
func securityAlertCopy(ev GovernanceEvent) (title, body string) {
	short := func(a string) string {
		if len(a) >= 10 {
			return a[:6] + "…" + a[len(a)-4:]
		}
		return a
	}
	switch ev.Event {
	case "UpgradeQueued":
		return "Contract upgrade queued", "A new implementation (" + short(ev.Subject) + ") was queued for " + short(ev.Contract) + ". Upgrades are instant on this network."
	case "Upgraded":
		return "Contract upgraded", short(ev.Contract) + " now runs implementation " + short(ev.Subject) + "."
	case "KeeperSet":
		return "Keeper replaced", "The auction keeper changed to " + short(ev.Subject) + "."
	case "AdminTransferStarted":
		return "Admin hand-over started", "The admin offered control to " + short(ev.Subject) + ". Nothing changes until they accept."
	case "AdminTransferred":
		return "Admin changed", "Control of the contracts moved to " + short(ev.Subject) + "."
	case "AdminRenounced":
		return "Contracts are now immutable", "The admin gave up control. No more upgrades or keeper changes are possible on this network."
	}
	return "Governance event", ev.Event
}

// decodeGovernance turns a raw log into a GovernanceEvent, or nil when the
// topic is not a governance topic. Malformed logs (too few topics) return an
// error wrapping errMalformedLog so the range keeps advancing.
func decodeGovernance(l types.Log, blockTime uint64) (*GovernanceEvent, error) {
	if len(l.Topics) == 0 {
		return nil, nil
	}
	ev := &GovernanceEvent{
		Contract:  addrStr(l.Address.Bytes()),
		TxHash:    l.TxHash.Hex(),
		Block:     l.BlockNumber,
		BlockTime: int64(blockTime),
	}
	need := func(n int, name string) error {
		if len(l.Topics) < n {
			return malformedLogf("%s: short log", name)
		}
		return nil
	}
	switch l.Topics[0] {
	case TopicKeeperSet: // KeeperSet(address indexed previousKeeper, address indexed newKeeper)
		if err := need(3, "KeeperSet"); err != nil {
			return nil, err
		}
		ev.Event, ev.Actor, ev.Subject = "KeeperSet", addrStr(l.Topics[1].Bytes()), addrStr(l.Topics[2].Bytes())
	case TopicAdminRenounced: // AdminRenounced(address indexed lastAdmin)
		if err := need(2, "AdminRenounced"); err != nil {
			return nil, err
		}
		ev.Event, ev.Actor = "AdminRenounced", addrStr(l.Topics[1].Bytes())
	case TopicAdminTransferStarted: // AdminTransferStarted(address indexed current, address indexed pending)
		if err := need(3, "AdminTransferStarted"); err != nil {
			return nil, err
		}
		ev.Event, ev.Actor, ev.Subject = "AdminTransferStarted", addrStr(l.Topics[1].Bytes()), addrStr(l.Topics[2].Bytes())
	case TopicAdminTransferCancelled: // AdminTransferCancelled(address indexed pending)
		if err := need(2, "AdminTransferCancelled"); err != nil {
			return nil, err
		}
		ev.Event, ev.Subject = "AdminTransferCancelled", addrStr(l.Topics[1].Bytes())
	case TopicAdminTransferred: // AdminTransferred(address indexed previous, address indexed current)
		if err := need(3, "AdminTransferred"); err != nil {
			return nil, err
		}
		ev.Event, ev.Actor, ev.Subject = "AdminTransferred", addrStr(l.Topics[1].Bytes()), addrStr(l.Topics[2].Bytes())
	case TopicAuditLog: // AuditLog(bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra)
		if err := need(4, "AuditLog"); err != nil {
			return nil, err
		}
		ev.Event = "AuditLog"
		ev.Actor, ev.Subject = addrStr(l.Topics[2].Bytes()), addrStr(l.Topics[3].Bytes())
		ev.Extra = bytes32Label(l.Topics[1].Bytes())
	case TopicUpgradeQueued: // UpgradeQueued(address indexed implementation, uint64 eta)
		if err := need(2, "UpgradeQueued"); err != nil {
			return nil, err
		}
		ev.Event, ev.Subject = "UpgradeQueued", addrStr(l.Topics[1].Bytes())
		if len(l.Data) >= 32 {
			ev.Extra = "eta=" + bigStr(chunk(l.Data, 0))
		}
	case TopicUpgradeCancelled: // UpgradeCancelled(address indexed implementation)
		if err := need(2, "UpgradeCancelled"); err != nil {
			return nil, err
		}
		ev.Event, ev.Subject = "UpgradeCancelled", addrStr(l.Topics[1].Bytes())
	case TopicUpgraded: // Upgraded(address indexed implementation) — ERC-1967
		if err := need(2, "Upgraded"); err != nil {
			return nil, err
		}
		ev.Event, ev.Subject = "Upgraded", addrStr(l.Topics[1].Bytes())
	default:
		return nil, nil
	}
	return ev, nil
}

// bytes32Label renders a right-padded ASCII bytes32 (the manager's action
// codes like "SET_KEEPER") as text, falling back to hex for binary values.
func bytes32Label(b []byte) string {
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	s := b[:end]
	for _, c := range s {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("0x%x", b)
		}
	}
	return string(s)
}

// onGovernance persists, counts, publishes and (when it changes control)
// notifies. chainID is carried on the handlers so the row is network-scoped.
func (h *handlers) onGovernance(ctx context.Context, l types.Log, blockTime uint64) error {
	ev, err := decodeGovernance(l, blockTime)
	if err != nil || ev == nil {
		return err
	}
	row := db.GovernanceEventRow{
		ChainID: h.chainID, BlockNumber: int64(l.BlockNumber), TxHash: ev.TxHash, LogIndex: int32(l.Index),
		Contract: ev.Contract, Event: ev.Event, Actor: ev.Actor, Subject: ev.Subject, Extra: ev.Extra,
		BlockTime: time.Unix(int64(blockTime), 0).UTC(),
	}
	fresh, err := h.q.InsertGovernanceEvent(ctx, row)
	if err != nil {
		return fmt.Errorf("onGovernance %s: %w", ev.Event, err)
	}
	if !fresh {
		return nil // replayed by reindexgov; already counted + notified
	}
	bumpGovernance(ev.Event)
	log.Warn().Str("event", ev.Event).Str("contract", ev.Contract).Str("actor", ev.Actor).
		Str("subject", ev.Subject).Str("tx", ev.TxHash).Msg("governance event")
	h.bcast.Publish(sse.Event{Type: "governance", Data: ev})

	if securityAlertEvents[ev.Event] {
		h.fanOutSecurityAlert(ctx, *ev)
	}
	return nil
}

// fanOutSecurityAlert notifies every opted-in wallet. Bounded (recipient cap
// in the query) and best-effort: a DB hiccup here never blocks the cursor.
func (h *handlers) fanOutSecurityAlert(ctx context.Context, ev GovernanceEvent) {
	title, body := securityAlertCopy(ev)
	recipients, err := h.q.ListSecurityAlertRecipients(ctx, 0)
	if err != nil {
		log.Warn().Err(err).Msg("governance: recipient list failed; alert not fanned out")
		return
	}
	for _, addr := range recipients {
		h.notify(ctx, addr, "system", title, body, "/status")
	}
}
