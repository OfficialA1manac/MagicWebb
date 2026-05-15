// Deploy a minimal ERC-721 (same bytecode as contracts/test/MockERC721.sol) on Flare Coston2
// and mint token #1 to RECIPIENT. Requires funded deployer key on Coston2.
//
// Env:
//   RPC_URL          — default https://coston2-api.flare.network/ext/C/rpc
//   PRIVATE_KEY      — hex, 0x-prefixed; if unset, a random key is generated (must fund for deploy)
//   RECIPIENT        — default 0x6E3A86a52DD89Ac471e6Ae9e914668687DFf0456
//
// Build initcode file once: (from repo root) forge build --contracts test/MockERC721.sol -C contracts
//   then ensure devtools/mock721_initcode.hex exists (see Makefile target devtools-mock721-hex).
package main

import (
	"context"
	"crypto/ecdsa"
	_ "embed"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

//go:embed devtools/mock721_initcode.hex
var mock721InitCodeHex string

const defaultRPC = "https://coston2-api.flare.network/ext/C/rpc"
const defaultRecipient = "0x6E3A86a52DD89Ac471e6Ae9e914668687DFf0456"

func main() {
	ctx := context.Background()
	rpc := getenv("RPC_URL", defaultRPC)
	recipient := common.HexToAddress(getenv("RECIPIENT", defaultRecipient))

	initHex := strings.TrimSpace(strings.TrimPrefix(mock721InitCodeHex, "0x"))
	if initHex == "" {
		log.Fatal("embedded devtools/mock721_initcode.hex is empty — run: make devtools-mock721-hex (after forge build)")
	}
	initCode := common.FromHex("0x" + initHex)

	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		log.Fatalf("rpc dial: %v", err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("chain id: %v", err)
	}
	log.Printf("chainId=%s rpc=%s", chainID.String(), rpc)

	pk, addr, err := loadOrCreatePrivateKey()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("deployer=%s", addr.Hex())

	nonce, err := client.PendingNonceAt(ctx, addr)
	if err != nil {
		log.Fatalf("nonce: %v", err)
	}

	gp, err := client.SuggestGasPrice(ctx)
	if err != nil {
		log.Fatalf("gas price: %v", err)
	}

	msg := ethereum.CallMsg{From: addr, Data: initCode}
	gas, err := client.EstimateGas(ctx, msg)
	if err != nil {
		log.Fatalf("estimate gas (fund deployer with C2FLR?): %v", err)
	}

	tx := types.NewContractCreation(nonce, big.NewInt(0), gas, gp, initCode)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), pk)
	if err != nil {
		log.Fatalf("sign: %v", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("send deploy: %v", err)
	}
	log.Printf("deploy tx %s — waiting…", signed.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, client, signed)
	if err != nil {
		log.Fatalf("wait deploy: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("deploy failed status=%d", receipt.Status)
	}
	nftAddr := receipt.ContractAddress
	log.Printf("Mock721 deployed at %s", nftAddr.Hex())

	mintABI, err := abi.JSON(strings.NewReader(`[{"inputs":[{"name":"to","type":"address"}],"name":"mint","outputs":[{"name":"id","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`))
	if err != nil {
		log.Fatal(err)
	}
	calldata, err := mintABI.Pack("mint", recipient)
	if err != nil {
		log.Fatalf("pack mint: %v", err)
	}

	nonce, err = client.PendingNonceAt(ctx, addr)
	if err != nil {
		log.Fatalf("nonce2: %v", err)
	}
	mintTx := types.NewTransaction(nonce, nftAddr, big.NewInt(0), 200_000, gp, calldata)
	signedMint, err := types.SignTx(mintTx, types.LatestSignerForChainID(chainID), pk)
	if err != nil {
		log.Fatalf("sign mint: %v", err)
	}
	if err := client.SendTransaction(ctx, signedMint); err != nil {
		log.Fatalf("send mint: %v", err)
	}
	log.Printf("mint tx %s — waiting…", signedMint.Hash().Hex())
	mr, err := bind.WaitMined(ctx, client, signedMint)
	if err != nil {
		log.Fatalf("wait mint: %v", err)
	}
	if mr.Status != types.ReceiptStatusSuccessful {
		log.Fatalf("mint failed status=%d", mr.Status)
	}

	log.Printf("minted to %s — add collection to NEXT_PUBLIC_TRACKED_COLLECTIONS or list from this deployer", recipient.Hex())
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func loadOrCreatePrivateKey() (*ecdsa.PrivateKey, common.Address, error) {
	raw := strings.TrimSpace(os.Getenv("PRIVATE_KEY"))
	if raw == "" {
		k, err := crypto.GenerateKey()
		if err != nil {
			return nil, common.Address{}, err
		}
		hexKey := "0x" + hex.EncodeToString(crypto.FromECDSA(k))
		log.Printf("PRIVATE_KEY was unset — generated key (fund this address on Coston2, then re-run with PRIVATE_KEY=%s)", hexKey)
		addr := crypto.PubkeyToAddress(k.PublicKey)
		return k, addr, nil
	}
	raw = strings.TrimPrefix(raw, "0x")
	b, err := hex.DecodeString(raw)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("PRIVATE_KEY hex: %w", err)
	}
	k, err := crypto.ToECDSA(b)
	if err != nil {
		return nil, common.Address{}, err
	}
	return k, crypto.PubkeyToAddress(k.PublicKey), nil
}
