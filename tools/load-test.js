// ── Phase 5 V5.1: k6 load-testing script ─────────────────────────────────────
// Validates backend capacity before mainnet traffic. Run with:
//   k6 run tools/load-test.js --vus 10 --duration 30s
//
// Targets the key public endpoints (no auth). Adjust TARGET env for staging:
//   k6 run -e TARGET=https://magicwebb.fly.dev tools/load-test.js --vus 20 --duration 60s
//
// Install k6: https://k6.io/docs/get-started/installation/
// ─────────────────────────────────────────────────────────────────────────────

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');

// ── Config (override via -e TARGET=...) ──
const TARGET  = __ENV.TARGET  || 'https://magicwebb.fly.dev';
const VUS     = __ENV.VUS     || '10';
const DUR     = __ENV.DUR     || '30s';

export const options = {
  vus: parseInt(VUS),
  duration: DUR,
  thresholds: {
    http_req_duration: ['p(95)<2000'], // 95% of requests under 2s
    errors:            ['rate<0.05'],   // <5% error rate
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)'],
};

// ── Simple GET helper with status + timing checks ──
function get(path, expectedStatus = 200, tags = {}) {
  const res = http.get(`${TARGET}${path}`, { tags });
  const ok = check(res, {
    [`${path} -> ${expectedStatus}`]: (r) => r.status === expectedStatus,
    [`${path} < 3s`]:                (r) => r.timings.duration < 3000,
  });
  errorRate.add(!ok); // tracks failed out of total requests
  return res;
}

// ── Default function: k6 calls this once per VU per iteration ──
export default function () {
  group('homepage', () => {
    // Home page (full HTML + SSE preamble)
    get('/', 200);
  });

  group('api - listings', () => {
    get('/api/v1/listings?limit=24&sort=recent', 200);
  });

  group('api - auctions', () => {
    get('/api/v1/auctions?limit=24&status=active&sort=ends_asc', 200);
  });

  group('api - search', () => {
    // Phase 5 V5.1: use k6 built-in __VU and __ITER instead of Math.random().
    // k6 seeds Math.random() deterministically per VU, meaning all VUs get the
    // same sequence of "random" query strings — they all hit the same search
    // terms simultaneously (less realistic load). __VU and __ITER produce true
    // variation across VUs and iterations.
    const queries = ['nft', 'art', 'collectible', 'pfp', 'ape', 'punk', 'doodle', 'azuki'];
    const q = queries[(__VU + __ITER) % queries.length];
    get(`/api/v1/search?q=${q}&limit=12`, 200);
  });

  group('api - collections', () => {
    get('/api/v1/collections?limit=12', 200);
  });

  group('healthz', () => {
    get('/healthz', 200);
  });

  // ── SSE events ──
  group('sse - events', () => {
    const res = http.get(`${TARGET}/events`, { headers: { Accept: 'text/event-stream' }, timeout: '5s' });
    check(res, {
      'SSE preamble': (r) => r.body.startsWith(': connected\n\n'),
    });
  });

  // Pace iterations: ~1-2s between each VU's iteration to simulate real-ish traffic.
  // Phase 5 V5.1: use __VU + __ITER for VU-varied pacing (same rationale as search).
  sleep(1 + ((__VU + __ITER) % 2000) / 1000);
}
