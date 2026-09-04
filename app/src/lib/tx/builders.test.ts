import { describe, it, expect, beforeEach } from 'vitest';
import { _resetChainCache } from '../chains';
import { buildList, buildBuy, buildCancel, buildEditPrice, buildBatchList, MIN_PRICE_WEI } from './marketplace';
import { buildCreate, buildBid, buildForceCancel, forceCancelUnlocked, minimumTopUp, MIN_BID_INCREMENT_WEI, FORCE_CANCEL_WINDOW_SEC } from './auction';
import { buildMakeOffer, buildAcceptOffer } from './offers';
import { TxError } from './errors';
import { DURATIONS } from './durations';

const NFT = '0x832d74Cfbb4617B50C32cD110dfe16837A359B35' as const;
const SELLER = '0xdf73Dc01Fc162cDa23e3f8FbD09bF663Cf5cAe0b' as const;
const MP = '0x34b53209eC694Ce243e28606233CFc72D0673436';
const AH = '0x93627Ca0032bc1B8eE0D9134a7c9c4f74d97d04C';
const OB = '0x918bF8748c9950Dc6aDbB2796dE6DfFaf3f44d5E';
const E = 10n ** 18n;

beforeEach(() => {
  _resetChainCache();
  window.MW_CHAIN_ID = '114';
  window.MW_MARKETPLACE = MP; window.MW_AUCTION = AH; window.MW_OFFERBOOK = OB;
});

describe('marketplace builders (ABI: list(coll,id,uint128 price,uint64 duration))', () => {
  it('list: exact args, duration not expiresAt', () => {
    const r = buildList(NFT, 7n, 2n * E, 86400);
    expect(r.address).toBe(MP);
    expect(r.functionName).toBe('list');
    expect(r.args).toEqual([NFT, 7n, 2n * E, 86400n]);
    expect(r.value).toBeUndefined();
  });
  it('list1155 carries amount before price', () => {
    const r = buildList(NFT, 7n, 2n * E, 3600, 'erc1155', 5n);
    expect(r.functionName).toBe('list1155');
    expect(r.args).toEqual([NFT, 7n, 5n, 2n * E, 3600n]);
  });
  it('rejects price below MIN_PRICE (1 ether)', () => {
    expect(() => buildList(NFT, 1n, MIN_PRICE_WEI - 1n, 86400)).toThrowError(TxError);
    expect(() => buildList(NFT, 1n, MIN_PRICE_WEI, 86400)).not.toThrow();
  });
  it('accepts exactly the fourteen shared durations', () => {
    for (const d of DURATIONS) expect(() => buildList(NFT, 1n, E, d.seconds)).not.toThrow();
    for (const bad of [0, 1, 120, 420, 7201, 86401, 90000]) expect(() => buildList(NFT, 1n, E, bad)).toThrowError(/durations/);
  });
  it('buy sends msg.value == price', () => {
    const r = buildBuy(NFT, 7n, SELLER, 3n * E);
    expect(r.functionName).toBe('buy');
    expect(r.args).toEqual([NFT, 7n, SELLER]);
    expect(r.value).toBe(3n * E);
  });
  it('cancel / editPrice', () => {
    expect(buildCancel(NFT, 7n).args).toEqual([NFT, 7n]);
    expect(buildEditPrice(NFT, 7n, 5n * E).args).toEqual([NFT, 7n, 5n * E]);
    expect(() => buildEditPrice(NFT, 7n, 1n)).toThrowError(TxError);
  });
  it('batchList: struct shape {coll,id,price,duration}, 1..50', () => {
    const r = buildBatchList([{ coll: NFT, id: 1n, price: E, duration: 86400 }]);
    expect(r.args).toEqual([[{ coll: NFT, id: 1n, price: E, duration: 86400n }]]);
    expect(() => buildBatchList([])).toThrowError(TxError);
    expect(() => buildBatchList(Array.from({ length: 51 }, (_, i) => ({ coll: NFT, id: BigInt(i), price: E, duration: 86400 })))).toThrowError(TxError);
  });
  it('throws a clear error when the chain has no marketplace', () => {
    window.MW_MARKETPLACE = ''; _resetChainCache();
    expect(() => buildBuy(NFT, 1n, SELLER, E)).toThrowError(/not live/);
  });
});

describe('auction builders', () => {
  it('create: (coll,id,reserve,duration) — v3.3, no increment params', () => {
    const r = buildCreate(NFT, 9n, E, 14400);
    expect(r.address).toBe(AH);
    expect(r.functionName).toBe('create');
    expect(r.args).toEqual([NFT, 9n, E, 14400n]);
  });
  it('create1155 inserts amount', () => {
    expect(buildCreate(NFT, 9n, E, 14400, 'erc1155', 3n).args).toEqual([NFT, 9n, 3n, E, 14400n]);
  });
  it('create accepts exactly the fourteen shared durations', () => {
    for (const d of DURATIONS) expect(() => buildCreate(NFT, 1n, E, d.seconds)).not.toThrow();
    for (const bad of [0, 1, 120, 420, 7201, 86401, 90000]) expect(() => buildCreate(NFT, 1n, E, bad)).toThrowError(/durations/);
    expect(() => buildCreate(NFT, 1n, E, 120, 'erc1155', 2n)).toThrowError(/durations/);
  });
  it('forceCancel: (id) only; unlocks at endsAt + 3 days', () => {
    const r = buildForceCancel(42n);
    expect(r.address).toBe(AH); expect(r.functionName).toBe('forceCancel'); expect(r.args).toEqual([42n]); expect(r.value).toBeUndefined();
    const ends = 1_700_000_000;
    expect(FORCE_CANCEL_WINDOW_SEC).toBe(3 * 86400);
    expect(forceCancelUnlocked(ends, (ends + FORCE_CANCEL_WINDOW_SEC - 1) * 1000)).toBe(false);
    expect(forceCancelUnlocked(ends, (ends + FORCE_CANCEL_WINDOW_SEC) * 1000)).toBe(true);
  });
  it('bid is payable with the top-up amount', () => {
    const r = buildBid(42n, 2n * E);
    expect(r.args).toEqual([42n]); expect(r.value).toBe(2n * E);
    expect(() => buildBid(42n, 0n)).toThrowError(TxError);
  });
  it('minimumTopUp: reserve when no bids, +1 native over leader, cumulative-aware', () => {
    expect(minimumTopUp({ currentHighestWei: 0n, reserveWei: 5n * E, myCumulativeWei: 0n })).toBe(5n * E);
    expect(minimumTopUp({ currentHighestWei: 10n * E, reserveWei: 5n * E, myCumulativeWei: 0n })).toBe(10n * E + MIN_BID_INCREMENT_WEI);
    expect(minimumTopUp({ currentHighestWei: 10n * E, reserveWei: 5n * E, myCumulativeWei: 8n * E })).toBe(3n * E);
    expect(minimumTopUp({ currentHighestWei: 10n * E, reserveWei: 5n * E, myCumulativeWei: 20n * E })).toBe(0n);
  });
});

describe('offer builders', () => {
  it('makeOffer escrows principal as value; duration last', () => {
    const r = buildMakeOffer(NFT, 3n, 2n * E, 1800);
    expect(r.address).toBe(OB);
    expect(r.functionName).toBe('makeOffer');
    expect(r.args).toEqual([NFT, 3n, 2n * E, 1800n]);
    expect(r.value).toBe(2n * E);
  });
  it('makeOffer1155 adds units before duration', () => {
    expect(buildMakeOffer(NFT, 3n, 2n * E, 1800, 'erc1155', 4n).args).toEqual([NFT, 3n, 2n * E, 4n, 1800n]);
  });
  it('acceptOffer pins expectedPrincipal', () => {
    expect(buildAcceptOffer(NFT, 3n, SELLER, 2n * E).args).toEqual([NFT, 3n, SELLER, 2n * E]);
  });
});
