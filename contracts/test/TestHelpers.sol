// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";

/// @notice Shared helpers for deploying contracts behind ERC1967Proxy.
///         Ensures _disableInitializers() in constructors doesn't block
///         test initialization while maintaining production safety.
///         All helpers deploy the implementation, then wrap it in a proxy
///         that calls initialize() in the proxy's context.
contract TestHelpers {
    function _deployAuctionHouse(address recipient, address manager_)
        internal returns (AuctionHouse)
    {
        AuctionHouse impl = new AuctionHouse();
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(AuctionHouse.initialize.selector, recipient, manager_)
        );
        return AuctionHouse(address(proxy));
    }

    function _deployMarketplace(address recipient, address manager_)
        internal returns (Marketplace)
    {
        Marketplace impl = new Marketplace();
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(Marketplace.initialize.selector, recipient, manager_)
        );
        return Marketplace(address(proxy));
    }

    function _deployOfferBook(address recipient, address manager_)
        internal returns (OfferBook)
    {
        OfferBook impl = new OfferBook();
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(OfferBook.initialize.selector, recipient, manager_)
        );
        return OfferBook(address(proxy));
    }

    function _deployMarketplaceManager(address admin)
        internal returns (MarketplaceManager)
    {
        MarketplaceManager impl = new MarketplaceManager();
        ERC1967Proxy proxy = new ERC1967Proxy(
            address(impl),
            abi.encodeWithSelector(MarketplaceManager.initialize.selector, admin)
        );
        return MarketplaceManager(address(proxy));
    }
}
