package chain

import (
	"context"
	"math/big"
	"strings"
	"unicode/utf8"

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

var (
	nameSelector   = crypto.Keccak256([]byte("name()"))[:4]
	symbolSelector = crypto.Keccak256([]byte("symbol()"))[:4]
)

// CollectionInfo reads the ERC-721/1155 metadata extension's name() and
// symbol(). Both are optional in the standards, so a contract that omits them
// is not an error — it yields empty strings and the caller keeps whatever it
// already had.
//
// Callers MUST NOT overwrite stored values with the empty string: a chain blip
// and a contract that genuinely has no name are indistinguishable here, and
// blanking a good name because one eth_call failed is the worse outcome.
func CollectionInfo(ctx context.Context, eth Caller, collection string) (name, symbol string, err error) {
	name, err = callString(ctx, eth, collection, nameSelector)
	if err != nil {
		return "", "", err
	}
	symbol, err = callString(ctx, eth, collection, symbolSelector)
	if err != nil {
		return name, "", err
	}
	return name, symbol, nil
}

var ownerSelector = crypto.Keccak256([]byte("owner()"))[:4] // ERC-173, 0x8da5cb5b

// Owner reads the ERC-173 owner() of a contract and returns it as a lowercase
// 0x-prefixed hex address — the normalization used for every address stored by
// this backend. Ownable is optional, so a contract without owner() is not an
// error: it yields "" and the caller keeps whatever it already had. The zero
// address (ownership renounced or never set) also yields "" — it names nobody
// and storing it would only masquerade as a real creator.
//
// Like CollectionInfo, callers MUST NOT overwrite a stored value with "": an
// unimplemented getter and a chain blip are indistinguishable here.
func Owner(ctx context.Context, eth Caller, collection string) (string, error) {
	to := common.HexToAddress(collection)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: ownerSelector}, nil)
	if err != nil {
		if isContractRefusal(err) {
			return "", nil
		}
		return "", err
	}
	// An address occupies the last 20 bytes of a 32-byte return word. A short
	// return is a fallback function answering a getter it does not implement.
	if len(out) < 32 {
		return "", nil
	}
	addr := common.BytesToAddress(out[12:32])
	if addr == (common.Address{}) {
		return "", nil
	}
	return strings.ToLower(addr.Hex()), nil
}

// callString performs a no-argument eth_call and decodes the result as a
// solidity string. A revert means the contract does not implement the getter,
// which is a definitive empty answer rather than a failure.
func callString(ctx context.Context, eth Caller, collection string, selector []byte) (string, error) {
	to := common.HexToAddress(collection)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: selector}, nil)
	if err != nil {
		if isContractRefusal(err) {
			return "", nil
		}
		return "", err
	}
	return decodeString(out), nil
}

// decodeString handles both shapes seen in the wild: the ABI dynamic string
// (offset, length, bytes) and the pre-ABI bytes32 that early NFT contracts
// returned for name/symbol.
func decodeString(out []byte) string {
	switch {
	case len(out) == 0:
		return ""
	case len(out) >= 64:
		offset := new(big.Int).SetBytes(out[:32]).Uint64()
		// A bytes32-style return has no valid offset; fall through to the
		// fixed-width branch rather than indexing past the buffer.
		if offset == 32 && uint64(len(out)) >= 64 {
			length := new(big.Int).SetBytes(out[32:64]).Uint64()
			if 64+length <= uint64(len(out)) {
				return sanitizeUTF8(string(out[64 : 64+length]))
			}
		}
		fallthrough
	case len(out) >= 32:
		return sanitizeUTF8(strings.TrimRight(string(out[:32]), "\x00"))
	default:
		return ""
	}
}

// sanitizeUTF8 drops control characters and invalid runes. Collection names are
// attacker-controlled: they reach templates, JSON and search, so anything that
// could smuggle a newline or a NUL into a log line or a header is removed here,
// at the boundary, rather than trusted downstream.
func sanitizeUTF8(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
