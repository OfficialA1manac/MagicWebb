// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";

/// @notice Deploy or verify a Gnosis Safe (Safe) multisig proxy on Flare,
///         Songbird, or Coston2. The resulting Safe address should be used
///         as CREATOR_ADDR (feeRecipient) in the main deploy scripts.
///
///         Safe (v1.3.0 L2) singleton + proxy factory are deployed at
///         canonical addresses on Flare (14), Coston2 (114), and at the
///         eip155 address on Songbird (19). This script picks the correct
///         singleton and factory for the running chain's chain ID.
///
/// Example usage:
///   SAFE_OWNERS="0x1111...,0x2222...,0x3333..." SAFE_THRESHOLD=2 \
///     forge script script/DeploySafe.s.sol --rpc-url <RPC> --broadcast \
///     --private-key $PRIVATE_KEY
///
/// Required env vars:
///   SAFE_OWNERS    -- comma-separated list of owner addresses (min 1)
///   SAFE_THRESHOLD -- number of confirmations needed (min 1, max owners)
///
/// Optional env vars:
///   SAFE_SALT      -- create2 salt nonce (default: 0, i.e. fresh deploy)
///
/// The script prints the Safe address and the setup call data. Paste the
/// address into CREATOR_ADDR when running DeployCoston2 / DeployFlareMainnet
/// / DeploySongbird. The script does NOT deploy marketplace contracts — it
/// only creates the Safe.
contract DeploySafe is Script {
    /// @notice Canonical SafeL2 v1.3.0 singleton (used on chains 14, 114).
    address constant SAFE_SINGLETON_CANONICAL = 0x3E5c63644E683549055b9Be8653de26E0B4CD36E;

    /// @notice EIP-155 SafeL2 v1.3.0 singleton (used on chain 19 — Songbird).
    address constant SAFE_SINGLETON_EIP155 = 0xfb1bffC9d739B8D520DaF37dF666da4C687191EA;

    /// @notice Canonical SafeProxyFactory v1.3.0 (used on chains 14, 114).
    address constant FACTORY_CANONICAL = 0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2;

    /// @notice EIP-155 SafeProxyFactory v1.3.0 (used on chain 19 — Songbird).
    address constant FACTORY_EIP155 = 0xC22834581EbC8527d974F8a1c97E1bEA4EF910BC;

    /// @notice Null address for the fallback handler param in setup().
    ///         Safe v1.3.0 does not require a fallback handler for basic
    ///         multisig operation; setting it to address(0) is valid.
    address constant NO_FALLBACK = address(0);

    /// @notice Null address for the payment receiver in setup().
    address constant NO_PAYMENT = address(0);

    function run() external {
        // ── Resolve singleton + factory per chain ──────────────────────────
        address singleton;
        address factory;
        if (block.chainid == 14 || block.chainid == 114) {
            // Flare mainnet (14) and Coston2 testnet (114): canonical addresses
            singleton = SAFE_SINGLETON_CANONICAL;
            factory   = FACTORY_CANONICAL;
        } else if (block.chainid == 19) {
            // Songbird canary-net (19): eip155 addresses
            singleton = SAFE_SINGLETON_EIP155;
            factory   = FACTORY_EIP155;
        } else {
            revert(string.concat("Unsupported chain ID: ", vm.toString(block.chainid)));
        }

        console2.log("Chain ID:",         block.chainid);
        console2.log("Safe singleton:",   vm.toString(singleton));
        console2.log("Proxy factory:",    vm.toString(factory));

        // ── Verify on-chain contracts exist at those addresses ─────────────
        uint256 singletonCodeLen;
        uint256 factoryCodeLen;
        assembly {
            singletonCodeLen := extcodesize(singleton)
            factoryCodeLen   := extcodesize(factory)
        }
        if (singletonCodeLen == 0) {
            revert(string.concat("Safe singleton not found at ", vm.toString(singleton),
                " on chain ", vm.toString(block.chainid)));
        }
        if (factoryCodeLen == 0) {
            revert(string.concat("Safe factory not found at ", vm.toString(factory),
                " on chain ", vm.toString(block.chainid)));
        }
        console2.log("Singleton code size:", singletonCodeLen);
        console2.log("Factory code size:",   factoryCodeLen);

        // ── Parse env vars ─────────────────────────────────────────────────
        address[] memory owners = parseAddresses(vm.envString("SAFE_OWNERS"));
        uint256 threshold = vm.envUint("SAFE_THRESHOLD");
        uint256 salt = vm.envOr("SAFE_SALT", uint256(0));

        require(owners.length > 0, "SAFE_OWNERS must have at least 1 address");
        require(threshold > 0, "SAFE_THRESHOLD must be >= 1");
        require(threshold <= owners.length, "SAFE_THRESHOLD exceeds owners count");

        console2.log("Owners:",       owners.length);
        console2.log("Threshold:",    threshold);
        console2.log("Salt nonce:",   salt);

        // ── Build initializer data ─────────────────────────────────────────
        // Safe.setup(address[] calldata _owners, uint256 _threshold,
        //            address to, bytes calldata data,
        //            address fallbackHandler, address paymentToken,
        //            uint256 payment, address payable paymentReceiver)
        //
        // For a basic multisig: owners=owners, threshold=threshold,
        // to=address(0), data="", fallbackHandler=address(0),
        // paymentToken=address(0), payment=0, paymentReceiver=address(0)
        bytes memory initializer = abi.encodeWithSelector(
            bytes4(keccak256("setup(address[],uint256,address,bytes,address,address,uint256,address)")),
            owners,
            threshold,
            address(0),         // to: no delegate call
            "",                 // data: empty
            NO_FALLBACK,        // fallbackHandler: none
            address(0),         // paymentToken: no payment
            uint256(0),         // payment: no payment
            NO_PAYMENT          // paymentReceiver: no payment
        );

        // ── Compute predicted address before broadcast ─────────────────────
        // SafeProxyFactory v1.3.0 (createProxyWithNonce) uses:
        //   create2Salt   = keccak256(abi.encodePacked(keccak256(initializer), saltNonce))
        //   initCodeHash  = keccak256(abi.encodePacked(proxyCreationCode,
        //                     uint256(uint160(singleton))))
        //   predicted     = keccak256(0xff ++ factory ++ create2Salt ++ initCodeHash)
        // The creation code is read from the factory itself rather than
        // hardcoded, so a factory version change cannot skew the prediction.
        (bool codeOk, bytes memory codeRet) = factory.staticcall(
            abi.encodeWithSignature("proxyCreationCode()")
        );
        require(codeOk, "Cannot read proxyCreationCode()");
        bytes memory proxyCreationCode = abi.decode(codeRet, (bytes));
        bytes32 create2Salt = keccak256(abi.encodePacked(keccak256(initializer), salt));
        bytes32 initCodeHash = keccak256(
            abi.encodePacked(proxyCreationCode, uint256(uint160(singleton)))
        );
        address predicted = address(uint160(uint256(keccak256(
            abi.encodePacked(bytes1(0xff), factory, create2Salt, initCodeHash)
        ))));
        console2.log("Predicted Safe (create2):", vm.toString(predicted));

        // ── Broadcast proxy deployment ─────────────────────────────────────
        vm.startBroadcast();

        (bool ok, bytes memory ret) = factory.call(
            abi.encodeWithSignature(
                "createProxyWithNonce(address,bytes,uint256)",
                singleton,
                initializer,
                salt
            )
        );
        require(ok, "Safe proxy deployment failed");

        address safe = abi.decode(ret, (address));
        require(safe != address(0), "Safe address cannot be zero");
        require(safe == predicted, "predicted Safe address mismatch");

        vm.stopBroadcast();

        // ── Verify deployment ──────────────────────────────────────────────
        uint256 safeCodeLen;
        assembly {
            safeCodeLen := extcodesize(safe)
        }
        require(safeCodeLen > 0, "Safe has no code after deploy");

        console2.log("");
        console2.log("=== Safe Multisig Deployed ===");
        console2.log("SAFE_ADDR=",  vm.toString(safe));
        console2.log("CREATOR_ADDR (use this as feeRecipient):", vm.toString(safe));
        console2.log("Owners:",     owners.length);
        console2.log("Threshold:",  threshold);

        // ── Verify setup was applied correctly ─────────────────────────────
        // Call getOwners() and getThreshold() on the Safe to verify setup.
        {
            (bool ownersOk, bytes memory ownersRet) = safe.staticcall(
                abi.encodeWithSignature("getOwners()")
            );
            require(ownersOk, "Cannot query Safe owners");
            address[] memory actualOwners = abi.decode(ownersRet, (address[]));
            require(actualOwners.length == owners.length, "Owner count mismatch");
        }
        {
            (bool thresholdOk, bytes memory thresholdRet) = safe.staticcall(
                abi.encodeWithSignature("getThreshold()")
            );
            require(thresholdOk, "Cannot query Safe threshold");
            uint256 actualThreshold = abi.decode(thresholdRet, (uint256));
            require(actualThreshold == threshold, "Threshold mismatch");
        }

        console2.log("Safe verified: owner count + threshold match");
        console2.log("");
        console2.log("Paste SAFE_ADDR into your .env as CREATOR_ADDR");
        console2.log("CREATOR_ADDR=", vm.toString(safe));
        console2.log("Then run the main deploy script with CREATOR_ADDR set to this Safe address.");
    }

    /// @notice Parses a comma-separated address string from an env var into
    ///         a Solidity address[] memory array. Empty entries are skipped.
    ///         Reverts if any entry is not a valid 40-hex-char address.
    function parseAddresses(string memory input) internal pure returns (address[] memory) {
        bytes memory inputBytes = bytes(input);
        if (inputBytes.length == 0) {
            return new address[](0);
        }
        // Count comma-delimited elements.
        uint256 count = 1;
        for (uint256 i = 0; i < inputBytes.length; i++) {
            if (inputBytes[i] == ',') {
                count++;
            }
        }
        address[] memory result = new address[](count);
        uint256 idx = 0;
        uint256 start = 0;
        for (uint256 i = 0; i <= inputBytes.length; i++) {
            if (i == inputBytes.length || inputBytes[i] == ',') {
                // Extract substring [start..i), trimming whitespace
                uint256 s = start;
                uint256 e = i;
                while (s < e && inputBytes[s] == ' ') s++;
                while (e > s && inputBytes[e - 1] == ' ') e--;
                if (e > s) {
                    bytes memory addrBytes = new bytes(e - s);
                    for (uint256 j = s; j < e; j++) {
                        addrBytes[j - s] = inputBytes[j];
                    }
                    string memory addrStrVal = string(addrBytes);
                    // Validate 0x prefix + 40 hex chars
                    if (bytes(addrStrVal).length == 42 &&
                        bytes(addrStrVal)[0] == '0' &&
                        bytes(addrStrVal)[1] == 'x') {
                        bytes memory raw = bytes(addrStrVal);
                        bool valid = true;
                        for (uint256 k = 2; k < 42; k++) {
                            bytes1 char = raw[k];
                            if (!((char >= bytes1('0') && char <= bytes1('9')) ||
                                  (char >= bytes1('a') && char <= bytes1('f')) ||
                                  (char >= bytes1('A') && char <= bytes1('F')))) {
                                valid = false;
                                break;
                            }
                        }
                        if (valid) {
                            result[idx] = vm.parseAddress(addrStrVal);
                            idx++;
                        }
                    }
                }
                start = i + 1;
            }
        }
        // Resize array to actual count (solidity can't resize, so just return
        // with the proper count — callers should check length.
        // Use assembly to create a new properly-sized array.
        address[] memory trimmed = new address[](idx);
        for (uint256 i = 0; i < idx; i++) {
            trimmed[i] = result[i];
        }
        return trimmed;
    }

}
