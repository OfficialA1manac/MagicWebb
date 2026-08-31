// Minimal ERC-721 surface used by the marketplace UI.
export const erc721Abi = [
  { type: 'function', name: 'ownerOf', stateMutability: 'view', inputs: [{ name: 'tokenId', type: 'uint256' }], outputs: [{ name: '', type: 'address' }] },
  { type: 'function', name: 'tokenURI', stateMutability: 'view', inputs: [{ name: 'tokenId', type: 'uint256' }], outputs: [{ name: '', type: 'string' }] },
  { type: 'function', name: 'isApprovedForAll', stateMutability: 'view', inputs: [{ name: 'owner', type: 'address' }, { name: 'operator', type: 'address' }], outputs: [{ name: '', type: 'bool' }] },
  { type: 'function', name: 'setApprovalForAll', stateMutability: 'nonpayable', inputs: [{ name: 'operator', type: 'address' }, { name: 'approved', type: 'bool' }], outputs: [] },
  { type: 'function', name: 'supportsInterface', stateMutability: 'view', inputs: [{ name: 'interfaceId', type: 'bytes4' }], outputs: [{ name: '', type: 'bool' }] },
  // ERC-173 contract ownership. Not part of ERC-721 proper, but OfferBook's
  // setOfferEligible gates on it, so the UI reads it to decide whether the
  // connected wallet may enable offers. Contracts without it simply revert
  // the read and the caller treats ownership as unknown.
  { type: 'function', name: 'owner', stateMutability: 'view', inputs: [], outputs: [{ name: '', type: 'address' }] },
] as const;
