// The six durations every time-bound action accepts (MarketplaceCore.sol).
// Contracts take the DURATION and compute expiry on-chain.
export const DURATIONS = [
  { seconds: 3 * 60, label: '3 minutes' },
  { seconds: 15 * 60, label: '15 minutes' },
  { seconds: 30 * 60, label: '30 minutes' },
  { seconds: 60 * 60, label: '1 hour' },
  { seconds: 4 * 60 * 60, label: '4 hours' },
  { seconds: 24 * 60 * 60, label: '24 hours' },
] as const;

export type DurationSeconds = (typeof DURATIONS)[number]['seconds'];

export function isValidDuration(s: number): s is DurationSeconds {
  return DURATIONS.some((d) => d.seconds === s);
}

export const DEFAULT_DURATION: DurationSeconds = 24 * 60 * 60;
