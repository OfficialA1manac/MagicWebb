export const AuctionHouseAbi = [
  {type: "function", name: "create", stateMutability: "nonpayable",
    inputs: [
      {name: "coll", type: "address"},
      {name: "tokenId", type: "uint256"},
      {name: "reserve", type: "uint128"},
      {name: "startsAt", type: "uint64"},
      {name: "endsAt", type: "uint64"},
      {name: "minIncBps", type: "uint16"}
    ], outputs: [{name: "id", type: "uint256"}]},
  {type: "function", name: "bid", stateMutability: "payable",
    inputs: [{name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "settle", stateMutability: "nonpayable",
    inputs: [{name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "cancel", stateMutability: "nonpayable",
    inputs: [{name: "id", type: "uint256"}], outputs: []},
  {type: "function", name: "withdrawRefund", stateMutability: "nonpayable",
    inputs: [], outputs: []},
  {type: "function", name: "auctions", stateMutability: "view",
    inputs: [{name: "", type: "uint256"}],
    outputs: [
      {name: "seller", type: "address"},
      {name: "startsAt", type: "uint64"},
      {name: "minIncrementBps", type: "uint16"},
      {name: "settled", type: "bool"},
      {name: "collection", type: "address"},
      {name: "endsAt", type: "uint64"},
      {name: "tokenId", type: "uint256"},
      {name: "reserve", type: "uint128"},
      {name: "highestBid", type: "uint128"},
      {name: "highestBidder", type: "address"}
    ]},
  {type: "function", name: "pendingReturns", stateMutability: "view",
    inputs: [{name: "", type: "address"}], outputs: [{type: "uint256"}]},
  {type: "function", name: "nextAuctionId", stateMutability: "view", inputs: [], outputs: [{type: "uint256"}]},
  {type: "event", name: "AuctionCreated", inputs: [
    {indexed: true, name: "id", type: "uint256"},
    {indexed: true, name: "coll", type: "address"},
    {indexed: true, name: "tokenId", type: "uint256"},
    {indexed: false, name: "seller", type: "address"},
    {indexed: false, name: "reserve", type: "uint128"},
    {indexed: false, name: "startsAt", type: "uint64"},
    {indexed: false, name: "endsAt", type: "uint64"}
  ]},
  {type: "event", name: "BidPlaced", inputs: [
    {indexed: true, name: "id", type: "uint256"},
    {indexed: true, name: "bidder", type: "address"},
    {indexed: false, name: "amount", type: "uint128"}
  ]},
  {type: "event", name: "AuctionSettled", inputs: [
    {indexed: true, name: "id", type: "uint256"},
    {indexed: true, name: "winner", type: "address"},
    {indexed: true, name: "seller", type: "address"},
    {indexed: false, name: "amount", type: "uint128"},
    {indexed: false, name: "fee", type: "uint256"}
  ]},
  {type: "event", name: "RefundWithdrawn", inputs: [
    {indexed: true, name: "bidder", type: "address"},
    {indexed: false, name: "amount", type: "uint256"}
  ]}
] as const;
