/* ── WalletConnect pairing overlay (self-contained, no Alpine dependency).
 *
 * Shows a full-screen centered modal immediately on connect click (loading
 * state), then transitions to display a scannable QR code when the SDK
 * emits `display_uri`. Uses the Reown/WalletConnect SDK directly via the
 * `display_uri` event — no custom wallet logic, just a clean UI over the
 * standard SDK pairing flow.
 *
 * The QR code is rendered using the `qrcode` library (qrcode.min.js, loaded
 * in layout.html) which generates a data:image/gif;base64 URL from any
 * string — no external services, no canvas, no third-party API.
 *
 * Exposes:
 *   MW_WC_show()      — show overlay with loading spinner
 *   MW_WC_showURI(u)  — transition to QR code display
 *   MW_WC_hide()      — tear down overlay
 *
 * Each call creates a fresh overlay. Stale overlays are removed before
 * creating a new one. No Alpine, no reactive state, no event bus.
 */

(function () {
'use strict';

var _overlay = null;

/**
 * Generate a scannable QR code data URL from a WalletConnect URI.
 * Uses the `qrcode` library (global from qrcode.min.js) to encode the
 * URI string into a QR code bitmap and return it as a data:image/gif
 * URL that can be set as an <img src="...">.
 *
 * The library API:
 *   var qr = qrcode(0, 'L');   // auto-typeNumber, low error correction
 *   qr.addData(uri);
 *   qr.make();
 *   var url = qr.createDataURL(cellSize, margin);
 */
function generateQR(uri) {
  if (typeof qrcode !== 'function') return null;
  try {
    var qr = qrcode(0, 'L');
    qr.addData(uri);
    qr.make();
    // cellSize=6 gives ~300px for a typical WC URI; margin=4*cellSize
    return qr.createDataURL(6, 24);
  } catch (e) {
    console.warn('[mw] QR generation failed:', e);
    return null;
  }
}

function createOverlay() {
  // Remove any stale overlay.
  if (_overlay) { try { document.body.removeChild(_overlay); } catch (_) {} _overlay = null; }

  // Remove the legacy wc-uri-banner if it exists (cleanup from prior deploys).
  var oldBanner = document.getElementById('wc-uri-banner');
  if (oldBanner) { try { oldBanner.remove(); } catch (_) {} }

  var overlay = document.createElement('div');
  overlay.id = 'wc-pairing-overlay';
  // Full-screen dimmed backdrop + centered card.
  overlay.style.cssText = 'position:fixed;inset:0;z-index:9999;display:flex;align-items:center;justify-content:center;background:rgba(5,5,10,0.92);backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);opacity:0;transition:opacity 0.25s ease;';

  var card = document.createElement('div');
  card.style.cssText = 'position:relative;width:90%;max-width:440px;background:rgba(10,10,16,0.98);border:1px solid rgba(167,139,250,0.3);border-radius:24px;padding:36px 28px 28px;box-shadow:0 0 60px -12px rgba(167,139,250,0.25);text-align:center;';

  // ── Loading state (shown immediately) ──
  var body = document.createElement('div');
  body.id = 'wc-overlay-body';

  var spinner = document.createElement('div');
  spinner.id = 'wc-overlay-spinner';
  spinner.style.cssText = 'display:flex;flex-direction:column;align-items:center;gap:20px;';
  spinner.innerHTML = '<div style="width:48px;height:48px;border:4px solid rgba(167,139,250,0.2);border-top-color:#a78bfa;border-radius:50%;animation:wc-spin 0.8s linear infinite;"></div>'
    + '<p style="margin:0;color:rgba(255,255,255,0.7);font-size:14px;font-weight:600;font-family:sans-serif;">Connecting to WalletConnect…</p>';
  body.appendChild(spinner);

  // ── QR code state (hidden until display_uri fires) ──
  var qrBlock = document.createElement('div');
  qrBlock.id = 'wc-overlay-qr';
  qrBlock.style.cssText = 'display:none;flex-direction:column;align-items:center;gap:16px;';

  var icon = document.createElement('div');
  icon.style.cssText = 'width:56px;height:56px;border-radius:16px;background:linear-gradient(135deg,#a78bfa,#7c3aed);display:flex;align-items:center;justify-content:center;font-size:28px;box-shadow:0 0 24px -4px rgba(167,139,250,0.4);';
  icon.textContent = '⌬';
  qrBlock.appendChild(icon);

  var heading = document.createElement('p');
  heading.style.cssText = 'margin:0;color:#fff;font-size:16px;font-weight:800;font-family:sans-serif;';
  heading.textContent = 'Scan with your wallet app';
  qrBlock.appendChild(heading);

  var instructions = document.createElement('p');
  instructions.style.cssText = 'margin:0;color:rgba(255,255,255,0.5);font-size:12px;font-weight:500;font-family:sans-serif;max-width:320px;line-height:1.4;';
  instructions.textContent = 'Open any WalletConnect-compatible wallet (MetaMask, Trust Wallet, Rainbow, Bifrost) and scan the QR code or copy the link below.';
  qrBlock.appendChild(instructions);

  // ── QR code image container ──
  var qrImg = document.createElement('img');
  qrImg.id = 'wc-overlay-qr-img';
  qrImg.style.cssText = 'display:none;width:280px;height:280px;border-radius:16px;padding:8px;background:#fff;box-shadow:0 0 30px -6px rgba(167,139,250,0.3);';
  qrImg.alt = 'WalletConnect QR code — scan with your wallet';
  qrBlock.appendChild(qrImg);

  // ── Fallback URI row (shown alongside QR for copy-paste) ──
  var uriRow = document.createElement('div');
  uriRow.style.cssText = 'display:flex;gap:8px;width:100%;max-width:380px;';

  var uriInput = document.createElement('input');
  uriInput.id = 'wc-overlay-uri-input';
  uriInput.readOnly = true;
  uriInput.style.cssText = 'flex:1;background:rgba(15,15,22,0.9);border:1px solid rgba(255,255,255,0.1);border-radius:12px;padding:12px 14px;color:rgba(255,255,255,0.7);font-size:11px;font-family:monospace;outline:none;';
  uriInput.placeholder = 'Waiting for URI…';
  uriRow.appendChild(uriInput);

  var copyBtn = document.createElement('button');
  copyBtn.id = 'wc-overlay-copy-btn';
  copyBtn.style.cssText = 'padding:12px 20px;border:none;border-radius:12px;background:linear-gradient(135deg,#7dd3fc,#0ea5e9);color:#09090b;font-size:12px;font-weight:800;font-family:sans-serif;cursor:pointer;text-transform:uppercase;letter-spacing:0.5px;transition:opacity 0.2s;white-space:nowrap;';
  copyBtn.textContent = 'Copy';
  copyBtn.onmouseover = function () { copyBtn.style.opacity = '0.9'; };
  copyBtn.onmouseout = function () { copyBtn.style.opacity = '1'; };
  copyBtn.onclick = function () {
    var inp = document.getElementById('wc-overlay-uri-input');
    if (!inp || !inp.value) return;
    inp.select();
    var promise = null;
    try { promise = navigator.clipboard.writeText(inp.value); } catch (_) {
      try { document.execCommand('copy'); promise = Promise.resolve(); } catch (_) {}
    }
    if (promise && typeof promise.then === 'function') {
      promise.then(function () {
        copyBtn.textContent = '\u2713 Copied';
        copyBtn.style.background = 'linear-gradient(135deg,#a78bfa,#7c3aed)';
        copyBtn.style.color = '#fff';
        setTimeout(function () {
          copyBtn.textContent = 'Copy';
          copyBtn.style.background = 'linear-gradient(135deg,#7dd3fc,#0ea5e9)';
          copyBtn.style.color = '#09090b';
        }, 2000);
      }).catch(function () {
        // Clipboard writeText() promise rejected asynchronously (e.g.
        // insecure context, permission denied, or DOMException). Fall
        // back to execCommand('copy') which works in most contexts
        // where navigator.clipboard is unavailable. Only update the
        // button UI on success — leave unchanged on failure so the
        // user sees the action didn't complete.
        try {
          if (document.execCommand('copy')) {
            copyBtn.textContent = '\u2713 Copied';
            copyBtn.style.background = 'linear-gradient(135deg,#a78bfa,#7c3aed)';
            copyBtn.style.color = '#fff';
            setTimeout(function () {
              copyBtn.textContent = 'Copy';
              copyBtn.style.background = 'linear-gradient(135deg,#7dd3fc,#0ea5e9)';
              copyBtn.style.color = '#09090b';
            }, 2000);
          }
        } catch (_) {}
      });
    } else {
      // execCommand fallback succeeded synchronously.
      copyBtn.textContent = '\u2713 Copied';
      copyBtn.style.background = 'linear-gradient(135deg,#a78bfa,#7c3aed)';
      copyBtn.style.color = '#fff';
      setTimeout(function () {
        copyBtn.textContent = 'Copy';
        copyBtn.style.background = 'linear-gradient(135deg,#7dd3fc,#0ea5e9)';
        copyBtn.style.color = '#09090b';
      }, 2000);
    }
  };
  uriRow.appendChild(copyBtn);
  qrBlock.appendChild(uriRow);

  // Deep-link button (mobile wallets)
  var deepLink = document.createElement('a');
  deepLink.id = 'wc-overlay-deeplink';
  deepLink.style.cssText = 'display:none;margin-top:4px;padding:10px 24px;border-radius:12px;background:rgba(167,139,250,0.12);border:1px solid rgba(167,139,250,0.25);color:#a78bfa;font-size:12px;font-weight:700;font-family:sans-serif;text-decoration:none;transition:background 0.2s;';
  deepLink.textContent = 'Open in wallet →';
  deepLink.onmouseover = function () { deepLink.style.background = 'rgba(167,139,250,0.2)'; };
  deepLink.onmouseout = function () { deepLink.style.background = 'rgba(167,139,250,0.12)'; };
  deepLink.target = '_blank';
  deepLink.rel = 'noopener noreferrer';
  qrBlock.appendChild(deepLink);

  body.appendChild(qrBlock);
  card.appendChild(body);

  // Dismiss button: appended to the CARD (not qrBlock) so it remains
  // visible during both the loading spinner state AND the QR state. When
  // placed inside qrBlock (the hidden QR section), users could never
  // dismiss the overlay if the WalletConnect URI never arrived — the
  // Cancel button was hidden behind qrBlock.style.display='none' during
  // the loading phase. Now it's always accessible.
  var dismissBtn = document.createElement('button');
  dismissBtn.style.cssText = 'margin-top:8px;padding:8px 16px;border:none;border-radius:8px;background:transparent;color:rgba(255,255,255,0.35);font-size:11px;font-weight:600;font-family:sans-serif;cursor:pointer;transition:color 0.2s;';
  dismissBtn.textContent = 'Cancel';
  dismissBtn.onmouseover = function () { dismissBtn.style.color = 'rgba(255,255,255,0.7)'; };
  dismissBtn.onmouseout = function () { dismissBtn.style.color = 'rgba(255,255,255,0.35)'; };
  dismissBtn.onclick = function () { MW_WC_hide(); };
  card.appendChild(dismissBtn);
  overlay.appendChild(card);
  document.body.appendChild(overlay);

  // Trigger fade-in on next frame.
  requestAnimationFrame(function () { overlay.style.opacity = '1'; });

  _overlay = overlay;
}

// Spin animation keyframe (injected once).
(function () {
  if (document.getElementById('wc-overlay-style')) return;
  var style = document.createElement('style');
  style.id = 'wc-overlay-style';
  style.textContent = '@keyframes wc-spin { to { transform: rotate(360deg); } }';
  document.head.appendChild(style);
})();

window.MW_WC_show = function () {
  createOverlay();
  // Ensure loading state is visible.
  var spinner = document.getElementById('wc-overlay-spinner');
  var qrBlock = document.getElementById('wc-overlay-qr');
  if (spinner) spinner.style.display = 'flex';
  if (qrBlock) qrBlock.style.display = 'none';
};

window.MW_WC_showURI = function (uri) {
  // Ensure overlay exists first.
  if (!_overlay) {
    createOverlay();
  }
  var spinner = document.getElementById('wc-overlay-spinner');
  var qrBlock = document.getElementById('wc-overlay-qr');
  var qrImg = document.getElementById('wc-overlay-qr-img');
  var input = document.getElementById('wc-overlay-uri-input');
  var deep = document.getElementById('wc-overlay-deeplink');

  if (spinner) spinner.style.display = 'none';
  if (qrBlock) qrBlock.style.display = 'flex';

  // Generate and display the QR code image.
  if (qrImg && uri) {
    var dataUrl = generateQR(uri);
    if (dataUrl) {
      qrImg.src = dataUrl;
      qrImg.style.display = 'block';
    }
  }

  if (input) {
    input.value = uri;
    // Select all text for easy copy.
    try { input.select(); } catch (_) {}
  }

  // Set deep-link for mobile wallets.
  if (deep && uri) {
    deep.href = uri.indexOf('wc:') === 0 ? uri : ('https://walletconnect.com/wc?uri=' + encodeURIComponent(uri));
    deep.style.display = 'inline-block';
  } else if (deep) {
    deep.style.display = 'none';
  }
};

window.MW_WC_hide = function () {
  if (_overlay) {
    var o = _overlay;
    o.style.opacity = '0';
    setTimeout(function () {
      try { if (o.parentNode) o.parentNode.removeChild(o); } catch (_) {}
      if (_overlay === o) _overlay = null;
    }, 250);
  }
};

})();
