import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { DURATIONS, DEFAULT_DURATION } from './durations';

// v3.6 wave 7: the fifteen durations are defined ONCE on-chain
// (contracts/src/MarketplaceCore.sol DURATION_* constants) and mirrored here
// for the pickers. Any drift means the UI offers a duration the contract
// rejects (or hides one it accepts).
function contractDurations(): number[] {
  const sol = readFileSync(resolve(__dirname, '../../../../contracts/src/MarketplaceCore.sol'), 'utf8');
  const out: number[] = [];
  for (const m of sol.matchAll(/uint64\s+constant\s+DURATION_\w+\s*=\s*(\d+)\s*(minutes|hours|days);/g)) {
    const n = Number(m[1]);
    out.push(m[2] === 'minutes' ? n * 60 : m[2] === 'hours' ? n * 3600 : n * 86400);
  }
  return out;
}

describe('durations parity (contracts ↔ app)', () => {
  it('the app offers exactly the on-chain DURATION_* set, ascending, 15 entries', () => {
    const chain = contractDurations();
    expect(chain.length).toBe(15);
    const app = DURATIONS.map((d) => d.seconds);
    expect(app).toEqual([...chain].sort((a, b) => a - b));
    expect(app).toEqual([...app].sort((a, b) => a - b));
    expect(new Set(app).size).toBe(app.length);
  });

  it('the default is one of them and the labels are human', () => {
    expect(DURATIONS.some((d) => d.seconds === DEFAULT_DURATION)).toBe(true);
    for (const d of DURATIONS) expect(d.label).toMatch(/^\d+ (minute|minutes|hour|hours)$/);
  });
});
