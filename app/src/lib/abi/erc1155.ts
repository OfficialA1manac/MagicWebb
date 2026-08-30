// Minimal ERC-1155 surface used by the marketplace UI.
export const erc1155Abi = [
  { type: 'function', name: 'balanceOf', stateMutability: 'view', inputs: [{ name: 'account', type: 'address' }, { name: 'id', type: 'uint256' }], outputs: [{ name: '', type: 'uint256' }] },
  { type: 'function', name: 'uri', stateMutability: 'view', inputs: [{ name: 'id', type: 'uint256' }], outputs: [{ name: '', type: 'string' }] },
  { type: 'function', name: 'isApprovedForAll', stateMutability: 'view', inputs: [{ name: 'account', type: 'address' }, { name: 'operator', type: 'address' }], outputs: [{ name: '', type: 'bool' }] },
  { type: 'function', name: 'setApprovalForAll', stateMutability: 'nonpayable', inputs: [{ name: 'operator', type: 'address' }, { name: 'approved', type: 'bool' }], outputs: [] },
  { type: 'function', name: 'supportsInterface', stateMutability: 'view', inputs: [{ name: 'interfaceId', type: 'bytes4' }], outputs: [{ name: '', type: 'bool' }] },
] as const;
