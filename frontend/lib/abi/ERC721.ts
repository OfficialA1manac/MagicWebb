export const ERC721Abi = [
  {type: "function", name: "ownerOf", stateMutability: "view",
    inputs: [{name: "id", type: "uint256"}], outputs: [{type: "address"}]},
  {type: "function", name: "approve", stateMutability: "nonpayable",
    inputs: [{name: "to", type: "address"}, {name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "setApprovalForAll", stateMutability: "nonpayable",
    inputs: [{name: "operator", type: "address"}, {name: "approved", type: "bool"}], outputs: []},
  {type: "function", name: "isApprovedForAll", stateMutability: "view",
    inputs: [{name: "owner", type: "address"}, {name: "operator", type: "address"}], outputs: [{type: "bool"}]},
  {type: "function", name: "getApproved", stateMutability: "view",
    inputs: [{name: "id", type: "uint256"}], outputs: [{type: "address"}]},
  {type: "function", name: "tokenURI", stateMutability: "view",
    inputs: [{name: "id", type: "uint256"}], outputs: [{type: "string"}]},
  {type: "function", name: "name", stateMutability: "view", inputs: [], outputs: [{type: "string"}]},
  {type: "function", name: "symbol", stateMutability: "view", inputs: [], outputs: [{type: "string"}]}
] as const;
