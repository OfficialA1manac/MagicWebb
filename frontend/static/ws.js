/* ─────────────────────────────────────────────────────────────────────────────
 * MagicWebb — WebSocket client for ALL real-time communication.
 *
 * Replaces the legacy SSE (/events) connection — this is now the single
 * persistent transport for server→client push AND client→server messaging.
 * Push events (listing-updated, auction-updated, etc.) are dispatched as
 * DOM CustomEvents so HTMX hx-trigger attributes pick them up directly.
 *
 * Reconnect strategy: exponential backoff, capped at 30s, no jitter.
 * ───────────────────────────────────────────────────────────────────────────── */
(function () {
  'use strict';

  // ── Configuration ──────────────────────────────────────────────────────────
  const RECONNECT_BASE = 1000;   // 1s initial backoff
  const RECONNECT_MAX  = 30000;  // 30s max backoff
  const PING_INTERVAL  = 25000;  // 25s — send ping before server drops idle

  // ── State ──────────────────────────────────────────────────────────────────
  let ws      = null;
  let retry   = 0;
  let timer   = null;
  let closing = false;
  let pendingSubs = []; // channels queued for subscription on next connect

  /* ── Global API ─────────────────────────────────────────────────────────────
   * Exposed as window.MW_WS so Alpine or inline scripts can send messages.
   *
   *   MW_WS.send({ type: 'ping' })
   *   MW_WS.send({ type: 'action', data: { action: 'bid', params: {...} } })
   *
   * Returns true if the message was queued, false if not connected.
   */
  const api = {
    /** Send a JSON message. Returns true if queued. */
    send: function (msg) {
      if (!ws || ws.readyState !== WebSocket.OPEN) return false;
      try { ws.send(JSON.stringify(msg)); return true; } catch (_) { return false; }
    },

    /** True when the WebSocket is connected. */
    get connected() { return ws !== null && ws.readyState === WebSocket.OPEN; },

    /** Manually disconnect (stops auto-reconnect). */
    close: function () {
      closing = true;
      if (timer) { clearTimeout(timer); timer = null; }
      if (ws)    { try { ws.close(); } catch (_) {} ws = null; }
    },

    /** Manually reconnect. */
    reconnect: function () {
      retry = 0;
      closing = false;
      connect();
    },

    // ── Action helpers ────────────────────────────────────────────────────────

    /** Subscribe to event channels ("token:0xabc:123", "collection:0xabc", "user:0xdef").
     *  Channels are persisted locally and re-subscribed on reconnect so push
     *  events resume immediately after a network interruption. */
    subscribe: function (channels) {
      // Deduplicate against pending subscriptions
      for (var i = 0; i < channels.length; i++) {
        if (pendingSubs.indexOf(channels[i]) === -1) {
          pendingSubs.push(channels[i]);
        }
      }
      this.send({ type: 'subscribe', data: { channels: channels } });
    },

    /** Unsubscribe from event channels. */
    unsubscribe: function (channels) {
      // Remove from pending list so reconnects don't re-subscribe
      for (var i = 0; i < channels.length; i++) {
        var idx = pendingSubs.indexOf(channels[i]);
        if (idx !== -1) pendingSubs.splice(idx, 1);
      }
      this.send({ type: 'unsubscribe', data: { channels: channels } });
    },

    /** Returns the list of currently tracked subscriptions. */
    get subscriptions() { return pendingSubs.slice(); },

    /** Request the current state of a listing by collection + token ID. */
    getListing: function (collection, tokenId) {
      this.send({ type: 'action', data: { action: 'get_listing', params: { collection: collection, token_id: tokenId } } });
    },

    /** Request the current state of an auction by ID. */
    getAuction: function (auctionId) {
      this.send({ type: 'action', data: { action: 'get_auction', params: { auction_id: auctionId } } });
    },

    /** Request an offer by ID. */
    getOffer: function (offerId) {
      this.send({ type: 'action', data: { action: 'get_offer', params: { offer_id: offerId } } });
    },

    /** Request token metadata by collection + token ID. */
    getToken: function (collection, tokenId) {
      this.send({ type: 'action', data: { action: 'get_token', params: { collection: collection, token_id: tokenId } } });
    },
  };

  // ── Connection lifecycle ───────────────────────────────────────────────────

  function connect() {
    if (closing) return;
    // Don't open duplicate connections
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url      = protocol + '//' + window.location.host + '/ws';

    try {
      ws = new WebSocket(url);
    } catch (e) {
      scheduleReconnect();
      return;
    }

    ws.onopen = function () {
      retry = 0; // reset backoff on successful connect
      // Re-subscribe any channels that were subscribed before disconnect.
      // Page-level Alpine components subscribe on init() via MW_WS.subscribe();
      // on reconnect we replay those subscriptions so the server re-activates
      // the push event filters that were lost when the old connection closed.
      var subs = pendingSubs.slice();
      if (subs.length > 0) {
        api.send({ type: 'subscribe', data: { channels: subs } });
      }
      // Fire a custom event so Alpine / other listeners can react
      window.dispatchEvent(new CustomEvent('mw-ws-open'));
    };

    ws.onmessage = function (event) {
      try {
        const msg = JSON.parse(event.data);

        // Dispatch as a CustomEvent for Alpine/hTMX listeners
        window.dispatchEvent(new CustomEvent('mw-ws-message', {
          detail: msg,
        }));

        // Handle specific message types
        switch (msg.type) {
          case 'pong':
            break; // no-op; connection is healthy

          case 'error':
            // Surface as a custom event so the app can react
            window.dispatchEvent(new CustomEvent('mw-ws-error', {
              detail: msg.data,
            }));
            break;

          case 'ack':
            // Connection established / action acknowledged
            break;

          case 'subscribed':
            // Subscription confirmation — dispatch dedicated event so Alpine
            // components can react (e.g. show subscribed channels indicator).
            window.dispatchEvent(new CustomEvent('mw-ws-subscribed', {
              detail: msg.data,
            }));
            break;

          case 'unsubscribed':
            // Unsubscription confirmation.
            window.dispatchEvent(new CustomEvent('mw-ws-unsubscribed', {
              detail: msg.data,
            }));
            break;

          case 'state':
            // State data response (get_listing, get_auction, get_offer, get_token).
            // dispatch with both the raw msg and the resolved state payload.
            window.dispatchEvent(new CustomEvent('mw-ws-state', {
              detail: msg.data,
            }));
            break;

          default:
            // Forward push events as DOM events for HTMX hx-trigger attributes.
            // Server-sent events (listing-updated, auction-updated, activity,
            // notification, etc.) trigger matching HTMX elements directly.
            if (msg.type && msg.data) {
              // Custom event for programmatic listeners
              window.dispatchEvent(new CustomEvent('mw-ws-event', {
                detail: { type: msg.type, data: msg.data },
              }));
            // Trigger HTMX elements with hx-trigger matching this event type.
              // HTMX registers listeners per-element, so we iterate all [hx-trigger]
              // elements and trigger those whose trigger matches the event type
              // (using word-boundary match to avoid false positives like "update"
              // matching "auction-updated").
              if (typeof htmx !== 'undefined') {
                document.querySelectorAll('[hx-trigger]').forEach(function(el) {
                  var triggers = (el.getAttribute('hx-trigger') || '').split(',');
                  var matches = triggers.some(function(t) {
                    return t.trim().split(/\s+/)[0] === msg.type;
                  });
                  if (matches) {
                    htmx.trigger(el, msg.type);
                  }
                });
              }
            }
        }
      } catch (_) {
        // Malformed JSON — ignore
      }
    };

    ws.onclose = function () {
      ws = null;
      window.dispatchEvent(new CustomEvent('mw-ws-close'));
      scheduleReconnect();
    };

    ws.onerror = function () {
      // onclose fires after onerror, so reconnect is handled there
    };
  }

  function scheduleReconnect() {
    if (closing) return;
    if (timer) return; // already scheduled
    retry = Math.min(retry + 1, 10);
    const delay = Math.min(RECONNECT_BASE * Math.pow(1.5, retry - 1), RECONNECT_MAX);
    timer = setTimeout(function () {
      timer = null;
      connect();
    }, delay);
  }

  // ── Periodic ping ──────────────────────────────────────────────────────────
  setInterval(function () {
    if (api.connected) {
      api.send({ type: 'ping' });
    }
  }, PING_INTERVAL);

  // ── Auto-connect on page load ─────────────────────────────────────────────
  if (document.readyState === 'complete' || document.readyState === 'interactive') {
    connect();
  } else {
    document.addEventListener('DOMContentLoaded', connect);
  }

  // Expose globally
  window.MW_WS = api;
})();
