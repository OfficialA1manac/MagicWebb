export const OfferBookAbi = [
  {type: "function", name: "deposit", stateMutability: "payable", inputs: [], outputs: []},
  {type: "function", name: "withdraw", stateMutability: "nonpayable",
    inputs: [{name: "amount", type: "uint256"}], outputs: []},
  {type: "function", name: "cancelOffer", stateMutability: "nonpayable",
    inputs: [{name: "nonce", type: "uint64"}], outputs: []},
  {type: "function", name: "acceptOffer", stateMutability: "nonpayable",
    inputs: [
      {name: "o", type: "tuple", components: [
        {name: "bidder", type: "address"},
        {name: "collection", type: "address"},
        {name: "tokenId", type: "uint256"},
        {name: "amount", type: "uint128"},
        {name: "expiresAt", type: "uint64"},
        {name: "nonce", type: "uint64"}
      ]},
      {name: "sig", type: "bytes"},
      {name: "tokenIdActual", type: "uint256"}
    ], outputs: []},
  {type: "function", name: "deposits", stateMutability: "view",
    inputs: [{name: "", type: "address"}], outputs: [{type: "uint256"}]},
  {type: "function", name: "usedNonce", stateMutability: "view",
    inputs: [{name: "", type: "address"}, {name: "", type: "uint64"}], outputs: [{type: "bool"}]},
  {type: "function", name: "hashOffer", stateMutability: "view",
    inputs: [{name: "o", type: "tuple", components: [
      {name: "bidder", type: "address"},
      {name: "collection", type: "address"},
      {name: "tokenId", type: "uint256"},
      {name: "amount", type: "uint128"},
      {name: "expiresAt", type: "uint64"},
      {name: "nonce", type: "uint64"}
    ]}],
    outputs: [{type: "bytes32"}]},
  {type: "event", name: "OfferAccepted", inputs: [
    {indexed: true, name: "coll", type: "address"},
    {indexed: true, name: "tokenId", type: "uint256"},
    {indexed: true, name: "seller", type: "address"},
    {indexed: false, name: "bidder", type: "address"},
    {indexed: false, name: "amount", type: "uint128"},
    {indexed: false, name: "fee", type: "uint256"},
    {indexed: false, name: "nonce", type: "uint64"}
  ]}
] as const;
