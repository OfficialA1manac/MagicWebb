// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Shared helpers for deploying the v3.4 contract set in tests.
///         v3.4: feeRecipient + manager are implementation-constructor args
///         (immutables); the proxy initializer takes no arguments. The
///         manager is a PLAIN contract — no proxy.
contract TestHelpers {
    function _deployAuctionHouse(address recipient, address manager_)
        internal returns (AuctionHouse)
    {
        AuctionHouse impl = new AuctionHouse(recipient, manager_);
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(AuctionHouse.initialize.selector)
        );
        return AuctionHouse(address(proxy));
    }

    function _deployMarketplace(address recipient, address manager_)
        internal returns (Marketplace)
    {
        Marketplace impl = new Marketplace(recipient, manager_);
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(Marketplace.initialize.selector)
        );
        return Marketplace(address(proxy));
    }

    function _deployOfferBook(address recipient, address manager_)
        internal returns (OfferBook)
    {
        OfferBook impl = new OfferBook(recipient, manager_);
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(OfferBook.initialize.selector)
        );
        return OfferBook(address(proxy));
    }

    /// @dev The manager constructor takes (admin, keeper). The 1-arg helper
    ///      keeps old call sites compiling by installing a sentinel keeper
    ///      that no test impersonates; tests that need a specific keeper call
    ///      `setKeeper` as the admin, or use the 2-arg overload.
    address internal constant TEST_SENTINEL_KEEPER = address(uint160(uint256(keccak256("mw.test.sentinel-keeper"))));

    function _deployMarketplaceManager(address admin)
        internal returns (MarketplaceManager)
    {
        return _deployMarketplaceManager(admin, TEST_SENTINEL_KEEPER);
    }

    function _deployMarketplaceManager(address admin, address keeper)
        internal returns (MarketplaceManager)
    {
        // v3.4: plain contract, no proxy, no initializer.
        return new MarketplaceManager(admin, keeper);
    }
}
