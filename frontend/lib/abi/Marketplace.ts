export const MarketplaceAbi = [
  {type: "function", name: "list", stateMutability: "nonpayable",
    inputs: [
      {name: "coll", type: "address"},
      {name: "id", type: "uint256"},
      {name: "price", type: "uint128"},
      {name: "expiresAt", type: "uint64"}
    ], outputs: []},
  {type: "function", name: "list1155", stateMutability: "nonpayable",
    inputs: [
      {name: "coll", type: "address"},
      {name: "id", type: "uint256"},
      {name: "amount", type: "uint128"},
      {name: "price", type: "uint128"},
      {name: "expiresAt", type: "uint64"}
    ], outputs: []},
  {type: "function", name: "cancel", stateMutability: "nonpayable",
    inputs: [{name: "coll", type: "address"}, {name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "buy", stateMutability: "payable",
    inputs: [{name: "coll", type: "address"}, {name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "listings", stateMutability: "view",
    inputs: [{name: "", type: "address"}, {name: "", type: "uint256"}],
    outputs: [
      {name: "seller", type: "address"},
      {name: "expiresAt", type: "uint64"},
      {name: "standard", type: "uint8"},
      {name: "price", type: "uint128"},
      {name: "amount", type: "uint128"}
    ]},
  {type: "function", name: "feeBps", stateMutability: "view", inputs: [], outputs: [{type: "uint16"}]},
  {type: "event", name: "Listed", inputs: [
    {indexed: true,  name: "coll",      type: "address"},
    {indexed: true,  name: "id",        type: "uint256"},
    {indexed: true,  name: "seller",    type: "address"},
    {indexed: false, name: "standard",  type: "uint8"},
    {indexed: false, name: "amount",    type: "uint128"},
    {indexed: false, name: "price",     type: "uint128"},
    {indexed: false, name: "expiresAt", type: "uint64"}
  ]},
  {type: "event", name: "Cancelled", inputs: [
    {indexed: true, name: "coll",   type: "address"},
    {indexed: true, name: "id",     type: "uint256"},
    {indexed: true, name: "seller", type: "address"}
  ]},
  {type: "event", name: "Bought", inputs: [
    {indexed: true,  name: "coll",     type: "address"},
    {indexed: true,  name: "id",       type: "uint256"},
    {indexed: true,  name: "buyer",    type: "address"},
    {indexed: false, name: "seller",   type: "address"},
    {indexed: false, name: "standard", type: "uint8"},
    {indexed: false, name: "amount",   type: "uint128"},
    {indexed: false, name: "price",    type: "uint128"},
    {indexed: false, name: "fee",      type: "uint256"}
  ]}
] as const;
