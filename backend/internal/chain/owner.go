// Package chain provides small eth_call helpers shared by the API and indexer.
package chain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Caller is the subset of an Ethereum client used for read-only NFT checks.
type Caller interface {
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

var (
	ownerOfSelector   = crypto.Keccak256([]byte("ownerOf(uint256)"))[:4]
	balanceOfSelector = crypto.Keccak256([]byte("balanceOf(address,uint256)"))[:4]
)

// OwnerOf721 returns the holder of an ERC-721 token via ownerOf(uint256).
func OwnerOf721(ctx context.Context, eth Caller, collection, tokenID string) (string, error) {
	id, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return "", fmt.Errorf("bad token id")
	}
	idBytes := make([]byte, 32)
	id.FillBytes(idBytes)
	data := append(append([]byte(nil), ownerOfSelector...), idBytes...)

	to := common.HexToAddress(collection)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return "", err
	}
	if len(out) < 32 {
		return "", fmt.Errorf("ownerOf: short return")
	}
	return common.BytesToAddress(out[12:32]).Hex(), nil
}

// Balance1155 returns an owner's ERC-1155 balance via balanceOf(address,uint256).
func Balance1155(ctx context.Context, eth Caller, collection, tokenID, owner string) (*big.Int, error) {
	id, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return nil, fmt.Errorf("bad token id")
	}
	ownerAddr := common.HexToAddress(owner)
	idBytes := make([]byte, 32)
	id.FillBytes(idBytes)
	ownerPadded := common.LeftPadBytes(ownerAddr.Bytes(), 32)
	data := append(append([]byte(nil), balanceOfSelector...), ownerPadded...)
	data = append(data, idBytes...)

	to := common.HexToAddress(collection)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("balanceOf: short return")
	}
	return new(big.Int).SetBytes(out), nil
}

// SameAddr compares two hex addresses case-insensitively.
func SameAddr(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
