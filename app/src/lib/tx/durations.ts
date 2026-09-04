// The fourteen durations every time-bound action accepts (MarketplaceCore.sol).
// Contracts take the DURATION and compute expiry on-chain.
export const DURATIONS = [
  { seconds: 1 * 60, label: '1 minute' },
  { seconds: 3 * 60, label: '3 minutes' },
  { seconds: 5 * 60, label: '5 minutes' },
  { seconds: 15 * 60, label: '15 minutes' },
  { seconds: 30 * 60, label: '30 minutes' },
  { seconds: 45 * 60, label: '45 minutes' },
  { seconds: 60 * 60, label: '1 hour' },
  { seconds: 2 * 60 * 60, label: '2 hours' },
  { seconds: 4 * 60 * 60, label: '4 hours' },
  { seconds: 8 * 60 * 60, label: '8 hours' },
  { seconds: 12 * 60 * 60, label: '12 hours' },
  { seconds: 16 * 60 * 60, label: '16 hours' },
  { seconds: 20 * 60 * 60, label: '20 hours' },
  { seconds: 24 * 60 * 60, label: '24 hours' },
] as const;

export type DurationSeconds = (typeof DURATIONS)[number]['seconds'];

export function isValidDuration(s: number): s is DurationSeconds {
  return DURATIONS.some((d) => d.seconds === s);
}

export const DEFAULT_DURATION: DurationSeconds = 24 * 60 * 60;
