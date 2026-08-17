package chain

import (
	"context"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ERC-165 interface identifiers. These are the canonical values from the
// standards themselves — the XOR of every function selector in the interface.
var (
	InterfaceERC721  = [4]byte{0x80, 0xac, 0x58, 0xcd}
	InterfaceERC1155 = [4]byte{0xd9, 0xb6, 0x7a, 0x26}
)

var supportsInterfaceSelector = crypto.Keccak256([]byte("supportsInterface(bytes4)"))[:4]

// SupportsInterface reports whether a contract answers true to the ERC-165
// supportsInterface(bytes4) probe.
//
// Two failure shapes must not be confused. A contract that does not implement
// ERC-165 at all reverts (or returns nothing) — that is a definitive "no", and
// comes back as (false, nil). An RPC transport failure tells us nothing about
// the contract and comes back as (false, err), so callers can retry rather than
// record a false negative.
func SupportsInterface(ctx context.Context, eth Caller, collection string, id [4]byte) (bool, error) {
	// bytes4 is a left-aligned ABI type: the four identifier bytes occupy
	// positions 0-3 of the word and the remaining 28 bytes are zero. Right
	// -aligning it (the layout used for uint256 and address) would probe a
	// different, always-unsupported interface.
	arg := make([]byte, 32)
	copy(arg, id[:])
	data := append(append([]byte(nil), supportsInterfaceSelector...), arg...)

	to := common.HexToAddress(collection)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		if isContractRefusal(err) {
			return false, nil
		}
		return false, err
	}
	// A bool occupies a full word with the value in the last byte. A short or
	// empty return is what a non-ERC-165 contract with a fallback function
	// gives back; treat it as "no" rather than an error.
	if len(out) < 32 {
		return false, nil
	}
	return out[31] == 1, nil
}

// DetectStandard returns "erc721", "erc1155", or "" for a collection that
// declares neither. A non-nil error means the chain could not be reached and
// the answer is unknown — it is never a statement about the contract.
func DetectStandard(ctx context.Context, eth Caller, collection string) (string, error) {
	is721, err := SupportsInterface(ctx, eth, collection, InterfaceERC721)
	if err != nil {
		return "", err
	}
	if is721 {
		return "erc721", nil
	}
	is1155, err := SupportsInterface(ctx, eth, collection, InterfaceERC1155)
	if err != nil {
		return "", err
	}
	if is1155 {
		return "erc1155", nil
	}
	return "", nil
}

// isContractRefusal reports whether an eth_call error came from the EVM
// rejecting the call rather than from the transport. go-ethereum collapses both
// into a plain error, so the message is the only signal available.
func isContractRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"execution reverted",
		"invalid opcode",
		"invalid jump",
		"out of gas",
		"no contract code",
		"contract creation without any data",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
