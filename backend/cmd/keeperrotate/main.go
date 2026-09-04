// keeperrotate: one-shot operational tool for rotating the marketplace keeper
// wallet. It never prints private keys to stdout; the generated key is written
// only to the file named by -out.
//
// Modes:
//
//	-derive <hexkey-file-or-env>   print the address a private key controls
//	-gen -out <file>               generate a fresh secp256k1 keeper key
//	-set -granter <key> -to <addr> -manager <addr> -rpc <url>
//	                               MarketplaceManager.setKeeper(to), signed by
//	                               the ADMIN wallet (v3.2+ removed all grant
//	                               paths; the single keeper is REPLACED, never
//	                               added)
//	-fund -granter <key> -to <addr> -wei <amount> -rpc <url>
//	                               native transfer to the new keeper
//	-transfer-admin -granter <key> -to <addr> -manager <addr> -rpc <url>
//	                               MarketplaceManager.transferAdmin(to), signed
//	                               by the CURRENT admin: step 1 of the 2-step
//	                               admin rotation (nothing changes until the
//	                               new key accepts)
//	-accept-admin -granter <key> -manager <addr> -rpc <url>
//	                               MarketplaceManager.acceptAdmin(), signed by
//	                               the NEW admin key (the pending admin): step
//	                               2 -- from this block the old key has no
//	                               power on the manager or any core
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

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain/profile"
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
	setK := flag.Bool("set", false, "setKeeper(to) as the admin (replaces the single keeper)")
	fund := flag.Bool("fund", false, "send native value")
	transferAdmin := flag.Bool("transfer-admin", false, "transferAdmin(to) as the CURRENT admin (step 1 of 2)")
	acceptAdmin := flag.Bool("accept-admin", false, "acceptAdmin() as the PENDING admin (step 2 of 2)")
	granter := flag.String("granter", "", "hex private key of the sender (ADMIN for -set/-transfer-admin; NEW admin for -accept-admin)")
	to := flag.String("to", "", "target address")
	manager := flag.String("manager", "", "MarketplaceManager address")
	chain := flag.Uint64("chain", 114, "chain id; selects the default -rpc from the network profile (114 Coston2, 19 Songbird, 14 Flare)")
	rpc := flag.String("rpc", "", "RPC URL (default: the -chain profile's primary RPC)")
	wei := flag.String("wei", "", "amount in wei for -fund")
	flag.Parse()

	if *rpc == "" {
		prof, err := profile.For(*chain)
		if err != nil {
			die("%v", err)
		}
		*rpc = prof.DefaultRPCs[0]
	}

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

	case *setK, *fund, *transferAdmin, *acceptAdmin:
		if *granter == "" {
			die("need -granter")
		}
		if *to == "" && !*acceptAdmin {
			die("need -to")
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
		if *setK {
			if *manager == "" {
				die("-set requires -manager")
			}
			mgr := common.HexToAddress(*manager)
			// MarketplaceManager.setKeeper(address) — ADMIN-only since v3.2.
			// The old -grant mode called addKeeper (0x4032b72b), which no
			// longer exists anywhere in the contract set; the single keeper
			// is REPLACED via setKeeper, and after renounceAdmin() nobody can.
			data := append([]byte{0x74, 0x87, 0x47, 0xe6}, // setKeeper(address)
				common.LeftPadBytes(common.HexToAddress(*to).Bytes(), 32)...)
			tx = types.NewTransaction(nonce, mgr, big.NewInt(0), 200_000, gasPrice, data)
		} else if *transferAdmin {
			if *manager == "" {
				die("-transfer-admin requires -manager")
			}
			mgr := common.HexToAddress(*manager)
			// MarketplaceManager.transferAdmin(address) — step 1 of the 2-step
			// admin rotation. Signed by the CURRENT admin; sets pendingAdmin
			// only. Selector: `cast sig "transferAdmin(address)"`.
			data := append([]byte{0x75, 0x82, 0x9d, 0xef}, // transferAdmin(address)
				common.LeftPadBytes(common.HexToAddress(*to).Bytes(), 32)...)
			tx = types.NewTransaction(nonce, mgr, big.NewInt(0), 200_000, gasPrice, data)
		} else if *acceptAdmin {
			if *manager == "" {
				die("-accept-admin requires -manager")
			}
			mgr := common.HexToAddress(*manager)
			// MarketplaceManager.acceptAdmin() — step 2. Signed by the NEW key
			// (must equal pendingAdmin). Selector: `cast sig "acceptAdmin()"`.
			data := []byte{0x0e, 0x18, 0xb6, 0x81} // acceptAdmin()
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
