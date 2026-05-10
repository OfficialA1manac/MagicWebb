// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {MarketplaceCore} from "./MarketplaceCore.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";

error NotOwner();
error NotListed();
error WrongPrice();
error Expired();
error NotApproved();
error InvalidExpiry();

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 tokens.
/// @dev Non-custodial: NFT stays with seller until a buyer settles. Approval to this contract is required.
contract Marketplace is MarketplaceCore {
    /// @notice Listing record. Two slots: (seller + expiresAt) | (price).
    struct Listing {
        address seller;     // slot 0 lower 20 bytes
        uint64  expiresAt;  // slot 0 next 8 bytes
        uint128 price;      // slot 1 lower 16 bytes
    }

    /// @notice listings[collection][tokenId] => Listing
    mapping(address => mapping(uint256 => Listing)) public listings;

    event Listed(address indexed coll, uint256 indexed id, address indexed seller, uint128 price, uint64 expiresAt);
    event Cancelled(address indexed coll, uint256 indexed id, address indexed seller);
    event Bought(address indexed coll, uint256 indexed id, address indexed buyer, address seller, uint128 price, uint256 fee);

    constructor(address admin, address vault, uint16 fee) MarketplaceCore(admin, vault, fee) {}

    /// @notice List a token at a fixed price. Caller must own and have approved this contract for the token.
    function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external whenNotPaused {
        if (price == 0) revert WrongPrice();
        if (expiresAt <= block.timestamp) revert InvalidExpiry();
        if (IERC721(coll).ownerOf(id) != msg.sender) revert NotOwner();
        if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
            && IERC721(coll).getApproved(id) != address(this)) revert NotApproved();

        listings[coll][id] = Listing({seller: msg.sender, expiresAt: expiresAt, price: price});
        emit Listed(coll, id, msg.sender, price, expiresAt);
    }

    /// @notice Cancel an active listing. Seller only.
    function cancel(address coll, uint256 id) external {
        Listing memory l = listings[coll][id];
        if (l.seller != msg.sender) revert NotOwner();
        delete listings[coll][id];
        emit Cancelled(coll, id, msg.sender);
    }

    /// @notice Buy a listed token at the listing price. Reverts if expired, missing, or wrong msg.value.
    function buy(address coll, uint256 id) external payable nonReentrant whenNotPaused {
        Listing memory l = listings[coll][id];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();
        if (msg.value != l.price) revert WrongPrice();

        delete listings[coll][id];

        _transferNFT(coll, l.seller, msg.sender, id);
        (uint256 fee,) = _splitAndPay(l.seller, msg.value);

        emit Bought(coll, id, msg.sender, l.seller, l.price, fee);
    }
}
