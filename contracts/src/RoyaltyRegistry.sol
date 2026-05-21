// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";
import {IERC2981} from "@openzeppelin/contracts/interfaces/IERC2981.sol";
import {IERC165} from "@openzeppelin/contracts/utils/introspection/IERC165.sol";

error InvalidFeeBps();
error ZeroAddress();

/// @title RoyaltyRegistry
/// @notice ERC-2981-compatible royalty registry for Magic Webb.
/// @dev Two-tier lookup:
///   1. On-chain per-token override stored here (highest priority).
///   2. Per-collection default stored here.
///   Marketplace contracts first try calling IERC2981 on the NFT contract directly;
///   only if that returns zero/fails does the registry act as the fallback source.
///   ROYALTY_SETTER_ROLE is intended for the Safe multi-sig admin.
contract RoyaltyRegistry is AccessControl {
    bytes32 public constant ROYALTY_SETTER_ROLE = keccak256("ROYALTY_SETTER_ROLE");

    /// @dev Max royalty enforced by this registry: 25% (2500 bps).
    uint16 public constant MAX_ROYALTY_BPS = 2_500;

    struct RoyaltyEntry {
        address receiver;
        uint16  feeBps;
    }

    /// @notice Per-collection default royalty.
    mapping(address => RoyaltyEntry) private _collectionRoyalty;
    /// @notice Per-token royalty override. Non-zero `feeBps` takes priority over collection default.
    mapping(address => mapping(uint256 => RoyaltyEntry)) private _tokenRoyalty;

    event CollectionRoyaltySet(address indexed collection, address indexed receiver, uint16 feeBps);
    event CollectionRoyaltyCleared(address indexed collection);
    event TokenRoyaltySet(address indexed collection, uint256 indexed tokenId, address indexed receiver, uint16 feeBps);
    event TokenRoyaltyCleared(address indexed collection, uint256 indexed tokenId);

    constructor(address admin) {
        if (admin == address(0)) revert ZeroAddress();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(ROYALTY_SETTER_ROLE, admin);
    }

    // ── Setters ──────────────────────────────────────────────────────────

    function setCollectionRoyalty(address collection, address receiver, uint16 feeBps)
        external onlyRole(ROYALTY_SETTER_ROLE)
    {
        if (collection == address(0) || receiver == address(0)) revert ZeroAddress();
        if (feeBps > MAX_ROYALTY_BPS) revert InvalidFeeBps();
        _collectionRoyalty[collection] = RoyaltyEntry(receiver, feeBps);
        emit CollectionRoyaltySet(collection, receiver, feeBps);
    }

    function clearCollectionRoyalty(address collection) external onlyRole(ROYALTY_SETTER_ROLE) {
        delete _collectionRoyalty[collection];
        emit CollectionRoyaltyCleared(collection);
    }

    function setTokenRoyalty(address collection, uint256 tokenId, address receiver, uint16 feeBps)
        external onlyRole(ROYALTY_SETTER_ROLE)
    {
        if (collection == address(0) || receiver == address(0)) revert ZeroAddress();
        if (feeBps > MAX_ROYALTY_BPS) revert InvalidFeeBps();
        _tokenRoyalty[collection][tokenId] = RoyaltyEntry(receiver, feeBps);
        emit TokenRoyaltySet(collection, tokenId, receiver, feeBps);
    }

    function clearTokenRoyalty(address collection, uint256 tokenId) external onlyRole(ROYALTY_SETTER_ROLE) {
        delete _tokenRoyalty[collection][tokenId];
        emit TokenRoyaltyCleared(collection, tokenId);
    }

    // ── Query ─────────────────────────────────────────────────────────────

    /// @notice Returns the royalty receiver and amount for a given sale.
    ///         Per-token override > collection default. Returns (address(0), 0) when no royalty applies.
    ///         This is called by marketplace contracts ONLY when the NFT collection itself returns
    ///         zero from ERC-2981 (or doesn't implement it).
    function getRoyalty(address collection, uint256 tokenId, uint256 salePrice)
        external view returns (address receiver, uint256 amount)
    {
        RoyaltyEntry storage tokenEntry = _tokenRoyalty[collection][tokenId];
        if (tokenEntry.feeBps > 0) {
            return (tokenEntry.receiver, (salePrice * tokenEntry.feeBps) / 10_000);
        }
        RoyaltyEntry storage collEntry = _collectionRoyalty[collection];
        if (collEntry.feeBps > 0) {
            return (collEntry.receiver, (salePrice * collEntry.feeBps) / 10_000);
        }
        return (address(0), 0);
    }

    // ── View helpers ──────────────────────────────────────────────────────

    function collectionRoyalty(address collection) external view returns (address receiver, uint16 feeBps) {
        RoyaltyEntry storage e = _collectionRoyalty[collection];
        return (e.receiver, e.feeBps);
    }

    function tokenRoyalty(address collection, uint256 tokenId) external view returns (address receiver, uint16 feeBps) {
        RoyaltyEntry storage e = _tokenRoyalty[collection][tokenId];
        return (e.receiver, e.feeBps);
    }
}
