// keeperrotate: one-shot operational tool for rotating the marketplace keeper
// wallet. It never prints private keys to stdout; the generated key is written
// only to the file named by -out.
//
// Modes:
//
//	-derive <hexkey-file-or-env>   print the address a private key controls
//	-gen -out <file>               generate a fresh secp256k1 keeper key
//	-grant -granter <key> -to <addr> -manager <addr> -rpc <url>
//	                               grantRole(KEEPER_ROLE, to) from an existing
//	                               keeper or admin wallet
//	-fund -granter <key> -to <addr> -wei <amount> -rpc <url>
//	                               native transfer to the new keeper
package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "FATAL: "+f+"\n", a...); os.Exit(1) }

func loadKey(s string) *ecdsa.PrivateKey {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	k, err := crypto.HexToECDSA(s)
	if err != nil {
		die("bad private key: %v", err)
	}
	return k
}

func main() {
	derive := flag.String("derive", "", "hex private key: print its address")
	gen := flag.Bool("gen", false, "generate a new keeper key")
	out := flag.String("out", "", "file to write the generated key (0600)")
	grant := flag.Bool("grant", false, "grant KEEPER_ROLE")
	fund := flag.Bool("fund", false, "send native value")
	granter := flag.String("granter", "", "hex private key of keeper/admin sender")
	to := flag.String("to", "", "target address")
	manager := flag.String("manager", "", "MarketplaceManager address")
	rpc := flag.String("rpc", "https://coston2-api.flare.network/ext/C/rpc", "RPC URL")
	wei := flag.String("wei", "", "amount in wei for -fund")
	flag.Parse()

	switch {
	case *derive != "":
		fmt.Println(crypto.PubkeyToAddress(loadKey(*derive).PublicKey).Hex())

	case *gen:
		if *out == "" {
			die("-gen requires -out")
		}
		k, err := crypto.GenerateKey()
		if err != nil {
			die("generate: %v", err)
		}
		hexKey := fmt.Sprintf("%064x", k.D)
		if err := os.WriteFile(*out, []byte(hexKey+"\n"), 0o600); err != nil {
			die("write %s: %v", *out, err)
		}
		fmt.Println(crypto.PubkeyToAddress(k.PublicKey).Hex())

	case *grant, *fund:
		if *granter == "" || *to == "" {
			die("need -granter and -to")
		}
		key := loadKey(*granter)
		from := crypto.PubkeyToAddress(key.PublicKey)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		ec, err := ethclient.DialContext(ctx, *rpc)
		if err != nil {
			die("dial: %v", err)
		}
		chainID, err := ec.ChainID(ctx)
		if err != nil {
			die("chainid: %v", err)
		}
		nonce, err := ec.PendingNonceAt(ctx, from)
		if err != nil {
			die("nonce: %v", err)
		}
		gasPrice, err := ec.SuggestGasPrice(ctx)
		if err != nil {
			die("gasprice: %v", err)
		}
		// Coston2's pool minimum has been observed at 500+ gwei; double the
		// suggestion so the tx clears the floor with headroom.
		gasPrice = new(big.Int).Mul(gasPrice, big.NewInt(2))

		var tx *types.Transaction
		if *grant {
			if *manager == "" {
				die("-grant requires -manager")
			}
			mgr := common.HexToAddress(*manager)
			// MarketplaceManager.addKeeper(address) — callable by an existing
			// keeper or the admin; AccessControl.grantRole would require the
			// role admin and reverts for a keeper caller.
			data := append([]byte{0x40, 0x32, 0xb7, 0x2b}, // addKeeper(address)
				common.LeftPadBytes(common.HexToAddress(*to).Bytes(), 32)...)
			tx = types.NewTransaction(nonce, mgr, big.NewInt(0), 200_000, gasPrice, data)
		} else {
			amt, ok := new(big.Int).SetString(*wei, 10)
			if !ok {
				die("bad -wei")
			}
			tx = types.NewTransaction(nonce, common.HexToAddress(*to), amt, 21_000, gasPrice, nil)
		}
		signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
		if err != nil {
			die("sign: %v", err)
		}
		if err := ec.SendTransaction(ctx, signed); err != nil {
			die("send: %v", err)
		}
		fmt.Printf("tx %s from %s\n", signed.Hash().Hex(), from.Hex())
		for i := 0; i < 45; i++ {
			r, err := ec.TransactionReceipt(ctx, signed.Hash())
			if err == nil {
				fmt.Printf("status=%d block=%d\n", r.Status, r.BlockNumber)
				if r.Status != 1 {
					os.Exit(1)
				}
				return
			}
			time.Sleep(2 * time.Second)
		}
		die("receipt timeout")

	default:
		flag.Usage()
		os.Exit(2)
	}
}
