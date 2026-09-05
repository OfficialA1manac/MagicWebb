// Search pluralisation + min-length + recent searches (spec B4 "Search") —
// pure exports from the component's module script.
import { describe, it, expect } from 'vitest';
import { canSearch, resultsHeading, groupResults, pushRecent, MAX_RECENT } from './SearchPage.svelte';

describe('search min length', () => {
  it('requires at least 2 non-whitespace characters', () => {
    expect(canSearch('')).toBe(false);
    expect(canSearch('a')).toBe(false);
    expect(canSearch(' a ')).toBe(false);
    expect(canSearch('ab')).toBe(true);
    expect(canSearch('  ab  ')).toBe(true);
  });
});

describe('results heading pluralisation', () => {
  it('singular for exactly one result', () => {
    expect(resultsHeading(1, 'magic')).toBe('1 result for "magic"');
  });
  it('plural otherwise, including zero', () => {
    expect(resultsHeading(0, 'zzzz')).toBe('0 results for "zzzz"');
    expect(resultsHeading(7, 'webb')).toBe('7 results for "webb"');
  });
});

describe('result grouping', () => {
  it('splits collections from NFTs, preserving order', () => {
    const rows = [
      { kind: 'nft', id: 1 }, { kind: 'collection', id: 2 }, { kind: 'nft', id: 3 },
    ];
    const g = groupResults(rows);
    expect(g.collections.map((r) => r.id)).toEqual([2]);
    expect(g.nfts.map((r) => r.id)).toEqual([1, 3]);
  });
});

describe('recent searches', () => {
  it('newest first, capped at 5', () => {
    let list: string[] = [];
    for (const q of ['a1', 'b2', 'c3', 'd4', 'e5', 'f6']) list = pushRecent(list, q);
    expect(list).toHaveLength(MAX_RECENT);
    expect(list).toEqual(['f6', 'e5', 'd4', 'c3', 'b2']);
  });

  it('re-searching an existing term moves it to the front without duplicating', () => {
    let list = pushRecent(pushRecent(pushRecent([], 'animi'), 'webb'), 'Animi');
    expect(list).toEqual(['Animi', 'webb']);
    list = pushRecent(list, 'webb');
    expect(list).toEqual(['webb', 'Animi']);
  });

  it('ignores whitespace-only queries', () => {
    expect(pushRecent(['keep'], '   ')).toEqual(['keep']);
  });
});
